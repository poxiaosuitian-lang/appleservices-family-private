package hmesync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEndpointRejectsCredentialLeakDestinations(t *testing.T) {
	for _, base := range []string{"http://example.com", "https://user:pass@example.com", "https://example.com?key=x", "https://example.com#x", "//example.com"} {
		if _, err := Endpoint(base); err == nil {
			t.Fatalf("accepted %q", base)
		}
	}
	if got, err := Endpoint("https://example.com/hme/"); err != nil || got != "https://example.com/hme"+Path {
		t.Fatal(got, err)
	}
}

func TestImportValidatesAcknowledgementAndNeverRedirectsOrLeaks(t *testing.T) {
	p := Payload{Version: 1, AppleID: "owner@example.test", Name: "fixture", ICloudEmail: "owner@icloud.com", Host: "icloud.com", Cookies: map[string]string{"X-APPLE-WEBAUTH-TOKEN": "secret-cookie"}, AppPassword: "abcd-efgh-ijkl-mnop"}
	p.SourceID = SourceID(p.AppleID)
	for _, mode := range []string{"ok", "redirect", "rejected", "partial", "wrong-account", "echo", "missing"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != Path {
					t.Error("followed redirect")
				}
				if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("k", 32) {
					t.Error("missing authorization")
				}
				var received Payload
				_ = json.NewDecoder(r.Body).Decode(&received)
				if received.SourceID != p.SourceID || received.AppPassword != p.AppPassword {
					t.Error("payload mismatch")
				}
				switch mode {
				case "redirect":
					w.Header().Set("Location", "/other")
					w.WriteHeader(307)
					return
				case "rejected":
					w.WriteHeader(401)
					return
				case "missing":
					w.WriteHeader(404)
					return
				case "echo":
					w.Write([]byte(`{"message":"secret-cookie abcd-efgh-ijkl-mnop"}`))
					return
				}
				source := p.SourceID
				if mode == "wrong-account" {
					source = "other"
				}
				json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"id": "acc_test", "source_id": source, "status": "active", "hme_verified": true, "imap_verified": mode != "partial"}})
			}))
			defer server.Close()
			id, err := importWithClient(context.Background(), server.URL, strings.Repeat("k", 32), p, server.Client())
			if mode == "ok" {
				if err != nil || id != "acc_test" {
					t.Fatal(id, err)
				}
			} else {
				if err == nil {
					t.Fatal("must reject")
				}
				if strings.Contains(err.Error(), p.AppPassword) || strings.Contains(err.Error(), "secret-cookie") {
					t.Fatal("secret leaked")
				}
			}
			if calls != 1 {
				t.Fatal("request replayed")
			}
		})
	}
}
