//go:build windows

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Laky-64/appleservices"
)

func TestRememberedPasswordRequiresBoundSessionAndEncryptsOnDisk(t *testing.T) {
	store := fileStore{dir: t.TempDir()}
	const email = "owner@example.test"
	const secret = "synthetic-test-password-not-a-real-secret"
	if err := store.SaveLoginPassword(email, secret); err == nil {
		t.Fatal("saved before account binding")
	}
	if err := store.SaveSession(&appleservices.Session{AppleID: email, DSID: "gsa-owner"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLoginPassword("other@example.test", secret); err == nil {
		t.Fatal("cross-account password saved")
	}
	if err := store.SaveLoginPassword(email, secret); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(store.dir, "login-password.dpapi"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(secret)) || bytes.Contains(data, []byte(email)) {
		t.Fatal("credential stored as plaintext")
	}
	if got, err := store.LoadLoginPassword("OWNER@example.test"); err != nil || got != secret {
		t.Fatal("password did not roundtrip")
	}
	if _, err := store.LoadLoginPassword("other@example.test"); err == nil {
		t.Fatal("other account read password")
	}
	if err := store.SaveSession(&appleservices.Session{AppleID: email, DSID: "different-owner"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadLoginPassword(email); err == nil {
		t.Fatal("changed account identity decrypted password")
	}
	if err := appleservices.Logout(store); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(store.dir, "login-password.dpapi")); !os.IsNotExist(err) {
		t.Fatal("logout retained password")
	}
}

func TestRememberedPasswordCanBeForgottenWithoutLoggingOut(t *testing.T) {
	store := fileStore{dir: t.TempDir()}
	if err := store.SaveSession(&appleservices.Session{AppleID: "owner@example.test", DSID: "gsa"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLoginPassword("owner@example.test", "synthetic"); err != nil {
		t.Fatal(err)
	}
	if err := store.ForgetLoginPassword(); err != nil {
		t.Fatal(err)
	}
	if savedLoginPassword(store, "owner@example.test") != "" {
		t.Fatal("forgotten password was loaded")
	}
	if session, err := store.LoadSession(); err != nil || session == nil {
		t.Fatal("forget password cleared the login session")
	}
}
