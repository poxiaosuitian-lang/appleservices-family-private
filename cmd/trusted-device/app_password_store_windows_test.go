//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Laky-64/appleservices"
)

func appPasswordTestStore(t *testing.T) fileStore {
	t.Helper()
	store := fileStore{dir: t.TempDir()}
	if err := store.SaveSession(&appleservices.Session{AppleID: "Owner@example.test", DSID: "synthetic-app-password-owner"}); err != nil {
		t.Fatal(err)
	}
	return store
}

func appPasswordTestRecord() *appPasswordRecord {
	return &appPasswordRecord{
		Password: "abcd-efgh-ijkl-mnop", Label: "synthetic-mail-label", ID: "synthetic-password-id",
		CreatedAt: time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
	}
}

func TestAppPasswordDPAPIRoundTripIsBoundAndRepeatable(t *testing.T) {
	store := appPasswordTestStore(t)
	record := appPasswordTestRecord()
	if err := store.SaveAppPassword(" OWNER@EXAMPLE.TEST ", record); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.dir, appPasswordFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var encrypted []byte
	if err := json.Unmarshal(data, &encrypted); err != nil || len(encrypted) == 0 {
		t.Fatal("saved credential is not a DPAPI ciphertext envelope")
	}
	for _, secret := range []string{record.Password, record.Label, record.ID, "owner@example.test", "synthetic-app-password-owner"} {
		if bytes.Contains(data, []byte(secret)) || bytes.Contains(encrypted, []byte(secret)) {
			t.Fatal("credential metadata or password persisted as plaintext")
		}
	}
	for range 3 {
		got, err := store.LoadAppPassword("owner@example.test")
		if err != nil || got == nil || *got != *record {
			t.Fatal("protected application-specific password did not roundtrip")
		}
		got.Password = "zzzz-zzzz-zzzz-zzzz"
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, data) {
		t.Fatal("loading modified the stored credential")
	}
	if _, err := store.LoadAppPassword("other@example.test"); err == nil {
		t.Fatal("another account requested this credential")
	}
	for _, session := range []*appleservices.Session{
		{AppleID: "owner@example.test", DSID: "different-synthetic-owner"},
		{AppleID: "other@example.test", DSID: "synthetic-app-password-owner"},
	} {
		if err := store.SaveSession(session); err != nil {
			t.Fatal(err)
		}
		if _, err := store.LoadAppPassword(session.AppleID); err == nil {
			t.Fatal("changed account binding decrypted credential")
		}
	}
}

func TestAppPasswordStorageProbeLeavesSavedCredentialUntouched(t *testing.T) {
	store := appPasswordTestStore(t)
	if err := store.CheckAppPasswordStorage("owner@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadAppPassword("owner@example.test"); !os.IsNotExist(err) {
		t.Fatal("storage probe unexpectedly created a credential")
	}
	if err := store.SaveAppPassword("owner@example.test", appPasswordTestRecord()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.dir, appPasswordFile)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CheckAppPasswordStorage("owner@example.test"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("storage probe modified the saved credential")
	}
	entries, err := os.ReadDir(store.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatal("storage probe retained a temporary file")
		}
	}
}

func TestAppPasswordRequiresBoundSessionAndValidRecord(t *testing.T) {
	store := fileStore{dir: t.TempDir()}
	if err := store.SaveAppPassword("owner@example.test", appPasswordTestRecord()); err == nil {
		t.Fatal("saved credential without account binding")
	}
	if err := store.CheckAppPasswordStorage("owner@example.test"); err == nil {
		t.Fatal("storage probe passed without account binding")
	}
	if _, err := store.LoadAppPassword("owner@example.test"); err == nil {
		t.Fatal("loaded credential without account binding")
	}
	store = appPasswordTestStore(t)
	if err := store.SaveAppPassword("other@example.test", appPasswordTestRecord()); err == nil {
		t.Fatal("saved credential for a different account")
	}
	if err := store.SaveAppPassword("owner@example.test", nil); err == nil {
		t.Fatal("saved absent credential record")
	}
	for _, mutate := range []func(*appPasswordRecord){
		func(r *appPasswordRecord) { r.Password = "ordinary-account-password" },
		func(r *appPasswordRecord) { r.Label = " " },
		func(r *appPasswordRecord) { r.ID = "" },
		func(r *appPasswordRecord) { r.CreatedAt = time.Time{} },
	} {
		record := appPasswordTestRecord()
		mutate(record)
		if err := store.SaveAppPassword("owner@example.test", record); err == nil {
			t.Fatal("saved invalid credential record")
		}
	}
	if _, err := os.Stat(filepath.Join(store.dir, appPasswordFile)); !os.IsNotExist(err) {
		t.Fatal("invalid save created a credential file")
	}
}

