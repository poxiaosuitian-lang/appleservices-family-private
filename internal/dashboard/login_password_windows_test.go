//go:build windows

package dashboard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	appleservices "github.com/Laky-64/appleservices"
)

func saveFixtureLoginPassword(t *testing.T, dir, appleID, adsid, password string) {
	t.Helper()
	session, _ := json.Marshal(appleservices.Session{
		AppleID: appleID,
		DSID:    adsid,
		Tokens:  map[string]string{"com.apple.gs.idms.pet": "fixture-pet"},
	})
	if err := os.WriteFile(filepath.Join(dir, "session.json"), session, 0600); err != nil {
		t.Fatal(err)
	}
	plain, _ := json.Marshal(protectedLoginPassword{Version: 1, AppleID: appleID, ADSID: adsid, Password: password})
	encrypted, err := protectLoginPasswordData(plain, loginPasswordEntropy(appleID, adsid), false)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(encrypted)
	if err := os.WriteFile(filepath.Join(dir, "login-password.dpapi"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSavedLoginPasswordRoundtripBindingAndSnapshot(t *testing.T) {
	dir := t.TempDir()
	const appleID = "owner@example.test"
	const adsid = "adsid-owner-1"
	const secret = "synthetic-password-fixture"
	saveFixtureLoginPassword(t, dir, appleID, adsid, secret)

	if !hasSavedLoginPassword(dir, "OWNER@example.test") {
		t.Fatal("expected saved password for matching account")
	}
	got, err := loadSavedLoginPassword(dir, "OWNER@example.test")
	if err != nil || got != secret {
		t.Fatalf("loadSavedLoginPassword() = (%q, %v), want %q", got, err, secret)
	}
	if hasSavedLoginPassword(dir, "other@example.test") {
		t.Fatal("saved password decrypted across different account")
	}

	// Change ADSID in session.json; decryption must fail cleanly.
	session, _ := json.Marshal(appleservices.Session{AppleID: appleID, DSID: "adsid-other"})
	if err := os.WriteFile(filepath.Join(dir, "session.json"), session, 0600); err != nil {
		t.Fatal(err)
	}
	if hasSavedLoginPassword(dir, appleID) {
		t.Fatal("saved password accepted after ADSID changed")
	}

	// Verify snapshotFor reports password_remembered=true from login-password.dpapi
	// even if runtime.json has password_remembered=false.
	saveFixtureLoginPassword(t, dir, appleID, adsid, secret)
	rt, _ := json.Marshal(Status{PasswordRemembered: false, State: "failed"})
	if err := os.WriteFile(filepath.Join(dir, "runtime.json"), rt, 0600); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		config:   Config{Dir: dir},
		accounts: []*Account{{ID: "01-account", AppleID: appleID, Dir: dir}},
	}
	snap := s.snapshotFor(context.Background(), "01-account")
	if !snap.Status.PasswordRemembered {
		t.Fatal("snapshotFor did not infer PasswordRemembered from saved login-password.dpapi")
	}

	// Verify startInModeInternal accepts blank password when login-password.dpapi is present,
	// advancing past the missing-password rejection ("请输入 Apple 账户和密码").
	result := s.startInModeInternal(context.Background(), dir, credentials{AppleID: appleID}, s.accounts[0], false, false)
	if result.Message == "请输入 Apple 账户和密码" {
		t.Fatalf("startInModeInternal did not reuse saved password: %+v", result)
	}
}
