package icloud

import (
	"net/http"
	"net/url"
	"testing"
)

func TestWebSessionRefreshReplacesPersistedHostCopies(t *testing.T) {
	for _, domain := range []string{"icloud.com", "icloud.com.cn"} {
		t.Run(domain, func(t *testing.T) {
			dir := t.TempDir()
			client, err := NewHideMyEmailClient(dir, "alice@example.test")
			if err != nil {
				t.Fatal(err)
			}
			setup, _ := url.Parse("https://setup." + domain + "/setup/ws/1/validate")
			gateway, _ := url.Parse("https://p01-mccgateway." + domain + "/mailacct/v1/web/emailAvailability")
			client.setupBase = setup.String()
			client.session = hmeSession{DSID: "123", ClientID: "client", ServiceURL: "https://" + gateway.Host}
			client.jar.SetCookies(setup, []*http.Cookie{{Name: "X-APPLE-WEBAUTH-TOKEN", Value: "old", Domain: "." + domain, Path: "/", Secure: true}})
			client.jar.SetCookies(gateway, []*http.Cookie{{Name: "host-only", Value: "keep", Path: "/", Secure: true}})
			if err := client.saveSession(); err != nil {
				t.Fatal(err)
			}
			// Repeated restarts must not reintroduce stale tokens after validate.
			for _, token := range []string{"rotated-once", "rotated-twice"} {
				client, err = NewHideMyEmailClient(dir, "alice@example.test")
				if err != nil {
					t.Fatal(err)
				}
				client.setupBase = setup.String()
				client.jar.SetCookies(setup, []*http.Cookie{{Name: "X-APPLE-WEBAUTH-TOKEN", Value: token, Domain: "." + domain, Path: "/", Secure: true}})
				for _, endpoint := range []*url.URL{setup, gateway} {
					count := 0
					for _, c := range client.jar.Cookies(endpoint) {
						if c.Name == "X-APPLE-WEBAUTH-TOKEN" {
							count++
							if c.Value != token {
								t.Fatal("stale token survived refresh")
							}
						}
					}
					if count != 1 {
						t.Fatalf("expected one refreshed token, got %d", count)
					}
				}
				kept := false
				for _, c := range client.jar.Cookies(gateway) {
					if c.Name == "host-only" && c.Value == "keep" {
						kept = true
					}
				}
				if !kept {
					t.Fatal("unrelated host cookie removed")
				}
				if err := client.saveSession(); err != nil {
					t.Fatal(err)
				}
			}
			client.jar.SetCookies(setup, []*http.Cookie{{Name: "X-APPLE-WEBAUTH-TOKEN", Domain: "." + domain, Path: "/", MaxAge: -1}})
			for _, c := range client.jar.Cookies(gateway) {
				if c.Name == "X-APPLE-WEBAUTH-TOKEN" {
					t.Fatal("deleted token survived")
				}
			}
		})
	}
}

func TestWebSessionRejectsUnrelatedCookieRotation(t *testing.T) {
	client, err := NewHideMyEmailClient(t.TempDir(), "alice@example.test")
	if err != nil {
		t.Fatal(err)
	}
	jar := client.jar.(*webSessionJar)
	scope, _ := url.Parse("https://p01-mccgateway.icloud.com")
	jar.restore(scope, hmeCookie{Scope: scope.String(), Name: "session", Value: "keep", Path: "/"})
	foreign, _ := url.Parse("https://icloud.com.example.test")
	jar.SetCookies(foreign, []*http.Cookie{{Name: "session", Value: "foreign", Domain: ".icloud.com", Path: "/"}})
	jar.SetCookies(scope, []*http.Cookie{{Name: "session", Value: "different-path", Domain: ".icloud.com", Path: "/other"}})
	cookies := jar.Cookies(scope)
	if len(cookies) != 1 || cookies[0].Value != "keep" {
		t.Fatal("unrelated response replaced the restored cookie")
	}
}
