package icloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func appPasswordTestClient(t *testing.T, handler http.HandlerFunc) *AppleAccountClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &AppleAccountClient{apiBase: server.URL, apiKey: "fixture-api-key", http: server.Client()}
}

func TestListAppSpecificPasswordsReturnsMetadataOnly(t *testing.T) {
	client := appPasswordTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != appSpecificPasswordPath || r.Header.Get("X-Apple-Api-Key") != "fixture-api-key" {
			t.Error("list request did not match the account portal protocol")
		}
		io.WriteString(w, `[{"id":9007199254740993,"description":"subscription-mail","localizedCreateDate":"Today","password":"never-return-this"},{"id":"fixture-id","description":"calendar"}]`)
	})
	items, err := client.ListAppSpecificPasswords(context.Background())
	if err != nil || len(items) != 2 || items[0].ID != "9007199254740993" || items[1].ID != "fixture-id" || items[0].Label != "subscription-mail" {
		t.Fatal("list metadata did not decode correctly")
	}
	if items[0].Password != "" || items[1].Password != "" {
		t.Fatal("list output included a password")
	}
	data, err := json.Marshal(AppSpecificPassword{ID: "1", Label: "mail", Password: "must-not-serialize"})
	if err != nil || strings.Contains(string(data), "password") || strings.Contains(string(data), "must-not-serialize") {
		t.Fatal("app password JSON exposed the one-time secret")
	}
}

func TestListAppSpecificPasswordsRejectsUnexpectedShapes(t *testing.T) {
	for _, body := range []string{`null`, `{"passwords":[]}`, `{"unknown":null}`, `{} {}`, `[{}]`, `[{"id":0,"description":"mail"}]`, `[{"id":1.5,"description":"mail"}]`, `[{"id":true,"description":"mail"}]`} {
		t.Run(body, func(t *testing.T) {
			client := appPasswordTestClient(t, func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, body) })
			items, err := client.ListAppSpecificPasswords(context.Background())
			var failure *AppSpecificPasswordError
			if items != nil || !errors.As(err, &failure) || failure.CreationUncertain {
				t.Fatal("unexpected metadata shape was accepted")
			}
		})
	}
	for _, body := range []string{`[]`, `{}`, " \n { \t } \n"} {
		t.Run("empty:"+body, func(t *testing.T) {
			client := appPasswordTestClient(t, func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, body) })
			if items, err := client.ListAppSpecificPasswords(context.Background()); err != nil || items == nil || len(items) != 0 {
				t.Fatal("empty list was rejected")
			}
		})
	}
}

func TestCreateAppSpecificPasswordUsesVerifiedPayload(t *testing.T) {
	calls := 0
	client := appPasswordTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		var payload map[string]string
		if r.Method != http.MethodPost || r.URL.Path != appSpecificPasswordPath || json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload) != 1 || payload["description"] != "subscription-mail" {
			t.Error("create request did not match the account portal protocol")
		}
		io.WriteString(w, `{"id":42,"description":"subscription-mail","createDatetime":"2026-01-01","localizedCreateDate":"Today","password":"abcdefghijklmnop"}`)
	})
	result, err := client.CreateAppSpecificPassword(context.Background(), " subscription-mail ", "fixture-account-password")
	if err != nil || result == nil || result.ID != "42" || result.Label != "subscription-mail" || result.Password != "abcdefghijklmnop" || calls != 1 {
		t.Fatal("create did not return the one-time result")
	}
}

func TestCreateAppSpecificPasswordReplaysOnlyAfterPasswordChallenge(t *testing.T) {
	var calls []string
	client := appPasswordTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		switch len(calls) {
		case 1:
			w.Header().Set("scnt", "fixture-challenge-scnt")
			w.WriteHeader(451)
			io.WriteString(w, `{"formattedAccountName":"masked-account"}`)
		case 2:
			var payload map[string]string
			if r.URL.Path != "/authenticate/password" || r.Header.Get("scnt") != "fixture-challenge-scnt" || json.NewDecoder(r.Body).Decode(&payload) != nil || payload["password"] != "fixture-account-password" || len(payload) != 1 {
				t.Error("password challenge did not match the account portal protocol")
			}
			w.Header().Set("scnt", "fixture-confirmed-scnt")
			w.WriteHeader(http.StatusNoContent)
		case 3:
			if r.URL.Path != appSpecificPasswordPath || r.Header.Get("scnt") != "fixture-confirmed-scnt" {
				t.Error("creation was not replayed with the confirmed session")
			}
			io.WriteString(w, `{"id":7,"description":"mail","password":"abcdefghijklmnop"}`)
		default:
			t.Error("creation replayed more than once")
		}
	})
	result, err := client.CreateAppSpecificPassword(context.Background(), "mail", "fixture-account-password")
	if err != nil || result == nil || len(calls) != 3 {
		t.Fatal("password challenge was not completed exactly once")
	}
}

