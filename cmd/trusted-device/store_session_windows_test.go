//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Laky-64/appleservices"
)

func TestStoreSessionDPAPIAccountBindingAndLogout(t *testing.T) {
	store := fileStore{dir: t.TempDir()}
	if err := store.SaveSession(&appleservices.Session{AppleID: "owner@example.test", DSID: "gsa"}); err != nil {
		t.Fatal(err)
	}
	secret := []byte("synthetic-private-store-session")
	if err := store.SaveStoreSession("owner@example.test", "123", secret); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(store.dir, "store-session.dpapi"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, secret) || bytes.Contains(data, []byte("owner@example.test")) {
		t.Fatal("plaintext Store session persisted")
	}
	got, err := store.LoadStoreSession("OWNER@example.test", "123")
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatal("protected session did not roundtrip")
	}
	for _, binding := range [][2]string{{"other@example.test", "123"}, {"owner@example.test", "456"}} {
		if _, err := store.LoadStoreSession(binding[0], binding[1]); err == nil {
			t.Fatal("cross-account session decrypted")
		}
	}
	if err := appleservices.Logout(store); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(store.dir, "store-session.dpapi")); !os.IsNotExist(err) {
		t.Fatal("logout retained encrypted credentials")
	}
}

func TestStoreSessionUsesAppleValidationInsteadOfLocalAgeLimit(t *testing.T) {
	store := fileStore{dir: t.TempDir()}
	plain, _ := json.Marshal(protectedStoreSession{1, "owner@example.test", "123", time.Now().Add(-25 * time.Hour), []byte("test")})
	encrypted, err := protectStoreData(plain, storeEntropy("owner@example.test", "123"), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONFile(filepath.Join(store.dir, "store-session.dpapi"), encrypted); err != nil {
		t.Fatal(err)
	}
	if state, err := store.LoadStoreSession("owner@example.test", "123"); err != nil || string(state) != "test" {
		t.Fatal("local age limit prevented Apple from validating the saved session")
	}
	encrypted[len(encrypted)-1] ^= 1
	if _, err := protectStoreData(encrypted, storeEntropy("owner@example.test", "123"), true); err == nil {
		t.Fatal("tampered DPAPI data accepted")
	}
}
