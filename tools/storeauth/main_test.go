package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/majd/ipatool/v2/pkg/appstore"
	storehttp "github.com/majd/ipatool/v2/pkg/http"
)

type fakeLogin func(appstore.LoginInput) (appstore.LoginOutput, error)

func (f fakeLogin) Login(input appstore.LoginInput) (appstore.LoginOutput, error) { return f(input) }

func testRequest() loginRequest {
	return loginRequest{Version: 1, Type: "login", Email: "owner@example.test", Password: "private-password", MACAddress: "02:11:22:33:44:55", ExpectedDSID: "12345"}
}

func testAccount() appstore.Account {
	return appstore.Account{Email: "OWNER@example.test", DirectoryServicesID: "12345", PasswordToken: "private-token", StoreFront: "143441-1,29", Pod: "25", Password: "never-export-password"}
}

func encodeLines(values ...any) string {
	var out bytes.Buffer
	for _, value := range values {
		_ = json.NewEncoder(&out).Encode(value)
	}
	return out.String()
}

func runFake(t *testing.T, input string, factory loginFactory) (int, string, []loginResult) {
	t.Helper()
	var out bytes.Buffer
	status := runLogin(newWire(strings.NewReader(input), &out), factory)
	decoder := json.NewDecoder(strings.NewReader(out.String()))
	var results []loginResult
	for {
		var result loginResult
		if err := decoder.Decode(&result); err != nil {
			if err != io.EOF {
				t.Fatal(err)
			}
			break
		}
		results = append(results, result)
	}
	return status, out.String(), results
}

func TestLoginRequestsCodeOnlyAfterChallenge(t *testing.T) {
	calls := 0
	input := encodeLines(testRequest(), codeRequest{Version: 1, Type: "code", Code: "123 456"})
	status, output, results := runFake(t, input, func(*memoryKeys, *trackingJar, fixedMachine) loginClient {
		return fakeLogin(func(input appstore.LoginInput) (appstore.LoginOutput, error) {
			calls++
			if calls == 1 {
				if input.AuthCode != "" {
					t.Fatal("code supplied before challenge")
				}
				return appstore.LoginOutput{}, appstore.ErrAuthCodeRequired
			}
			if input.AuthCode != "123456" {
				t.Fatal("challenge code not used")
			}
			return appstore.LoginOutput{Account: testAccount()}, nil
		})
	})
	if status != 0 || calls != 2 || len(results) != 2 || results[0].Type != "challenge" || !results[1].OK || results[1].GUID != "021122334455" {
		t.Fatalf("unexpected handshake: calls=%d status=%d", calls, status)
	}
	if strings.Contains(output, "never-export-password") || strings.Contains(output, testRequest().Password) {
		t.Fatal("password exported")
	}
}

func TestLoginDoesNotReadCodeWithoutChallenge(t *testing.T) {
	status, _, results := runFake(t, encodeLines(testRequest()), func(*memoryKeys, *trackingJar, fixedMachine) loginClient {
		return fakeLogin(func(input appstore.LoginInput) (appstore.LoginOutput, error) {
			if input.AuthCode != "" {
				t.Fatal("unexpected code")
			}
			return appstore.LoginOutput{Account: testAccount()}, nil
		})
	})
	if status != 0 || len(results) != 1 || !results[0].OK {
		t.Fatal("successful login was not returned")
	}
}

func TestRepeatedChallengeStops(t *testing.T) {
	calls := 0
	status, _, results := runFake(t, encodeLines(testRequest(), codeRequest{1, "code", "123456"}), func(*memoryKeys, *trackingJar, fixedMachine) loginClient {
		return fakeLogin(func(appstore.LoginInput) (appstore.LoginOutput, error) {
			calls++
			return appstore.LoginOutput{}, appstore.ErrAuthCodeRequired
		})
	})
	if status != 1 || calls != 2 || len(results) != 2 || results[1].Stage != stageCodeInvalid {
		t.Fatal("challenge retry was not bounded")
	}
}

