package icloud

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestStoreTransportFallbackOnlyBeforeApplicationResponse(t *testing.T) {
	for _, test := range []struct {
		name     string
		failure  error
		fallback bool
	}{
		{"success", nil, false},
		{"routing-empty", &StorageError{Stage: "purchase-store-authentication", HTTPStatus: 204}, true},
		{"routing-unavailable", &StorageError{Stage: "purchase-store-authentication", HTTPStatus: 503}, true},
		{"account-rejection", &StorageError{Stage: "purchase-store-authentication", HTTPStatus: 200, AppleCode: "-5000"}, false},
		{"rate-limit", &StorageError{Stage: "purchase-store-rate-limited", HTTPStatus: 429}, false},
		{"server-backoff", &StorageError{Stage: "purchase-store-backoff", HTTPStatus: 503}, false},
		{"challenge-started", &StorageError{Stage: "purchase-store-authentication", HTTPStatus: 503, Hint: "after-store-challenge"}, false},
		{"bad-code", &StorageError{Stage: "purchase-store-code-unverified", HTTPStatus: 200}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if storeRoutingFailure(test.failure) != test.fallback {
				t.Fatal("unexpected transport fallback decision")
			}
		})
	}
}

func TestChallengedStoreFailureCannotStartAnotherTransportSession(t *testing.T) {
	t.Setenv("APPLESERVICES_SKILL_AUTH_ATTEMPTS", "2")
	calls, codes := 0, 0
	client := &http.Client{Jar: mustCookieJar(), Transport: testStoreTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		status, body := 200, `<plist><dict><key>customerMessage</key><string>MZFinance.BadLogin.Configurator_message</string></dict></plist>`
		header := http.Header{"Set-Cookie": {"challenge=synthetic; Path=/; Secure"}}
		if calls == 2 {
			cookie, err := req.Cookie("challenge")
			if err != nil || cookie.Value != "synthetic" {
				t.Fatal("verification request lost the challenged cookie session")
			}
			status, body = 503, ""
		}
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}
	_, err := authenticateSkillStoreClient(context.Background(), client, "TEST-GUID", "owner@example.test", "synthetic-password", func() (string, error) {
		codes++
		return "123456", nil
	})
	var failure *StorageError
	if calls != 2 || codes != 1 || !errors.As(err, &failure) || failure.HTTPStatus != 503 || storeRoutingFailure(err) {
		t.Fatal("a routing failure after a challenge can reset the verified session")
	}
}
