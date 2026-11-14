package hmesync

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerLoginOrchestration(t *testing.T) {
	for _, mode := range []string{"already-ready", "auto-code", "manual-needed", "provider-failed", "sms", "bad-source", "old-server", "redirect", "rejected", "manual-verify"} {
		t.Run(mode, func(t *testing.T) {
			calls := []string{}
			generated := 0
			challenge := strings.Repeat("a", 64)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != LoginPath || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("k", 32) {
					t.Error("wrong endpoint or credential")
				}
				var input loginRequest
				if json.NewDecoder(r.Body).Decode(&input) != nil {
					t.Fatal("invalid request")
				}
				calls = append(calls, input.Operation)
				if input.Operation == "login" && input.Password != "synthetic-password" {
					t.Error("password missing")
				}
				if input.Operation != "login" && input.Password != "" {
					t.Error("unneeded password transfer")
				}
				state := "ready"
				nonce := ""
				source := SourceID("owner@example.test")
				switch mode {
				case "old-server":
					w.WriteHeader(404)
					w.Write([]byte("not found"))
					return
				case "redirect":
					w.Header().Set("Location", "/elsewhere")
					w.WriteHeader(307)
					return
				case "rejected":
					w.WriteHeader(401)
					w.Write([]byte(`{"success":false,"code":"IMPORT_UNAUTHORIZED","message":"synthetic-password"}`))
					return
				case "bad-source":
					source = "wrong"
				}
				if mode != "already-ready" && input.Operation == "status" {
					state = "login_required"
				}
				if input.Operation == "login" || (input.Operation == "verify" && mode == "manual-needed") {
					state = "otp_required"
					nonce = challenge
				}
				if input.Operation == "verify" && (input.Code != "123456" || input.Challenge != challenge) {
					t.Error("incorrect challenge binding")
				}
				method := "trusted_device"
				if mode == "sms" {
					method = "sms"
				}
				json.NewEncoder(w).Encode(map[string]any{"success": true, "data": LoginResult{SourceID: source, State: state, Challenge: nonce, Verification: method}})
			}))
			defer server.Close()
			provider := func(context.Context) (string, error) {
				generated++
				if mode == "provider-failed" {
					return "", errors.New("fixture")
				}
				return "123456", nil
			}
			code, nonce := "", ""
			if mode == "manual-verify" {
				code, nonce = "123456", challenge
			}
			result, err := loginWithClient(context.Background(), server.Client(), server.URL, strings.Repeat("k", 32), "owner@example.test", "synthetic-password", code, nonce, provider)
			switch mode {
			case "old-server", "redirect", "rejected", "bad-source":
				if err == nil || strings.Contains(err.Error(), "synthetic-password") || len(calls) != 1 || generated != 0 {
					t.Fatal("unsafe failure/retry")
				}
			case "already-ready", "manual-verify":
				if err != nil || result.State != "ready" || len(calls) != 1 || generated != 0 {
					t.Fatal("unneeded login or OTP generation")
				}
			case "auto-code":
				if err != nil || result.State != "ready" || len(calls) != 3 || generated != 1 {
					t.Fatal("automatic flow failed")
				}
			case "manual-needed":
				if err != nil || result.State != "otp_required" || len(calls) != 3 || generated != 1 {
					t.Fatal("invalid code was retried")
				}
			case "sms":
				if err != nil || result.State != "otp_required" || len(calls) != 2 || generated != 0 {
					t.Fatal("local code attempted for SMS challenge")
				}
			case "provider-failed":
				if err != nil || result.State != "otp_required" || len(calls) != 2 || generated != 1 {
					t.Fatal("provider failure was not recoverable")
				}
			}
		})
	}
}
