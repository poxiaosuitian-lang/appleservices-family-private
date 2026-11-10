package icloud

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"howett.net/plist"
)

func skillRedirectTestClient(transport testStoreTransport) *http.Client {
	return &http.Client{Jar: mustCookieJar(), Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func TestSkillAuthenticationRedirectsPreserveFinalAttempt(t *testing.T) {
	t.Setenv("APPLESERVICES_SKILL_AUTH_ATTEMPTS", "1")
	calls := 0
	signer := &testStoreSigner{}
	var original []byte
	client := skillRedirectTestClient(func(req *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(req.Body)
		if calls == 1 {
			original = body
		}
		if req.Method != http.MethodPost || !bytes.Equal(body, original) || !bytes.Equal(body, signer.body) ||
			req.Header.Get("X-Apple-ActionSignature") != base64.StdEncoding.EncodeToString([]byte("signed")) {
			t.Fatal("redirect changed the signed POST body")
		}
		header := http.Header{}
		if calls <= 4 {
			header.Set("Location", "/WebObjects/MZFinance.woa/wa/authenticate/")
			return &http.Response{StatusCode: []int{301, 302, 307, 308}[calls-1], Header: header, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
		}
		header.Set("X-Set-Apple-Store-Front", "143441")
		return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(syntheticStoreSuccess)), Request: req}, nil
	})
	session, err := authenticateSkillStoreConfigured(context.Background(), client, storeAuthenticateURL, "TEST-GUID", "owner@example.test", "synthetic-password", skillStoreAgent, nil, signer, nil)
	if err != nil || !session.MatchesAccount("123") || calls != 5 {
		t.Fatalf("redirects exhausted the authentication budget: calls=%d, err=%v", calls, err)
	}
}

func TestSkillAuthenticationRejectsExcessRedirectsWithActualStatus(t *testing.T) {
	t.Setenv("APPLESERVICES_SKILL_AUTH_ATTEMPTS", "1")
	calls := 0
	client := skillRedirectTestClient(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 307, Header: http.Header{"Location": {"/auth/v1/native/fast/"}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	})
	_, err := authenticateSkillStoreClient(context.Background(), client, "TEST-GUID", "owner@example.test", "synthetic-password", nil)
	var failure *StorageError
	if calls != 5 || !errors.As(err, &failure) || failure.Stage != "purchase-store-redirect" || failure.HTTPStatus != 307 || storeRoutingFailure(err) {
		t.Fatalf("redirect limit or failure status lost: calls=%d, err=%v", calls, err)
	}
}

func TestSkillAuthenticationDoesNotReplayPOSTOnSeeOther(t *testing.T) {
	calls := 0
	client := skillRedirectTestClient(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusSeeOther, Header: http.Header{"Location": {"/auth/v1/native/fast/"}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	})
	_, err := authenticateSkillStoreClient(context.Background(), client, "TEST-GUID", "owner@example.test", "synthetic-password", nil)
	var failure *StorageError
	if calls != 1 || !errors.As(err, &failure) || failure.Stage != "purchase-store-redirect" || failure.HTTPStatus != http.StatusSeeOther || storeRoutingFailure(err) {
		t.Fatalf("303 replayed credentials or lost status: calls=%d, err=%v", calls, err)
	}
}

func TestSkillAuthenticationChallengeSurvivesFinalAttemptRedirect(t *testing.T) {
	t.Setenv("APPLESERVICES_SKILL_AUTH_ATTEMPTS", "1")
	calls, codes := 0, 0
	signer := &testStoreSigner{}
	var challengedBody []byte
	client := skillRedirectTestClient(func(req *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(req.Body)
		if req.Method != http.MethodPost || !bytes.Equal(body, signer.body) || req.Header.Get("X-Apple-ActionSignature") == "" {
			t.Fatal("challenge redirect lost signed POST")
		}
		header := http.Header{}
		if calls == 1 {
			header.Set("Set-Cookie", "challenge=synthetic; Path=/; Secure")
			return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(`<plist><dict><key>customerMessage</key><string>MZFinance.BadLogin.Configurator_message</string></dict></plist>`)), Request: req}, nil
		}
		cookie, err := req.Cookie("challenge")
		if err != nil || cookie.Value != "synthetic" {
			t.Fatal("challenge redirect lost its cookie session")
		}
		var values map[string]any
		if _, err := plist.Unmarshal(body, &values); err != nil || values["password"] != "synthetic-password123456" || values["attempt"] != "1" {
			t.Fatal("challenge credentials changed")
		}
		if calls == 2 {
			challengedBody = body
			header.Set("Location", "/WebObjects/MZFinance.woa/wa/authenticate/")
			return &http.Response{StatusCode: 302, Header: header, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
		}
		if !bytes.Equal(body, challengedBody) {
			t.Fatal("redirect changed the submitted code or XML layout")
		}
		header.Set("X-Set-Apple-Store-Front", "143441")
		return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(syntheticStoreSuccess)), Request: req}, nil
	})
	session, err := authenticateSkillStoreConfigured(context.Background(), client, storeAuthenticateURL, "TEST-GUID", "owner@example.test", "synthetic-password", skillStoreAgent, nil, signer, func() (string, error) {
		codes++
		return "123456", nil
	})
	if err != nil || !session.MatchesAccount("123") || calls != 3 || codes != 1 {
		t.Fatalf("redirect lost challenged final attempt: calls=%d, codes=%d, err=%v", calls, codes, err)
	}
}

func TestSkillAuthenticationBareRedirectStillUsesRoutingBudget(t *testing.T) {
	t.Setenv("APPLESERVICES_SKILL_AUTH_ATTEMPTS", "2")
	calls := 0
	client := skillRedirectTestClient(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 301, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	})
	_, err := authenticateSkillStoreClient(context.Background(), client, "TEST-GUID", "owner@example.test", "synthetic-password", nil)
	var failure *StorageError
	if calls != 2 || !errors.As(err, &failure) || failure.HTTPStatus != 301 || failure.Stage != "purchase-store-authentication" {
		t.Fatalf("bare redirects escaped their request budget: calls=%d, err=%v", calls, err)
	}
}
