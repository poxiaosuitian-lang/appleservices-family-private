package appleservices

import (
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Laky-64/appleservices/icloud"
)

// Load a synthetic, locally bound session through the production decoder.
// No Apple request is sent and no real account credential is read.
func fixtureCommerceSession(t *testing.T) *icloud.StoreCommerceSession {
	t.Helper()
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "synthetic-store.json")
	for _, iface := range interfaces {
		if len(iface.HardwareAddr) != 6 {
			continue
		}
		body, _ := json.Marshal(map[string]any{
			"apple-id": "owner@example.test",
			"identity": map[string]any{"DSID": "test-dsid", "PasswordToken": "synthetic-token", "GUID": strings.ToUpper(hex.EncodeToString(iface.HardwareAddr))},
			"cookies":  map[string]any{},
		})
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
		if session, err := icloud.LoadPrivateSkillSession(path, "owner@example.test", "test-dsid"); err == nil {
			return session
		}
	}
	t.Skip("no usable physical Store identity on this test host")
	return nil
}

func TestStoreSessionSurvivesRepeatedSameAccountRenewal(t *testing.T) {
	commerce := fixtureCommerceSession(t)
	login := &Login{creds: Credentials{AppleID: "owner@example.test"}, client: &Client{DSID: "test-dsid", ADSID: "test-adsid"}, storeCommerce: commerce}
	for i := 0; i < 3; i++ {
		renewed := &Login{creds: Credentials{AppleID: "OWNER@example.test"}, client: &Client{DSID: "test-dsid", ADSID: "test-adsid"}}
		if !renewed.InheritStorePurchaseSession(login) || !renewed.StorePurchaseSessionReady() || renewed.storeCommerce != commerce {
			t.Fatal("renewal lost the original commerce client and cookie session")
		}
		login = renewed
	}
}

func TestStoreSessionCannotCrossAccountOrAuthenticationBoundary(t *testing.T) {
	previous := &Login{creds: Credentials{AppleID: "owner@example.test"}, client: &Client{DSID: "test-dsid", ADSID: "test-adsid"}, storeCommerce: fixtureCommerceSession(t)}
	for _, test := range []struct {
		name   string
		mutate func(*Login)
	}{
		{"apple-id", func(l *Login) { l.creds.AppleID = "other@example.test" }},
		{"missing-apple-id", func(l *Login) { l.creds.AppleID = " " }},
		{"dsid", func(l *Login) { l.client.DSID = "other" }},
		{"missing-dsid", func(l *Login) { l.client.DSID = "" }},
		{"adsid", func(l *Login) { l.client.ADSID = "other" }},
		{"missing-adsid", func(l *Login) { l.client.ADSID = "" }},
		{"challenge", func(l *Login) { l.needsTwoFactor = true }},
		{"not-authenticated", func(l *Login) { l.client = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			renewed := &Login{creds: previous.creds, client: &Client{DSID: "test-dsid", ADSID: "test-adsid"}}
			test.mutate(renewed)
			if renewed.InheritStorePurchaseSession(previous) || renewed.storeCommerce != nil {
				t.Fatal("unverified/different account inherited commerce credentials")
			}
		})
	}
	previous.needsTwoFactor = true
	renewed := &Login{creds: previous.creds, client: &Client{DSID: "test-dsid", ADSID: "test-adsid"}}
	if renewed.InheritStorePurchaseSession(previous) || renewed.InheritStorePurchaseSession(nil) {
		t.Fatal("invalid previous session inherited")
	}
}

func TestStoreSessionRenewalRejectsUnusablePreviousSession(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Login)
	}{
		{"challenge", func(l *Login) { l.needsTwoFactor = true }},
		{"not-authenticated", func(l *Login) { l.client = nil }},
		{"missing-commerce-session", func(l *Login) { l.storeCommerce = nil }},
		{"empty-commerce-session", func(l *Login) { l.storeCommerce = &icloud.StoreCommerceSession{} }},
		{"missing-apple-id", func(l *Login) { l.creds.AppleID = "" }},
		{"missing-dsid", func(l *Login) { l.client.DSID = "" }},
		{"missing-adsid", func(l *Login) { l.client.ADSID = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous := &Login{creds: Credentials{AppleID: "owner@example.test"}, client: &Client{DSID: "test-dsid", ADSID: "test-adsid"}, storeCommerce: fixtureCommerceSession(t)}
			renewed := &Login{creds: previous.creds, client: &Client{DSID: "test-dsid", ADSID: "test-adsid"}}
			test.mutate(previous)
			if renewed.InheritStorePurchaseSession(previous) || renewed.storeCommerce != nil {
				t.Fatal("unusable previous account inherited commerce credentials")
			}
		})
	}
	var empty *Login
	if empty.InheritStorePurchaseSession(nil) {
		t.Fatal("nil account reported a ready commerce session")
	}
}

func TestStoreSessionRenewalKeepsNewSession(t *testing.T) {
	previous := &Login{creds: Credentials{AppleID: "owner@example.test"}, client: &Client{DSID: "test-dsid", ADSID: "test-adsid"}, storeCommerce: fixtureCommerceSession(t)}
	currentSession := fixtureCommerceSession(t)
	renewed := &Login{creds: previous.creds, client: &Client{DSID: "test-dsid", ADSID: "test-adsid"}, storeCommerce: currentSession}
	if !renewed.InheritStorePurchaseSession(previous) || renewed.storeCommerce != currentSession {
		t.Fatal("renewal replaced a newly established commerce session")
	}
}
