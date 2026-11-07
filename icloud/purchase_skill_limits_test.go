package icloud

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFinalRoutingAttemptStillAllowsAppleRequestedChallenge(t *testing.T) {
	t.Setenv("APPLESERVICES_SKILL_AUTH_ATTEMPTS", "1")
	calls := 0
	client := &http.Client{Transport: testStoreTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		body := `<plist><dict><key>customerMessage</key><string>MZFinance.BadLogin.Configurator_message</string></dict></plist>`
		header := http.Header{}
		if calls == 2 {
			body = `<plist><dict><key>dsPersonId</key><string>123</string><key>passwordToken</key><string>synthetic-token</string></dict></plist>`
			header.Set("X-Set-Apple-Store-Front", "143441")
		}
		return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}
	session, err := authenticateSkillStoreClient(context.Background(), client, "TEST-GUID", "owner@example.test", "synthetic-password", func() (string, error) { return "123456", nil })
	if err != nil || calls != 2 || !session.MatchesAccount("123") {
		t.Fatalf("challenge consumed retry budget: %v", err)
	}
}

func TestStoreRetryAfterStopsAuthenticationAndFallback(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: testStoreTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 503, Header: http.Header{"Retry-After": []string{"120"}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	})}
	_, err := authenticateSkillStoreClient(context.Background(), client, "TEST-GUID", "owner@example.test", "synthetic-password", nil)
	var failure *StorageError
	if calls != 1 || !errors.As(err, &failure) || failure.Stage != "purchase-store-backoff" || storeRoutingFailure(err) {
		t.Fatalf("server backoff ignored: %v", err)
	}
}

func TestStoreCancellationDoesNotBecomeAuthenticationRejection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &http.Client{Transport: testStoreTransport(func(req *http.Request) (*http.Response, error) { return nil, req.Context().Err() })}
	_, err := authenticateSkillStoreClient(ctx, client, "TEST-GUID", "owner@example.test", "synthetic-password", nil)
	var failure *StorageError
	if !errors.As(err, &failure) || failure.Stage != "purchase-store-timeout" || storeRoutingFailure(err) {
		t.Fatalf("cancellation reported as Apple rejection: %v", err)
	}
}
