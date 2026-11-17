package hmesync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebRecoveryBoundTransferAndNoRedirect(t *testing.T) {
	for _, mode := range []string{"ready", "wrong-account", "redirect", "not-verified"} {
		t.Run(mode, func(t *testing.T) {
			source := SourceID("owner@example.test")
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var payload map[string]json.RawMessage
				if json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload) != 8 || r.URL.Path != RecoveryPath+"web-session" {
					t.Error("invalid scoped transfer")
				}
				for _, key := range []string{"password", "app_password", "name"} {
					if _, ok := payload[key]; ok {
						t.Error("unrelated credentials sent")
					}
				}
				if mode == "redirect" {
					w.Header().Set("Location", "/unexpected")
					w.WriteHeader(307)
					return
				}
				returned := source
				if mode == "wrong-account" {
					returned = SourceID("other@example.test")
				}
				json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"source_id": returned, "state": "ready", "web_verified": mode != "not-verified"}})
			}))
			defer server.Close()
			client := &RecoveryClient{server.Client()}
			client.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			result, err := client.refreshWebSession(context.Background(), server.URL, strings.Repeat("k", 6), WebRecoveryPayload{Version: 1, ClientID: strings.Repeat("c", 32), JobID: "job_fixture", Lease: strings.Repeat("a", 64), SourceID: source, AppleID: "owner@example.test", Host: "icloud.com", Cookies: map[string]string{"fixture": "test-secret"}})
			if (mode == "ready") != (err == nil) || calls != 1 {
				t.Fatal("unverified recovery or automatic retry")
			}
			if err == nil && (result.State != "ready" || result.SourceID != source) {
				t.Fatal("wrong result binding")
			}
		})
	}
}