func TestAccountBindingAndErrorRedaction(t *testing.T) {
	for _, test := range []struct {
		name    string
		account appstore.Account
		err     error
		stage   string
		http    int
	}{
		{"different dsid", func() appstore.Account { a := testAccount(); a.DirectoryServicesID = "99999"; return a }(), nil, stageAccountMismatch, 0},
		{"different email", func() appstore.Account { a := testAccount(); a.Email = "other@example.test"; return a }(), nil, stageAccountMismatch, 0},
		{"missing token", func() appstore.Account { a := testAccount(); a.PasswordToken = ""; return a }(), nil, stageAuthentication, 0},
		{"header injection", func() appstore.Account { a := testAccount(); a.PasswordToken = "unsafe\r\nvalue"; return a }(), nil, stageAuthentication, 0},
		{"response body", appstore.Account{}, appstore.NewErrorWithMetadata(&storehttp.UnexpectedResponseError{StatusCode: 503, Snippet: "secret-response", CorrelationID: "secret-id"}, map[string]any{"password": "secret-password"}), stageAuthentication, 503},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, output, results := runFake(t, encodeLines(testRequest()), func(*memoryKeys, *trackingJar, fixedMachine) loginClient {
				return fakeLogin(func(appstore.LoginInput) (appstore.LoginOutput, error) {
					return appstore.LoginOutput{Account: test.account}, test.err
				})
			})
			if status != 1 || len(results) != 1 || results[0].Stage != test.stage || results[0].HTTPStatus != test.http || results[0].Token != "" || results[0].DSID != "" {
				t.Fatal("unsafe failed result")
			}
			if strings.Contains(output, "secret-") || strings.Contains(output, "private-") {
				t.Fatal("failure leaked upstream data")
			}
		})
	}
}

func TestInvalidRequestsNeverStartAuthentication(t *testing.T) {
	for _, input := range []string{
		`{"version":1,"type":"login","unknown":"secret"}` + "\n",
		encodeLines(func() loginRequest { r := testRequest(); r.ExpectedDSID = ""; return r }()),
		encodeLines(func() loginRequest { r := testRequest(); r.MACAddress = "01:11:22:33:44:55"; return r }()),
		encodeLines(func() loginRequest { r := testRequest(); r.State = []byte(`{"account":"secret"}`); return r }()),
		strings.Repeat("x", maxWireBytes+1) + "\n",
	} {
		status, _, results := runFake(t, input, func(*memoryKeys, *trackingJar, fixedMachine) loginClient {
			t.Fatal("invalid input reached authentication")
			return nil
		})
		if status != 1 || len(results) != 1 || results[0].OK {
			t.Fatal("invalid input accepted")
		}
	}
}

func TestCookieEventsPreserveScopeAndDeletion(t *testing.T) {
	jar, _ := newTrackingJar()
	origin, _ := url.Parse("https://auth.itunes.apple.com/auth/v1/native/fast/?guid=private-guid")
	cookie := &http.Cookie{Name: "session", Value: "private-cookie", Domain: ".itunes.apple.com", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(time.Hour).UTC().Truncate(time.Second)}
	jar.SetCookies(origin, []*http.Cookie{cookie})
	cookie.Value = "mutated"
	state := jar.snapshot()
	if jar.failure() != nil || len(state) != 1 || strings.Contains(state[0].URL, "guid") || state[0].Cookies[0].Value != "private-cookie" || !state[0].Cookies[0].Secure || !state[0].Cookies[0].HttpOnly || state[0].Cookies[0].Path != "/" || state[0].Cookies[0].Domain != ".itunes.apple.com" || state[0].Cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatal("cookie attributes not preserved")
	}
	consumer, _ := cookiejar.New(nil)
	for _, event := range state {
		u, _ := url.Parse(event.URL)
		consumer.SetCookies(u, event.Cookies)
	}
	pod, _ := url.Parse("https://p25-buy.itunes.apple.com/WebObjects/MZFinance.woa/wa/buyProduct")
	if got := consumer.Cookies(pod); len(got) != 1 || got[0].Value != "private-cookie" {
		t.Fatal("cookie did not reach commerce pod")
	}
	jar.SetCookies(origin, []*http.Cookie{{Name: "session", Domain: ".itunes.apple.com", Path: "/", MaxAge: -1}})
	state = jar.snapshot()
	consumer, _ = cookiejar.New(nil)
	for _, event := range state {
		u, _ := url.Parse(event.URL)
		consumer.SetCookies(u, event.Cookies)
	}
	if len(consumer.Cookies(pod)) != 0 {
		t.Fatal("cookie deletion lost")
	}
}