func TestCreateAppSpecificPasswordStopsAtAdditionalVerification(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		password string
	}{
		{"on-demand-2fa", 451, `{"onDemand2FAToken":"fixture-secret-token"}`, "fixture-password"},
		{"on-demand-marker-without-token", 451, `{"onDemand2FAToken":null}`, "fixture-password"},
		{"malformed-challenge", 451, `not-json`, "fixture-password"},
		{"null-challenge", 451, `null`, "fixture-password"},
		{"no-password", 451, `{}`, ""},
		{"native-takeover", 466, `{"message":"fixture-secret-token"}`, "fixture-password"},
		{"critical-account-edit", 467, `{}`, "fixture-password"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := appPasswordTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			})
			result, err := client.CreateAppSpecificPassword(context.Background(), "mail", tc.password)
			var failure *AppSpecificPasswordError
			if result != nil || !errors.As(err, &failure) || !errors.Is(err, ErrAppSpecificPasswordVerificationRequired) || failure.CreationUncertain || calls != 1 {
				t.Fatal("extra verification did not stop creation")
			}
			if strings.Contains(err.Error(), "fixture-") {
				t.Fatal("failure exposed authentication data")
			}
		})
	}
}

func TestCreateAppSpecificPasswordLimitsPasswordChallengeReplay(t *testing.T) {
	for _, authStatus := range []int{http.StatusConflict, http.StatusNoContent} {
		t.Run(fmt.Sprint(authStatus), func(t *testing.T) {
			calls := 0
			client := appPasswordTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path == "/authenticate/password" {
					w.WriteHeader(authStatus)
					return
				}
				w.WriteHeader(451)
				io.WriteString(w, `{}`)
			})
			_, err := client.CreateAppSpecificPassword(context.Background(), "mail", "fixture-password")
			var failure *AppSpecificPasswordError
			if !errors.As(err, &failure) || failure.CreationUncertain {
				t.Fatal("definite challenge rejection was not preserved")
			}
			wantCalls := 2
			if authStatus == http.StatusNoContent {
				wantCalls = 3
			}
			if calls != wantCalls {
				t.Fatal("password challenge replay exceeded its bound")
			}
		})
	}
}

func TestCreateAppSpecificPasswordNeverReplaysUncertainCreation(t *testing.T) {
	cases := []struct {
		status int
		body   string
	}{
		{http.StatusOK, `{"id":1,"description":"mail"}`},
		{http.StatusOK, `{"id":1,"description":"other","password":"fixture-password"}`},
		{http.StatusOK, `not-json`},
		{http.StatusAccepted, `{}`},
		{http.StatusRequestTimeout, `fixture-password`},
		{http.StatusInternalServerError, `fixture-password`},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.status)+tc.body, func(t *testing.T) {
			calls := 0
			client := appPasswordTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			})
			_, err := client.CreateAppSpecificPassword(context.Background(), "mail", "fixture-password")
			var failure *AppSpecificPasswordError
			if !errors.As(err, &failure) || !errors.Is(err, ErrAppSpecificPasswordCreationUncertain) || !failure.CreationUncertain || calls != 1 || strings.Contains(err.Error(), "fixture-") {
				t.Fatal("uncertain creation was replayed or did not return a safe typed error")
			}
		})
	}
}

func TestCreateAppSpecificPasswordTransportFailureIsUncertain(t *testing.T) {
	calls := 0
	client := appPasswordTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error("test connection could not be closed")
			return
		}
		connection.Close()
	})
	_, err := client.CreateAppSpecificPassword(context.Background(), "mail", "fixture-password")
	if !errors.Is(err, ErrAppSpecificPasswordCreationUncertain) || calls != 1 {
		t.Fatal("transport failure was not treated as uncertain")
	}
}

func TestAppSpecificPasswordPreflightMakesNoRequest(t *testing.T) {
	client := appPasswordTestClient(t, func(_ http.ResponseWriter, _ *http.Request) { t.Error("preflight rejection made a request") })
	if _, err := client.CreateAppSpecificPassword(context.Background(), " ", "fixture-password"); err == nil {
		t.Fatal("blank label was accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.CreateAppSpecificPassword(ctx, "mail", "fixture-password"); err == nil || errors.Is(err, ErrAppSpecificPasswordCreationUncertain) {
		t.Fatal("pre-cancelled request was not rejected before creation")
	}
	client.apiKey = ""
	if _, err := client.ListAppSpecificPasswords(context.Background()); !errors.Is(err, ErrAccountManagementAuthenticationRequired) {
		t.Fatal("missing portal authentication was not reported")
	}
	if _, err := client.CreateAppSpecificPassword(context.Background(), "mail", "fixture-password"); !errors.Is(err, ErrAccountManagementAuthenticationRequired) {
		t.Fatal("missing portal authentication was not reported")
	}
}
