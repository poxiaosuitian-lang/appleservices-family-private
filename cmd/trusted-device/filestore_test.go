package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Laky-64/appleservices"
)

func TestLoadAcceptedICloudRequiresSameSavedAccount(t *testing.T) {
	dir := t.TempDir()
	store := fileStore{dir: dir}
	saved := &appleservices.Session{AppleID: "owner@example.test", DSID: "gsa-account", Tokens: map[string]string{"com.apple.gs.idms.pet": "pet"}}
	if err := store.SaveSession(saved); err != nil {
		t.Fatal(err)
	}
	checkpoint := map[string]any{
		"session":      appleservices.Session{AppleID: saved.AppleID, DSID: "icloud-account", Tokens: map[string]string{"mmeAuthToken": "mme", "cloudKitToken": "cloudkit"}},
		"adsid":        saved.DSID,
		"service-data": map[string]any{"quota": true},
	}
	data, _ := json.Marshal(checkpoint)
	if err := os.WriteFile(filepath.Join(dir, "icloud-accepted.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	accepted, err := store.LoadAcceptedICloud()
	if err != nil {
		t.Fatal(err)
	}
	if accepted.DSID != "icloud-account" || accepted.ADSID != saved.DSID || accepted.MMEAuthToken == "" || accepted.ServiceData == nil {
		t.Fatalf("unexpected restored checkpoint: %#v", accepted)
	}
	checkpoint["adsid"] = "other-account"
	data, _ = json.Marshal(checkpoint)
	if err := os.WriteFile(filepath.Join(dir, "icloud-accepted.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadAcceptedICloud(); err == nil {
		t.Fatal("cross-account accepted checkpoint was restored")
	}
}

func TestAcceptedSessionSavedForRestartAndRemovedOnLogout(t *testing.T) {
	store := fileStore{dir: t.TempDir()}
	saved := &appleservices.Session{AppleID: "owner@example.test", DSID: "gsa"}
	if err := store.SaveSession(saved); err != nil {
		t.Fatal(err)
	}
	accepted := appleservices.RestoredICloudSession{ADSID: "gsa", DSID: "123", MMEAuthToken: "mme", ServiceData: map[string]any{}}
	if err := store.SaveAcceptedICloud("other@example.test", accepted); err == nil {
		t.Fatal("cross-account write accepted")
	}
	if err := store.SaveAcceptedICloud(saved.AppleID, accepted); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadAcceptedICloud()
	if err != nil || loaded.ADSID != accepted.ADSID || loaded.MMEAuthToken != accepted.MMEAuthToken {
		t.Fatal("checkpoint not restored")
	}
	if err := appleservices.Logout(store); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.acceptedPath()); !os.IsNotExist(err) {
		t.Fatal("logout retained delegate tokens")
	}
}

func TestResumeDoesNotMigrateMalformedCheckpoint(t *testing.T) {
	store := fileStore{dir: t.TempDir()}
	if err := os.WriteFile(store.acceptedPath(), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := resumeSavedLogin("owner@example.test", store); err == nil {
		t.Fatal("invalid checkpoint was migrated")
	}
}
