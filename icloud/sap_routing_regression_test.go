package icloud

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

const syntheticStoreSuccess = `<plist><dict><key>dsPersonId</key><string>123</string><key>passwordToken</key><string>synthetic-token</string></dict></plist>`

func TestNativeRedirectSignsLegacyDestinationAndKeepsSession(t *testing.T) {
	signer := &testStoreSigner{}
	calls := 0
	var firstBody string
	client := &http.Client{Jar: mustCookieJar(), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: testStoreTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(req.Body)
		header := http.Header{}
		if calls == 1 {
			firstBody = string(body)
			header.Set("Location", "/WebObjects/MZFinance.woa/wa/authenticate/")
			header.Set("Set-Cookie", "session=synthetic; Path=/; Secure")
			return &http.Response{StatusCode: 302, Header: header, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
		}
		cookie, err := req.Cookie("session")
		if calls != 2 || req.Method != http.MethodPost || string(body) != firstBody || string(body) != string(signer.body) || req.Header.Get("X-Apple-ActionSignature") == "" || req.Header.Get("User-Agent") != storeSAPUserAgent || err != nil || cookie.Value != "synthetic" {
			t.Fatal("redirect lost its signed POST profile or cookie session")
		}
		header.Set("X-Set-Apple-Store-Front", "143441")
		return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(syntheticStoreSuccess)), Request: req}, nil
	})}
	session, err := authenticateSignedStoreBounded(context.Background(), client, "https://auth.itunes.apple.com/auth/v1/native/fast/", "owner@example.test", "synthetic-password", "", "TEST-GUID", nil, signer, 0)
	if err != nil || session == nil || calls != 2 {
		t.Fatalf("redirect authentication failed: %v", err)
	}
}

func TestLegacyStoreBackoffNeverReplaysAuthentication(t *testing.T) {
	for _, retryAfter := range []string{"120", "Wed, 01 Jan 2031 00:00:00 GMT", "invalid"} {
		t.Run(retryAfter, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: testStoreTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 503, Header: http.Header{"Retry-After": {retryAfter}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
			})}
			_, err := authenticateSignedStoreBounded(context.Background(), client, "https://auth.itunes.apple.com/auth/v1/native/fast/", "owner@example.test", "synthetic-password", "", "TEST-GUID", nil, nil, storeAuthAttempts)
			var failure *StorageError
			if calls != 1 || !errors.As(err, &failure) || failure.Stage != "purchase-store-backoff" {
				t.Fatalf("server backoff ignored: calls=%d, err=%v", calls, err)
			}
		})
	}
}

func TestRegularStoreEntryRetriesBareRedirectWithinBudget(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: testStoreTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		status, body := 301, ""
		header := http.Header{}
		if calls == 2 {
			status, body = 200, syntheticStoreSuccess
			header.Set("X-Set-Apple-Store-Front", "143441")
		}
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}
	session, err := authenticateSignedStoreBounded(context.Background(), client, "https://auth.itunes.apple.com/auth/v1/native/fast/", "owner@example.test", "synthetic-password", "", "TEST-GUID", nil, nil, storeAuthAttempts)
	if err != nil || session == nil || calls != 2 {
		t.Fatalf("regular entry cannot recover from a bare redirect: %v", err)
	}
}
