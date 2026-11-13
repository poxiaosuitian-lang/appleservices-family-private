package icloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"howett.net/plist"
)

func TestSkillPurchaseRejectionDoesNotReplayOrExposeResponse(t *testing.T) {
	for _, afterConfirmation := range []bool{false, true} {
		calls := 0
		client := &http.Client{Transport: testStoreTransport(func(req *http.Request) (*http.Response, error) {
			calls++
			response := map[string]any{"failureType": "1234", "customerMessage": "Please update billing information for private@example.test", "authToken": "private-token"}
			if afterConfirmation && calls == 1 {
				offer, _ := url.Parse(DirectStorageOfferURL("TEST-GUID"))
				params := offer.Query()
				params.Set("hasConfirmAutoRenew", "true")
				response = map[string]any{"dialog": map[string]any{"okButtonAction": map[string]any{"kind": "Buy", "buyParams": params.Encode()}}}
			}
			data, _ := plist.Marshal(response, plist.XMLFormat)
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(data))), Request: req}, nil
		})}
		session := &StoreCommerceSession{client: client, identity: &storeSession{DSID: "123", PasswordToken: "test-token", GUID: "TEST-GUID", StoreFront: "143465"}}
		target := PurchaseTarget{Bytes: 53687091200, PriceMinor: 600, Currency: "CNY", Period: "month"}
		info := &StoragePlanInfo{Plans: []StoragePlan{{Bytes: target.Bytes, Price: 600, Currency: "CNY", Period: "month", Available: true}}}
		result, err := session.SubmitSkillPurchase(context.Background(), info, target, "")
		var failure *StorageError
		if result != nil || !errors.As(err, &failure) || failure.Hint != "billing-confirmation" || failure.AppleCode != "1234" {
			t.Fatalf("business rejection lost: %v", err)
		}
		wantCalls := 1
		if afterConfirmation {
			wantCalls = 2
		}
		if calls != wantCalls {
			t.Fatal("purchase rejection replayed")
		}
		if !session.MatchesAccount("123") || session.NeedsReauthentication() {
			t.Fatal("billing failure incorrectly invalidated Store credentials")
		}
		encoded, _ := json.Marshal(failure)
		if strings.Contains(string(encoded), "private") {
			t.Fatal("authenticated response leaked")
		}
	}
}

func TestSkillPurchaseFailureClassificationIsBounded(t *testing.T) {
	for _, value := range []any{nil, "", "0", 0} {
		if skillPurchaseRejection(map[string]any{"failureType": value}) != nil {
			t.Fatal("zero failure rejected")
		}
	}
	err := skillPurchaseRejection(map[string]any{"failureType": "token=private", "url": "https://example.test/payment?secret=private"}).(*StorageError)
	if err.AppleCode != "" || err.Hint == "payment-confirmation" {
		t.Fatal("untrusted field exposed or used for diagnosis")
	}
}

func TestStoreSessionRejectionClearsLocalReadiness(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		session := &StoreCommerceSession{
			identity: &storeSession{DSID: "test-dsid", PasswordToken: "synthetic-token"},
			client: &http.Client{Transport: testStoreTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet {
					t.Fatal("read-only check submitted a mutation")
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
			})},
		}
		if !session.MatchesAccount("test-dsid") {
			t.Fatal("fixture not ready")
		}
		_, err := session.ReadSkillSubscription(context.Background())
		if err == nil || session.MatchesAccount("test-dsid") || !session.NeedsReauthentication() {
			t.Fatal("rejected credentials remained ready")
		}
	}
}

func TestSkillPurchaseExpiredTokenInvalidatesWithoutReplay(t *testing.T) {
	for _, afterConfirmation := range []bool{false, true} {
		calls := 0
		client := &http.Client{Transport: testStoreTransport(func(req *http.Request) (*http.Response, error) {
			calls++
			response := map[string]any{"failureType": "2034", "customerMessage": "private@example.test must sign in", "authToken": "private-token"}
			if afterConfirmation && calls == 1 {
				offer, _ := url.Parse(DirectStorageOfferURL("TEST-GUID"))
				params := offer.Query()
				params.Set("hasConfirmAutoRenew", "true")
				response = map[string]any{"dialog": map[string]any{"okButtonAction": map[string]any{"kind": "Buy", "buyParams": params.Encode()}}}
			}
			data, _ := plist.Marshal(response, plist.XMLFormat)
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
		})}
		checkpoint := []byte("synthetic-private-checkpoint")
		session := &StoreCommerceSession{client: client,
			identity:       &storeSession{DSID: "123", PasswordToken: "test-token", GUID: "TEST-GUID", StoreFront: "143465"},
			frameworkState: []byte("synthetic-machine-state"), frameworkEmail: "owner@example.test", frameworkDSID: "123", frameworkGUID: "TEST-GUID",
			frameworkCheckpoint: checkpoint}
		target := PurchaseTarget{Bytes: 53687091200, PriceMinor: 600, Currency: "CNY", Period: "month"}
		info := &StoragePlanInfo{Plans: []StoragePlan{{Bytes: target.Bytes, Price: 600, Currency: "CNY", Period: "month", Available: true}}}
		result, err := session.SubmitSkillPurchase(context.Background(), info, target, "")
		var failure *StorageError
		if result != nil || !errors.As(err, &failure) || failure.Stage != "purchase-store-token-expired" || failure.AppleCode != "2034" || failure.HTTPStatus != 200 {
			t.Fatalf("expiry not classified: %v", err)
		}
		wantCalls := 1
		if afterConfirmation {
			wantCalls = 2
		}
		if calls != wantCalls || session.MatchesAccount("123") || !session.NeedsReauthentication() {
			t.Fatal("expired token replayed or kept ready")
		}
		if !session.CanReuseFrameworkState("owner@example.test", "123") || len(session.frameworkCheckpoint) != 0 || !bytes.Equal(checkpoint, make([]byte, len(checkpoint))) {
			t.Fatal("invalidation lost provisioning state or retained rejected credentials")
		}
		if _, err := session.SubmitSkillPurchase(context.Background(), info, target, ""); err == nil || calls != wantCalls {
			t.Fatal("rejected session submitted another purchase")
		}
		encoded, _ := json.Marshal(failure)
		if strings.Contains(string(encoded), "private") {
			t.Fatal("authenticated response leaked")
		}
	}
}

func TestSkillPurchaseMissingSessionDoesNotPanic(t *testing.T) {
	for _, session := range []*StoreCommerceSession{nil, {}, {client: &http.Client{}}} {
		_, err := session.SubmitSkillPurchase(context.Background(), nil, PurchaseTarget{}, "")
		var failure *StorageError
		if !errors.As(err, &failure) || failure.Stage != "purchase-store-session" {
			t.Fatalf("missing session not rejected: %v", err)
		}
	}
}
