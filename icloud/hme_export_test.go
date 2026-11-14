package icloud

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"
)

func TestHMEExportUsesOnlyAccountBoundWebCookies(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse("https://p1-maildomainws.icloud.com")
	jar.SetCookies(u, []*http.Cookie{{Name: "X-APPLE-WEBAUTH-TOKEN", Value: "synthetic"}, {Name: "X-APPLE-WEBAUTH-USER", Value: "synthetic-user"}, {Name: "unrelated", Value: "excluded"}})
	c := &HideMyEmailClient{appleID: "owner@example.test", jar: jar, session: hmeSession{AppleID: "owner@example.test", DSID: "fixture", ServiceURL: u.String()}}
	cookies, host, err := c.ExportServerCookies()
	if err != nil || host != "icloud.com" || len(cookies) != 2 {
		t.Fatal("invalid export", err)
	}
	c.session.AppleID = "other@example.test"
	if _, _, err = c.ExportServerCookies(); err == nil {
		t.Fatal("cross-account export")
	}
	c.session.AppleID = c.appleID
	c.session.ServiceURL = "https://attacker.example"
	if _, _, err = c.ExportServerCookies(); err == nil {
		t.Fatal("foreign service export")
	}
}
