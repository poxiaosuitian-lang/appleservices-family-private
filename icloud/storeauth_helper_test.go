package icloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func helperResultFixture() *storeAuthMessage {
	return &storeAuthMessage{Version: 1, Type: "result", OK: true, Email: "owner@example.test", DSID: "1234", Token: "synthetic-token", Storefront: "143441-1,29", GUID: "001122334455", State: []byte(`{"Identifier":"0123456789abcdef"}`)}
}

func TestPrivateStoreCheckpointExcludesPasswordCodeAndGiftState(t *testing.T) {
	r := helperResultFixture()
	r.Password, r.Code = "must-not-persist-password", "123456"
	s, err := importFrameworkSession(r, r.Email, r.DSID, r.GUID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := s.PrivateCheckpoint(r.Email, r.DSID)
	if err != nil {
		t.Fatal(err)
	}
	var decoded storeAuthMessage
	if json.Unmarshal(data, &decoded) != nil || decoded.Password != "" || decoded.Code != "" || decoded.Token != r.Token {
		t.Fatal("checkpoint included transient secrets or lost session token")
	}
	if _, err := s.PrivateCheckpoint("other@example.test", r.DSID); err == nil {
		t.Fatal("wrong-account checkpoint exported")
	}
	if _, err := s.PrivateCheckpoint(r.Email, "999"); err == nil {
		t.Fatal("wrong-DSID checkpoint exported")
	}
}

func TestFrameworkSessionBinding(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*storeAuthMessage)
	}{
		{"dsid", func(r *storeAuthMessage) { r.DSID = "4321" }},
		{"email", func(r *storeAuthMessage) { r.Email = "other@example.test" }},
		{"guid", func(r *storeAuthMessage) { r.GUID = "111122334455" }},
		{"token", func(r *storeAuthMessage) { r.Token += "\r\nheader: injected" }},
		{"storefront", func(r *storeAuthMessage) { r.Storefront = "invalid" }},
		{"pod", func(r *storeAuthMessage) { r.Pod = "other.example" }},
		{"state", func(r *storeAuthMessage) { r.State = make([]byte, maxHelperMessage) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := helperResultFixture()
			test.mutate(r)
			if s, err := importFrameworkSession(r, "owner@example.test", "1234", "001122334455"); err == nil || s != nil {
				t.Fatal("accepted unbound helper credentials")
			}
		})
	}
}

func TestFrameworkCookieImportPreservesScopeAndDeletion(t *testing.T) {
	r := helperResultFixture()
	r.Cookies = []storeAuthCookies{
		{URL: "https://auth.itunes.apple.com/auth/v1/native", Cookies: []*http.Cookie{
			{Name: "host", Value: "host-only", Secure: true, Path: "/"},
			{Name: "shared", Value: "old", Domain: ".itunes.apple.com", Secure: true, Path: "/subscription"},
			{Name: "deleted", Value: "old", Domain: ".itunes.apple.com", Path: "/"},
		}},
		{URL: "https://buy.itunes.apple.com/", Cookies: []*http.Cookie{
			{Name: "shared", Value: "new", Domain: ".itunes.apple.com", Secure: true, Path: "/subscription"},
			{Name: "deleted", Value: "", Domain: ".itunes.apple.com", Path: "/", MaxAge: -1},
		}},
	}
	s, err := importFrameworkSession(r, "OWNER@example.test", "1234", r.GUID)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ url, want string }{
		{"https://speedysub.itunes.apple.com/subscription/v3/manage/list", "shared=new"},
		{"https://speedysub.itunes.apple.com/other", ""},
		{"http://speedysub.itunes.apple.com/subscription/v3/manage/list", ""},
		{"https://auth.itunes.apple.com/auth/v1/native", "host=host-only"},
	} {
		u, _ := url.Parse(test.url)
		cookies := s.client.Jar.Cookies(u)
		got := ""
		if len(cookies) > 0 {
			got = cookies[0].String()
		}
		if got != test.want || len(cookies) > 1 {
			t.Fatalf("scope mismatch for %s", test.url)
		}
	}
	r.Cookies[0].URL = "https://foreign.example/"
	if _, err := importFrameworkSession(r, r.Email, r.DSID, r.GUID); err == nil {
		t.Fatal("accepted foreign origin")
	}
}

type helperRoundTrip func(*http.Request) (*http.Response, error)