func TestCookieRulesAndLimits(t *testing.T) {
	for _, test := range []struct {
		origin string
		cookie *http.Cookie
	}{
		{"https://evil.example/", &http.Cookie{Name: "session", Value: "x"}},
		{"http://auth.itunes.apple.com/", &http.Cookie{Name: "session", Value: "x"}},
		{"https://auth.itunes.apple.com/", &http.Cookie{Name: "session", Value: "x", Domain: "invalid/domain"}},
		{"https://auth.itunes.apple.com/", nil},
	} {
		jar, _ := newTrackingJar()
		u, _ := url.Parse(test.origin)
		jar.SetCookies(u, []*http.Cookie{test.cookie})
		if jar.failure() == nil || len(jar.snapshot()) != 0 {
			t.Fatal("unsafe cookie accepted")
		}
	}
	jar, _ := newTrackingJar()
	u, _ := url.Parse("https://buy.itunes.apple.com/")
	for i := 0; i <= maxCookies; i++ {
		jar.SetCookies(u, []*http.Cookie{{Name: "session", Value: "x"}})
	}
	if !errors.Is(jar.Save(), errCookies) || len(jar.snapshot()) != maxCookies {
		t.Fatal("cookie count not bounded")
	}
	gsa, _ := url.Parse("https://gsa.apple.com/grandslam/MidService/startMachineProvisioning")
	jar, _ = newTrackingJar()
	jar.SetCookies(gsa, []*http.Cookie{{Name: "machine", Value: "private-machine"}})
	if jar.failure() != nil || len(jar.Cookies(gsa)) != 1 || len(jar.snapshot()) != 0 {
		t.Fatal("GSA cookies escaped into exported Store session")
	}
}

func TestUnrelatedCookieDomainsAreIgnored(t *testing.T) {
	origin, _ := url.Parse("https://auth.itunes.apple.com/auth/v1/native/fast/")
	for _, domain := range []string{".volume.itunes.apple.com", "evil.example", ".com"} {
		jar, _ := newTrackingJar()
		jar.SetCookies(origin, []*http.Cookie{{Name: "delete", Domain: domain, MaxAge: -1}})
		if jar.failure() != nil || len(jar.snapshot()) != 0 || len(jar.Cookies(origin)) != 0 {
			t.Fatal("out-of-scope deletion cookie was accepted or stopped login")
		}
	}
}

func TestSiblingDomainCleanupDoesNotAbortAuthentication(t *testing.T) {
	status, _, results := runFake(t, encodeLines(testRequest()), func(_ *memoryKeys, jar *trackingJar, _ fixedMachine) loginClient {
		return fakeLogin(func(appstore.LoginInput) (appstore.LoginOutput, error) {
			origin, _ := url.Parse("https://auth.itunes.apple.com/auth/v1/native/fast/")
			jar.SetCookies(origin, []*http.Cookie{
				{Name: "delete", Domain: ".volume.itunes.apple.com", Path: "/", MaxAge: -1},
				{Name: "session", Value: "private-cookie", Domain: ".itunes.apple.com", Path: "/", Secure: true},
			})
			if err := jar.Save(); err != nil {
				return appstore.LoginOutput{}, err
			}
			return appstore.LoginOutput{Account: testAccount()}, nil
		})
	})
	if status != 0 || len(results) != 1 || !results[0].OK || len(results[0].Cookies) != 1 || len(results[0].Cookies[0].Cookies) != 1 {
		t.Fatal("sibling cleanup aborted successful login")
	}
	if results[0].Cookies[0].Cookies[0].Name != "session" {
		t.Fatal("out-of-scope cookie was exported")
	}
}