func TestAppPasswordRejectsCorruptAndMismatchedRecords(t *testing.T) {
	store := appPasswordTestStore(t)
	path := filepath.Join(store.dir, appPasswordFile)
	for _, data := range [][]byte{[]byte("not-json"), []byte("null"), []byte(`"cGxhaW50ZXh0"`), bytes.Repeat([]byte("a"), (32<<10)+1)} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.LoadAppPassword("owner@example.test"); err == nil {
			t.Fatal("corrupt credential accepted")
		}
	}
	for _, mutate := range []func(*protectedAppPassword){
		func(s *protectedAppPassword) { s.Version = 2 },
		func(s *protectedAppPassword) { s.AppleID = "other@example.test" },
		func(s *protectedAppPassword) { s.DSID = "different-owner" },
		func(s *protectedAppPassword) { s.Record.Password = "invalid" },
		func(s *protectedAppPassword) { s.Record.ID = "" },
		func(s *protectedAppPassword) { s.Record.CreatedAt = time.Time{} },
	} {
		saved := protectedAppPassword{Version: 1, AppleID: "owner@example.test", DSID: "synthetic-app-password-owner", Record: *appPasswordTestRecord()}
		mutate(&saved)
		plain, err := json.Marshal(saved)
		if err != nil {
			t.Fatal(err)
		}
		encrypted, err := protectStoreData(plain, appPasswordEntropy("owner@example.test", "synthetic-app-password-owner"), false)
		clear(plain)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeJSONFile(path, encrypted); err != nil {
			t.Fatal(err)
		}
		if _, err := store.LoadAppPassword("owner@example.test"); err == nil {
			t.Fatal("protected but invalid payload accepted")
		}
	}
	if err := store.SaveAppPassword("owner@example.test", appPasswordTestRecord()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var encrypted []byte
	if err := json.Unmarshal(data, &encrypted); err != nil {
		t.Fatal(err)
	}
	encrypted[len(encrypted)-1] ^= 1
	if err := writeJSONFile(path, encrypted); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadAppPassword("owner@example.test"); err == nil {
		t.Fatal("tampered DPAPI ciphertext accepted")
	}
}

func TestAppPasswordFailedReplacementPreservesSavedCredential(t *testing.T) {
	store := appPasswordTestStore(t)
	record := appPasswordTestRecord()
	if err := store.SaveAppPassword("owner@example.test", record); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.dir, appPasswordFile)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0600) })
	if err := store.CheckAppPasswordStorage("owner@example.test"); err == nil {
		t.Fatal("storage probe accepted a read-only destination")
	}
	replacement := *record
	replacement.Password = "zzzz-aaaa-bbbb-cccc"
	if err := store.SaveAppPassword("owner@example.test", &replacement); err == nil {
		t.Fatal("replacement unexpectedly succeeded for read-only destination")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed replacement changed the saved ciphertext")
	}
	got, err := store.LoadAppPassword("owner@example.test")
	if err != nil || got == nil || *got != *record {
		t.Fatal("failed replacement lost the saved credential")
	}
	entries, err := os.ReadDir(store.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatal("failed replacement retained a temporary file")
		}
	}
}
