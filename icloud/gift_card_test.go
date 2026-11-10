package icloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const giftFixturePage = `<html><p>Apply to owner@example.test.</p><form id="redeemForm" submit-url="https://buy.itunes.apple.com/commerce/redeemCodeSrv"><input id="codeField"></form><form submit-url="https://buy.itunes.apple.com/unrelated"></form></html>`
const giftFixtureCode = "X000000000000000" // Synthetic, never sent to Apple.

func giftFixture(t *testing.T, post func(*http.Request) (*http.Response, error)) (*StoreCommerceSession, *int) {
	t.Helper()
	gets := 0
	s := &StoreCommerceSession{identity: &storeSession{DSID: "test-dsid", PasswordToken: "private-token", GUID: "test-guid"}, frameworkEmail: "owner@example.test", commerceUserAgent: "fixture", client: &http.Client{Jar: mustCookieJar()}}
	s.client.Transport = testStoreTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("X-Token") != "private-token" {
			t.Fatal("missing bound Store identity")
		}
		if r.Method == "GET" {
			gets++
			if r.URL.String() != giftLanding {
				t.Fatal("unexpected read")
			}
			return &http.Response{StatusCode: 200, Header: http.Header{"Set-Cookie": {"page=bound; Path=/; Secure"}}, Body: io.NopCloser(strings.NewReader(giftFixturePage)), Request: r}, nil
		}
		if r.URL.String() != "https://buy.itunes.apple.com/commerce/redeemCodeSrv" {
			t.Fatal("unexpected submission endpoint")
		}
		cookie, err := r.Cookie("page")
		if err != nil || cookie.Value != "bound" {
			t.Fatal("landing cookies lost")
		}
		data, _ := io.ReadAll(r.Body)
		v, _ := url.ParseQuery(string(data))
		if v.Get("code") != giftFixtureCode || v.Get("dsPersonId") != "test-dsid" || v.Get("attemptCount") != "1" || v.Get("response-content-type") != "application/json" {
			t.Fatal("invalid desktop form contract")
		}
		return post(r)
	})
	return s, &gets
}
func TestGiftLandingBinding(t *testing.T) {
	if _, err := parseGiftLanding([]byte(giftFixturePage), "owner@example.test"); err != nil {
		t.Fatal(err)
	}
	for _, page := range []string{
		strings.ReplaceAll(giftFixturePage, "owner@example.test", "other@example.test"),
		strings.ReplaceAll(giftFixturePage, "https://buy.itunes.apple.com/commerce/redeemCodeSrv", "https://evil.example/commerce/redeemCodeSrv"),
		strings.ReplaceAll(giftFixturePage, "/commerce/redeemCodeSrv", "/commerce/redeemCodeSrv?code=private"),
		strings.ReplaceAll(giftFixturePage, "/commerce/redeemCodeSrv", "/commerce/buyProduct"),
		strings.ReplaceAll(giftFixturePage, "codeField", "other"), giftFixturePage + giftFixturePage,
	} {
		if _, err := parseGiftLanding([]byte(page), "owner@example.test"); err == nil {
			t.Fatal("unbound page accepted")
		}
	}
}
func TestGiftSubmissionConsumesConfirmationAndPreservesCookieJar(t *testing.T) {
	posts := 0
	s, gets := giftFixture(t, func(r *http.Request) (*http.Response, error) {
		posts++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"status":0,"email":"owner@example.test","redeemedCredit":{"money":"$5.00"},"totalCredit":{"money":"$6.00"}}`)), Request: r}, nil
	})
	pre, err := s.PrepareGiftCard(context.Background())
	if err != nil || !pre.Ready || posts != 0 || *gets != 1 {
		t.Fatalf("prepare: %v", err)
	}
	result, err := s.RedeemGiftCard(context.Background(), pre.PreparationID, giftFixtureCode)
	if err != nil || result.State != "completed" || result.Balance != "$6.00" || posts != 1 {
		t.Fatalf("result: %+v %v", result, err)
	}
	if _, err = s.RedeemGiftCard(context.Background(), pre.PreparationID, giftFixtureCode); err == nil || posts != 1 {
		t.Fatal("confirmation replay")
	}
	pre, err = s.PrepareGiftCard(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RedeemGiftCard(context.Background(), pre.PreparationID, giftFixtureCode); err == nil || posts != 1 {
		t.Fatal("same code resubmitted")
	}
	data, _ := json.Marshal(result)
	for _, secret := range []string{giftFixtureCode, "private-token", "test-dsid", "owner@example.test"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("private value exposed")
		}
	}
}
func TestGiftAmbiguousFailureAndConcurrencyDoNotReplay(t *testing.T) {
	for _, redirect := range []bool{false, true} {
		posts := 0
		s, _ := giftFixture(t, func(r *http.Request) (*http.Response, error) {
			posts++
			if redirect {
				return &http.Response{StatusCode: 307, Header: http.Header{"Location": {"https://other.example/"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
			}
			return nil, errors.New("timeout")
		})
		pre, err := s.PrepareGiftCard(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); s.RedeemGiftCard(context.Background(), pre.PreparationID, giftFixtureCode) }()
		}
		wg.Wait()
		if posts != 1 {
			t.Fatal("ambiguous mutation replayed")
		}
	}
}
func TestGiftExpiryAndInputRejectBeforeMutation(t *testing.T) {
	s, _ := giftFixture(t, func(*http.Request) (*http.Response, error) { t.Fatal("unexpected mutation"); return nil, nil })
	pre, _ := s.PrepareGiftCard(context.Background())
	if _, err := s.RedeemGiftCard(context.Background(), pre.PreparationID, "not a gift"); err == nil {
		t.Fatal("bad code accepted")
	}
	s.gift.expires = time.Now().Add(-time.Second)
	if _, err := s.RedeemGiftCard(context.Background(), pre.PreparationID, giftFixtureCode); err == nil {
		t.Fatal("expired confirmation accepted")
	}
}
func TestGiftResponseRequiresExplicitCreditResult(t *testing.T) {
	for _, body := range []string{`{}`, `{"status":0}`, `{"status":0,"errorNumber":9502}`, `{"status":3,"dialog":{"action":"buy"}}`, `{"status":0,"email":"other@example.test","redeemedCredit":{"money":"$5.00"},"totalCredit":{"money":"$5.00"}}`, `{"status":0,"redeemedCredit":{"money":"X000000000000000"},"totalCredit":{"money":"$5.00"}}`, `{"status":0,"salables":[{}],"redeemedCredit":{"money":"$5.00"},"totalCredit":{"money":"$5.00"}}`} {
		if result := parseGiftResult([]byte(body), "owner@example.test"); result.State == "completed" {
			t.Fatal("unverified credit marked complete")
		}
	}
}
