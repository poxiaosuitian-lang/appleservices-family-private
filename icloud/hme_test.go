package icloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestHideMyEmailLifecycleBindsCurrentAlias(t *testing.T) {
	var mu sync.Mutex
	active := true
	selectedForward := "primary@example.test"
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("dsid") != "123" || r.URL.Query().Get("clientBuildNumber") != hmeBuildNumber {
			http.Error(w, `{"success":false}`, http.StatusBadRequest)
			return
		}
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/hme/list":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{"forwardToEmails": []string{"primary@example.test", "backup@example.test"}, "selectedForwardTo": selectedForward, "hmeEmails": []map[string]any{{"hme": "alias@icloud.com", "label": "shop", "note": "test", "isActive": active, "anonymousId": "private-id", "forwardToEmail": selectedForward}}}})
		case "/v1/hme/generate":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{"hme": "candidate@icloud.com"}})
		case "/v1/hme/reserve", "/v1/hme/updateMetaData", "/v1/hme/reactivate", "/v1/hme/delete":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{}})
		case "/v1/hme/updateForwardTo":
			var request map[string]string
			if json.NewDecoder(r.Body).Decode(&request) != nil || request["forwardToEmail"] != "backup@example.test" {
				t.Fatal("forward target request was not bound to the selected address")
			}
			selectedForward = request["forwardToEmail"]
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{}})
		case "/v1/hme/deactivate":
			active = false
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &HideMyEmailClient{
		appleID:     "member@example.test",
		sessionPath: filepath.Join(t.TempDir(), "hme-session.json"),
		http:        server.Client(),
		jar:         jar,
		session:     hmeSession{AppleID: "member@example.test", DSID: "123", ClientID: "client", ServiceURL: server.URL},
	}
	client.http.Jar = jar
	ctx := context.Background()
	info, err := client.List(ctx)
	if err != nil || info.ActiveCount != 1 || len(info.Aliases) != 1 || info.Aliases[0].anonymousID != "private-id" || info.SelectedForward != "primary@example.test" || len(info.ForwardToEmails) != 2 {
		t.Fatalf("unexpected list info=%#v err=%v", info, err)
	}
	candidate, err := client.Generate(ctx, "zh-cn")
	if err != nil || candidate != "candidate@icloud.com" {
		t.Fatalf("unexpected candidate=%q err=%v", candidate, err)
	}
	if err := client.Reserve(ctx, candidate, "label", "note"); err != nil {
		t.Fatal(err)
	}
	if err := client.UpdateForwardTo(ctx, "unverified@example.test"); err == nil {
		t.Fatal("unverified forwarding target was accepted")
	}
	if err := client.UpdateForwardTo(ctx, "backup@example.test"); err != nil {
		t.Fatal(err)
	}
	forwarded, err := client.List(ctx)
	if err != nil || forwarded.SelectedForward != "backup@example.test" || forwarded.Aliases[0].ForwardToEmail != "backup@example.test" {
		t.Fatalf("forwarding change was not verified: %#v err=%v", forwarded, err)
	}
	if err := client.Update(ctx, "alias@icloud.com", "updated", "updated note"); err != nil {
		t.Fatal(err)
	}
	if err := client.Delete(ctx, "alias@icloud.com"); err == nil {
		t.Fatal("active alias was deleted")
	}
	if err := client.SetActive(ctx, "alias@icloud.com", false); err != nil {
		t.Fatal(err)
	}
	if err := client.Delete(ctx, "alias@icloud.com"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) < 8 || paths[len(paths)-1] != "/v1/hme/delete" {
		t.Fatalf("unexpected request paths %v", paths)
	}
}