func (f helperRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFrameworkSessionSubscriptionAndExpiry(t *testing.T) {
	r := helperResultFixture()
	s, err := importFrameworkSession(r, r.Email, r.DSID, r.GUID)
	if err != nil {
		t.Fatal(err)
	}
	status := 200
	s.client.Transport = helperRoundTrip(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("User-Agent") != frameworkStoreAgent || req.Header.Get("X-Dsid") != r.DSID || req.Header.Get("X-Token") != r.Token {
			t.Fatal("commerce fingerprint or credentials lost")
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"active":[]}`)), Request: req}, nil
	})
	if sub, err := s.ReadSkillSubscription(context.Background()); err != nil || sub.Present {
		t.Fatal("subscription check failed")
	}
	status = 503
	if _, err := s.ReadSkillSubscription(context.Background()); err == nil || !s.MatchesAccount(r.DSID) {
		t.Fatal("routing error incorrectly erased identity")
	}
	status = 401
	if _, err := s.ReadSkillSubscription(context.Background()); err == nil || s.MatchesAccount(r.DSID) {
		t.Fatal("expired identity still ready")
	}
	if !s.CanReuseFrameworkState(r.Email, r.DSID) || s.CanReuseFrameworkState("other@example.test", r.DSID) || s.CanReuseFrameworkState(r.Email, "4321") {
		t.Fatal("machine state binding invalid")
	}
	data, _ := json.Marshal(s)
	if string(data) != "{}" {
		t.Fatal("private session serialized")
	}
}

// Only this synthetic subprocess mode reads test fixtures. It never calls Apple.
func TestStoreAuthProtocolProcess(t *testing.T) {
	mode := os.Getenv("APPLESERVICES_TEST_HELPER_MODE")
	if mode == "" {
		return
	}
	var request storeAuthMessage
	if json.NewDecoder(os.Stdin).Decode(&request) != nil {
		os.Exit(3)
	}
	encoder := json.NewEncoder(os.Stdout)
	switch mode {
	case "challenge", "duplicate":
		_ = encoder.Encode(storeAuthMessage{Version: 1, Type: "challenge"})
		var code storeAuthMessage
		if json.NewDecoder(os.Stdin).Decode(&code) != nil || code.Code != "123456" || code.Type != "code" {
			os.Exit(4)
		}
		if mode == "duplicate" {
			_ = encoder.Encode(storeAuthMessage{Version: 1, Type: "challenge"})
			time.Sleep(5 * time.Second)
			os.Exit(5)
		}
	case "timeout":
		time.Sleep(10 * time.Second)
		os.Exit(6)
	case "failure":
		_ = encoder.Encode(storeAuthMessage{Version: 1, Type: "result", Stage: "RAW-SECRET-NOT-FOR-OUTPUT", HTTPStatus: 503})
		os.Exit(1)
	case "oversize":
		fmt.Fprintln(os.Stdout, strings.Repeat("x", maxHelperMessage+100))
		os.Exit(0)
	}
	_ = encoder.Encode(helperResultFixture())
	os.Exit(0)
}

func TestStoreAuthProtocolBoundedPrivateExchange(t *testing.T) {
	for _, mode := range []string{"success", "challenge", "duplicate", "timeout", "failure", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			limit := 5 * time.Second
			if mode == "timeout" {
				limit = 200 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), limit)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStoreAuthProtocolProcess$")
			command.Env = append(os.Environ(), "APPLESERVICES_TEST_HELPER_MODE="+mode)
			calls := 0
			result, err := exchangeStoreAuth(ctx, command, storeAuthMessage{Version: 1, Type: "login", Password: "synthetic-private-input"}, func() (string, error) { calls++; return "123456", nil })
			if mode == "success" || mode == "challenge" {
				if err != nil || result == nil || !result.OK {
					t.Fatalf("valid exchange failed: %v", err)
				}
			} else {
				if err == nil || result != nil {
					t.Fatal("invalid exchange accepted")
				}
				if strings.Contains(err.Error(), "SECRET") {
					t.Fatal("helper payload leaked")
				}
				var failure *StorageError
				if mode == "timeout" && (!errors.As(err, &failure) || failure.Stage != "purchase-store-timeout") {
					t.Fatal("deadline not reported")
				}
			}
			want := 0
			if mode == "challenge" || mode == "duplicate" {
				want = 1
			}
			if calls != want {
				t.Fatal("code requested without one challenge")
			}
		})
	}
}

func TestStoreAuthChildEnvironment(t *testing.T) {
	env := storeAuthEnvironment([]string{"PATH=keep", "https_proxy=secret-proxy", "LOCALAPPDATA=C:/old", "APPLESERVICES_PASSWORD=secret"}, "D:/test-cache")
	got := strings.Join(env, "\n")
	if strings.Contains(got, "secret") || strings.Contains(got, "C:/old") || !strings.Contains(got, "LOCALAPPDATA=D:/test-cache") || !strings.Contains(got, "PATH=keep") {
		t.Fatal("child inherited credentials or wrong cache")
	}
}

func TestStoreAuthDeadlineWhileWaitingForCode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStoreAuthProtocolProcess$")
	command.Env = append(os.Environ(), "APPLESERVICES_TEST_HELPER_MODE=challenge")
	finished := make(chan struct{})
	defer close(finished)
	_, err := exchangeStoreAuth(ctx, command, storeAuthMessage{Version: 1, Type: "login"}, func() (string, error) {
		<-finished
		return "123456", nil
	})
	var failure *StorageError
	if !errors.As(err, &failure) || failure.Stage != "purchase-store-timeout" {
		t.Fatal("code provider outlived authentication deadline")
	}
}