func TestProvisioningCookiesStayInsideHelper(t *testing.T) {
	for _, host := range []string{"init.itunes.apple.com", "gsa.apple.com", "gsa-ssl.apple.com", "gsas.apple.com"} {
		jar, _ := newTrackingJar()
		origin, _ := url.Parse("https://" + host + "/")
		jar.SetCookies(origin, []*http.Cookie{{Name: "bootstrap", Value: "private-cookie", Path: "/", Secure: true}})
		if jar.failure() != nil || len(jar.Cookies(origin)) != 1 || len(jar.snapshot()) != 0 {
			t.Fatal("valid bootstrap cookie was rejected or exported")
		}
	}
}

func TestBoundedStorefrontForms(t *testing.T) {
	request := testRequest()
	machine, _ := validateLogin(request)
	for _, storefront := range []string{"143441", "143441-1", "143441,29", "143441-1,29", "143441-1,29,32"} {
		account := testAccount()
		account.StoreFront = storefront
		if _, stage := validatedResult(request, machine, account); stage != "" {
			t.Fatal("valid storefront shape rejected")
		}
	}
	for _, storefront := range []string{"14344", "143441x", "143441-", "143441-123456789", "143441\r\n", "143441" + strings.Repeat(",1", 9)} {
		account := testAccount()
		account.StoreFront = storefront
		if _, stage := validatedResult(request, machine, account); stage != stageAuthentication {
			t.Fatal("malformed or unbounded storefront accepted")
		}
	}
}

func TestCookieMaxAgeDoesNotRestartOnImport(t *testing.T) {
	jar, _ := newTrackingJar()
	origin, _ := url.Parse("https://auth.itunes.apple.com/auth/v1/native/fast/")
	before := time.Now()
	jar.SetCookies(origin, []*http.Cookie{{Name: "short-lived", Value: "private-cookie", Path: "/", MaxAge: 30, Expires: before.Add(time.Hour)}})
	after := time.Now()
	state := jar.snapshot()
	if jar.failure() != nil || len(state) != 1 {
		t.Fatal("cookie not captured")
	}
	cookie := state[0].Cookies[0]
	if cookie.MaxAge != 0 || cookie.Expires.Before(before.Add(30*time.Second)) || cookie.Expires.After(after.Add(30*time.Second)) {
		t.Fatal("relative lifetime not converted at receipt")
	}
	if len(jar.Cookies(origin)) != 1 {
		t.Fatal("original cookie missing from authentication jar")
	}
	// A delayed consumer must retain the old absolute deadline rather than
	// starting another thirty seconds when it imports the cookie.
	expired := *cookie
	expired.Expires = before.Add(-time.Second)
	consumer, _ := cookiejar.New(nil)
	consumer.SetCookies(origin, []*http.Cookie{&expired})
	if len(consumer.Cookies(origin)) != 0 {
		t.Fatal("expired exported cookie was revived")
	}
}

func TestMemoryStateIsCopiedAndOnlyAnisetteExported(t *testing.T) {
	state, _ := json.Marshal(struct {
		Identifier, Provisioning []byte
		RoutingInfo              uint64
	}{bytes.Repeat([]byte{1}, 16), []byte("provisioning"), 1})
	keys := newMemoryKeys()
	if err := keys.Set(anisetteKey, state); err != nil {
		t.Fatal(err)
	}
	state[0] = 'X'
	copy, err := keys.Get(anisetteKey)
	if err != nil || !validState(copy) {
		t.Fatal("input not copied")
	}
	copy[0] = 'X'
	copy, _ = keys.Get(anisetteKey)
	if !validState(copy) {
		t.Fatal("output not copied")
	}
	if keys.Set("unexpected", []byte("secret")) == nil {
		t.Fatal("unknown key accepted")
	}
	keys.clear()
	if _, err := keys.Get(anisetteKey); !errors.Is(err, errKeyNotFound) {
		t.Fatal("key not removed")
	}
}

func TestOversizedResultProducesOnlySafeFailure(t *testing.T) {
	var output bytes.Buffer
	w := newWire(strings.NewReader(""), &output)
	if w.result(loginResult{OK: true, Token: strings.Repeat("x", maxWireBytes)}) == nil {
		t.Fatal("oversized result accepted")
	}
	var result loginResult
	if json.Unmarshal(bytes.TrimSpace(output.Bytes()), &result) != nil || result.OK || result.Stage != stageProtocol || result.Token != "" {
		t.Fatal("unsafe oversized response")
	}
}