func TestHideMyEmailSafeAliasOmitsPrivateIdentifier(t *testing.T) {
	data, err := json.Marshal(HideMyEmailAlias{Email: "alias@icloud.com", anonymousID: "private-id"})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"email":"alias@icloud.com","active":false}` {
		t.Fatalf("private identifier leaked in %s", data)
	}
}

func TestValidateHMEWebCookiesExplainsRepairState(t *testing.T) {
	if err := validateHMEWebCookies([]*http.Cookie{{Name: "X-APPLE-WEBAUTH-TOKEN", Value: "token"}}); err != nil {
		t.Fatal(err)
	}
	err := validateHMEWebCookies([]*http.Cookie{{Name: "X-APPLE-WEBAUTH-REPAIR", Value: "repair"}})
	if !errors.Is(err, ErrHMEWebRepairRequired) {
		t.Fatalf("repair state was not explained: %v", err)
	}
}

func TestAcceptHMEWebTermsUsesAdvertisedVersion(t *testing.T) {
	var accepted int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/getTerms":
			if r.Method != http.MethodGet {
				t.Fatalf("getTerms method = %s", r.Method)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"iCloudTerms": map[string]any{"version": 634301}})
		case "/repairDone":
			if r.Method != http.MethodGet {
				t.Fatalf("repairDone method = %s", r.Method)
			}
			var body struct {
				Accepted int `json:"acceptedICloudTerms"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Fatal("repairDone body was not valid JSON")
			}
			accepted = body.Accepted
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &HideMyEmailClient{http: server.Client(), jar: jar}
	if err := client.acceptHMEWebTerms(context.Background(), server.URL, "zh-cn"); err != nil {
		t.Fatal(err)
	}
	if accepted != 634301 {
		t.Fatalf("accepted terms version = %d", accepted)
	}
}

func TestAcceptPendingTermsRequiresRepairCookie(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	client := &HideMyEmailClient{setupBase: "https://setup.icloud.com/setup/ws/1", http: http.DefaultClient, jar: jar}
	if err := client.AcceptPendingTerms(context.Background()); !errors.Is(err, ErrHMEWebRepairSessionUnavailable) {
		t.Fatalf("missing repair session was not rejected: %v", err)
	}
}

func TestAcceptPendingTermsUsesInMemoryRepairStateWithoutRelogin(t *testing.T) {
	var logins, accepted int
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/accountLogin":
			logins++
			if accepted != 634301 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"dsInfo":            map[string]any{"dsid": "123", "languageCode": "en-us"},
					"termsUpdateNeeded": true,
					"webservices":       map[string]any{"mccgateway": map[string]any{"url": server.URL}},
				})
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "X-APPLE-WEBAUTH-TOKEN", Value: "web-token", Path: "/"})
			_ = json.NewEncoder(w).Encode(map[string]any{
				"dsInfo":            map[string]any{"dsid": "123", "languageCode": "en-us"},
				"termsUpdateNeeded": false,
				"webservices":       map[string]any{"mccgateway": map[string]any{"url": server.URL}},
			})
		case "/getTerms":
			_ = json.NewEncoder(w).Encode(map[string]any{"iCloudTerms": map[string]any{"version": 634301}})
		case "/repairDone":
			var body struct {
				Accepted int `json:"acceptedICloudTerms"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Fatal("repairDone body was not valid JSON")
			}
			accepted = body.Accepted
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	sessionPath := filepath.Join(t.TempDir(), "primary-mail-session.json")
	client := &HideMyEmailClient{
		appleID:     "member@example.test",
		serviceName: "mccgateway",
		sessionPath: sessionPath,
		setupBase:   server.URL,
		http:        &http.Client{Transport: server.Client().Transport, Jar: jar},
		jar:         &webSessionJar{CookieJar: jar},
	}
	err := client.accountLogin(context.Background(), &hmeAuthState{sessionToken: "sess", accountCountry: "USA", trustToken: "trust"}, false)
	if !errors.Is(err, ErrHMEWebRepairRequired) || client.pendingRepair == nil {
		t.Fatalf("expected ErrHMEWebRepairRequired with pendingRepair retained, got err=%v pendingRepair=%v", err, client.pendingRepair != nil)
	}
	if err := client.AcceptPendingTerms(context.Background()); err != nil {
		t.Fatalf("AcceptPendingTerms() failed: %v", err)
	}
	if accepted != 634301 || logins != 3 || client.pendingRepair != nil || client.session.DSID != "123" {
		t.Fatalf("unexpected repair state: accepted=%d logins=%d pendingRepair=%v dsid=%q", accepted, logins, client.pendingRepair != nil, client.session.DSID)
	}
	if _, statErr := os.Stat(sessionPath); statErr != nil {
		t.Fatalf("expected saved session after repair: %v", statErr)
	}
}
