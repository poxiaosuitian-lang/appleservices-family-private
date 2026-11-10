package icloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"path/filepath"
	"strings"
	"testing"
)

type mailTransport func(*http.Request) (*http.Response, error)

func (f mailTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func mailTestClient(t *testing.T, handler func(*http.Request) (int, string, error)) *PrimaryMailClient {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	w := &HideMyEmailClient{appleID: "alice@example.test", serviceName: "mccgateway", setupBase: "https://setup.icloud.com/setup/ws/1", sessionPath: filepath.Join(t.TempDir(), "session.json"), jar: jar,
		session: hmeSession{AppleID: "alice@example.test", DSID: "123", ClientID: "client", ServiceURL: "https://p01-mailgateway.icloud.com"}}
	w.http = &http.Client{Jar: jar, Transport: mailTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Query().Get("dsid") != "123" {
			t.Error("request lost method or account binding")
		}
		status, body, err := handler(r)
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	return &PrimaryMailClient{web: w}
}

func mailSetupResponse(exists bool) string {
	services := map[string]any{"mccgateway": map[string]string{"url": "https://p01-mailgateway.icloud.com"}}
	if exists {
		services["mail"] = map[string]string{"url": "https://p01-mailws.icloud.com"}
	}
	b, _ := json.Marshal(map[string]any{"dsInfo": map[string]string{"dsid": "123"}, "webservices": services})
	return string(b)
}

func TestPrimaryMailExistingNeverCreates(t *testing.T) {
	c := mailTestClient(t, func(r *http.Request) (int, string, error) {
		if r.URL.Path != "/setup/ws/1/validate" {
			t.Fatal("existing mailbox called a creation endpoint")
		}
		return 200, mailSetupResponse(true), nil
	})
	info, err := c.Ensure(context.Background(), true, func(string) error { t.Fatal("existing mailbox marked submitted"); return nil })
	if err != nil || !info.Exists {
		t.Fatalf("existing check failed: %v", err)
	}
}

func TestPrimaryMailLoginExplainsSecurityQuestionsWithoutLeakingResponse(t *testing.T) {
	err := primaryMailLoginError(412, []byte(`{"authType":"sa","private":"test-secret"}`))
	if !strings.Contains(err.Error(), "安全提示问题") || strings.Contains(err.Error(), "test-secret") {
		t.Fatal("security-question challenge was misclassified")
	}
	err = primaryMailLoginError(403, []byte(`{"serviceErrors":[{"code":"-123","message":"private account details"}]}`))
	if !strings.Contains(err.Error(), "-123") || strings.Contains(err.Error(), "private") {
		t.Fatal("diagnostic lost code or leaked raw response")
	}
}

func TestPrimaryMailCollisionAndVerifiedCreation(t *testing.T) {
	checks, creates, marked := 0, 0, false
	exists := false
	c := mailTestClient(t, func(r *http.Request) (int, string, error) {
		if r.URL.Path == "/setup/ws/1/validate" {
			return 200, mailSetupResponse(exists), nil
		}
		var body map[string]string
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Fatal("invalid body")
		}
		if r.URL.Path == "/mailacct/v1/web/emailAvailability" {
			checks++
			b, _ := json.Marshal(map[string]any{"email": body["email"], "available": checks > 1})
			return 200, string(b), nil
		}
		if r.URL.Path != "/mailacct/v1/web/activateEmail" || !marked {
			t.Fatal("creation before durable checkpoint")
		}
		creates++
		exists = true
		b, _ := json.Marshal(map[string]string{"email": body["email"]})
		return 200, string(b), nil
	})
	info, err := c.Ensure(context.Background(), true, func(email string) error {
		marked = true
		if email == "alice@icloud.com" {
			t.Fatal("collision not handled")
		}
		return nil
	})
	if err != nil || !info.Exists || info.Email == "" || checks != 2 || creates != 1 {
		t.Fatalf("creation not verified: %+v %v %d %d", info, err, checks, creates)
	}
}

func TestPrimaryMailUncertainSubmissionNeverReplays(t *testing.T) {
	for _, accepted := range []bool{true, false} {
		t.Run(map[bool]string{true: "applied-response-lost", false: "not-confirmed"}[accepted], func(t *testing.T) {
			creates := 0
			exists := false
			c := mailTestClient(t, func(r *http.Request) (int, string, error) {
				switch r.URL.Path {
				case "/setup/ws/1/validate":
					return 200, mailSetupResponse(exists), nil
				case "/mailacct/v1/web/emailAvailability":
					return 200, `{"email":"alice@icloud.com","available":true}`, nil
				default:
					creates++
					exists = accepted
					return 0, "", errors.New("connection closed")
				}
			})
			info, err := c.Ensure(context.Background(), true, func(string) error { return nil })
			if accepted && (err != nil || !info.Exists || info.Email != "") {
				t.Fatal("lost response not reconciled")
			}
			if !accepted && !errors.Is(err, ErrPrimaryMailUnconfirmed) {
				t.Fatal("uncertain create reported successful")
			}
			_, _ = c.Ensure(context.Background(), false, func(string) error { t.Fatal("replayed"); return nil })
			if creates != 1 {
				t.Fatal("activation replayed")
			}
		})
	}
}

func TestPrimaryMailRejectsUnknownOrWrongAccountState(t *testing.T) {
	for _, body := range []string{`{}`, `{"dsInfo":{"dsid":"999"},"webservices":{}}`, `{"dsInfo":{"dsid":"123"}}`, `{"dsInfo":{"dsid":"123"},"webservices":{"mccgateway":{"url":"https://evil.example"}}}`, `{"dsInfo":{"dsid":"123"},"webservices":{},"termsUpdateNeeded":true}`} {
		c := mailTestClient(t, func(r *http.Request) (int, string, error) {
			if r.URL.Path != "/setup/ws/1/validate" {
				t.Fatal("invalid state triggered mutation")
			}
			return 200, body, nil
		})
		if _, err := c.Ensure(context.Background(), true, func(string) error { return nil }); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}

func TestPrimaryMailAvailabilityMustBeExplicitAndBound(t *testing.T) {
	for _, body := range []string{`{}`, `{"email":"alice@icloud.com"}`, `{"email":"other@icloud.com","available":true}`, `{"errorCode":"BLOCKED","email":"alice@icloud.com","available":true}`} {
		c := mailTestClient(t, func(r *http.Request) (int, string, error) {
			if r.URL.Path == "/setup/ws/1/validate" {
				return 200, mailSetupResponse(false), nil
			}
			if r.URL.Path != "/mailacct/v1/web/emailAvailability" {
				t.Fatal("invalid availability triggered creation")
			}
			return 200, body, nil
		})
		if _, err := c.Ensure(context.Background(), true, func(string) error { return nil }); err == nil {
			t.Fatal("missing availability accepted")
		}
	}
}

func TestPrimaryMailCheckpointFailurePreventsSubmission(t *testing.T) {
	c := mailTestClient(t, func(r *http.Request) (int, string, error) {
		if r.URL.Path == "/setup/ws/1/validate" {
			return 200, mailSetupResponse(false), nil
		}
		if r.URL.Path != "/mailacct/v1/web/emailAvailability" {
			t.Fatal("submitted despite failed checkpoint")
		}
		return 200, `{"email":"alice@icloud.com","available":true}`, nil
	})
	if _, err := c.Ensure(context.Background(), true, func(string) error { return errors.New("disk full") }); err == nil {
		t.Fatal("ignored checkpoint failure")
	}
}
