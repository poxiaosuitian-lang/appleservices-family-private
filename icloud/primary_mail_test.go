package icloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestPrimaryMailCandidateNamesRespectAppleLimit(t *testing.T) {
	valid := regexp.MustCompile(`^[a-z][a-z0-9]{2,19}@icloud\.com$`)
	for _, id := range []string{"abcdefghijklmnopqrst@example.test", "abcdefghijklmnopqrstuvwxyz@example.test", "a@example.test", "123@example.test", "用户@example.test", "alice@example.test"} {
		seen := map[string]bool{}
		for _, email := range primaryMailCandidates(id) {
			if !valid.MatchString(email) || seen[email] {
				t.Fatalf("invalid or duplicate candidate for %q: %q", id, email)
			}
			seen[email] = true
		}
		if len(seen) != 4 {
			t.Fatal("missing fallback candidates")
		}
	}
}

func TestPrimaryMailServiceFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		name, path, body, want string
		status                 int
		auth                   bool
	}{
		{"activation-rejected", "/mailacct/v1/web/activateEmail", `{"errorCode":"-1234","errorMessage":"private-secret"}`, "邮箱创建（HTTP 403，代码 -1234）", 403, false},
		{"session-expired", "/mailacct/v1/web/activateEmail", `{"errorCode":"-9008"}`, "邮箱创建未完成", 403, true},
		{"numeric-session-expired", "/mailacct/v1/web/activateEmail", `{"errorCode":-9008}`, "邮箱创建未完成", 403, true},
		{"numeric-rejection", "/mailacct/v1/web/emailAvailability", `{"errorCode":-9007}`, "邮箱名称检查（HTTP 200，代码 -9007）", 200, false},
		{"malformed-error", "/mailacct/v1/web/emailAvailability", `{"errorCode":{"private":"private-secret"},"available":true}`, "邮箱名称检查（HTTP 200）", 200, false},
		{"session-unauthorized", "/setup/ws/1/validate", `{}`, "邮箱状态查询未完成", 401, true},
		{"too-long", "/mailacct/v1/web/emailAvailability", `{"errorCode":"MAX_LENGTH_VIOLATION"}`, "3–20", 400, false},
		{"unknown-code", "/mailacct/v1/web/activateEmail", `{"errorCode":"private-secret"}`, "邮箱创建（HTTP 200）", 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := mailTestClient(t, func(*http.Request) (int, string, error) { return tc.status, tc.body, nil })
			var out map[string]any
			err := c.request(context.Background(), "https://p01-mailgateway.icloud.com"+tc.path, map[string]any{}, &out)
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "private-secret") || errors.Is(err, ErrPrimaryMailAuthentication) != tc.auth {
				t.Fatalf("wrong classification or unsafe diagnostic: %v", err)
			}
		})
	}
}

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
	for _, body := range []string{`{}`, `{"dsInfo":{"dsid":"999"},"webservices":{}}`, `{"dsInfo":{"dsid":"123"}}`, `{"dsInfo":{"dsid":"123"},"webservices":{"mccgateway":{"url":"https://evil.example"}}}`, `{"dsInfo":{"dsid":"123"},"webservices":{"mail":{"url":"invalid"}}}`, `{"dsInfo":{"dsid":"123"},"webservices":{},"termsUpdateNeeded":true}`} {
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
	for _, body := range []string{`{}`, `{"email":"alice@icloud.com"}`, `{"email":"other@icloud.com","available":true}`, `{"errorCode":"BLOCKED","email":"alice@icloud.com","available":true}`, `{"errorCode":-9007,"email":"alice@icloud.com","available":true}`, `{"errorCode":{},"email":"alice@icloud.com","available":true}`} {
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

func TestPrimaryMailChinaServiceUsesMatchingOrigin(t *testing.T) {
	c := mailTestClient(t, func(r *http.Request) (int, string, error) {
		if !strings.HasSuffix(r.URL.Hostname(), ".icloud.com.cn") || r.Header.Get("Origin") != "https://www.icloud.com.cn" || r.Header.Get("Referer") != "https://www.icloud.com.cn/" {
			t.Fatal("China mailbox request used the wrong web origin")
		}
		if r.URL.Path == "/setup/ws/1/validate" {
			return 200, strings.ReplaceAll(mailSetupResponse(false), ".icloud.com", ".icloud.com.cn"), nil
		}
		return 200, `{"email":"alice@icloud.com","available":true}`, nil
	})
	c.web.session.AccountCountry = "CHN"
	if _, err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	var availability any
	if err := c.request(context.Background(), c.web.session.ServiceURL+"/mailacct/v1/web/emailAvailability", map[string]string{"email": "alice@icloud.com"}, &availability); err != nil {
		t.Fatal(err)
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
