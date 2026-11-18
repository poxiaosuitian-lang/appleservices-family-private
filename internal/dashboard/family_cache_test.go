package dashboard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Laky-64/appleservices/icloud"
	"github.com/Laky-64/appleservices/internal/control"
)

func TestListAccountsUsesPersonalPaidStorage(t *testing.T) {
	account := &Account{ID: "subscriber", Dir: t.TempDir()}
	server := &Server{accounts: []*Account{account}}
	for _, paid := range []uint64{50 << 30, 0} {
		if _, err := writeStorageCache(account, &icloud.StorageInfo{TotalBytes: 50 << 30, PaidBytes: paid}); err != nil {
			t.Fatal(err)
		}
		got := server.listAccounts(context.Background())[0]
		if got.ICloudPlus != (paid > 0) || got.FamilyOrganizer {
			t.Fatal("personal subscription was confused with shared capacity or family organizer status")
		}
	}
}

func TestListAccountsResolvesOrganizerForEveryCachedMember(t *testing.T) {
	root := t.TempDir()
	organizer := &Account{ID: "04-account", Name: "organizer", AppleID: "organizer@example.test", Dir: filepath.Join(root, "04-account")}
	members := []*Account{
		{ID: "01-primary", Name: "first", AppleID: "first@example.test", Dir: filepath.Join(root, "01-primary")},
		{ID: "03-account", Name: "second", AppleID: "second@example.test", Dir: filepath.Join(root, "03-account")},
		{ID: "05-account", Name: "third", AppleID: "third@example.test", Dir: filepath.Join(root, "05-account")},
	}
	for _, account := range append([]*Account{organizer}, members...) {
		if err := os.MkdirAll(account.Dir, 0700); err != nil {
			t.Fatal(err)
		}
		family := &control.FamilyInfo{Member: true, Members: 4, Organizers: 1, Self: 1, StorageKnown: true, StorageBytes: 50 << 30}
		if account == organizer {
			family.Organizer = true
			family.OrganizerAccount, family.OrganizerName = organizer.ID, organizer.Name
		}
		if _, err := writeFamilyCache(account, family); err != nil {
			t.Fatal(err)
		}
	}
	server := &Server{accounts: append([]*Account{organizer}, members...)}
	views := server.listAccounts(context.Background())
	if len(views) != 4 {
		t.Fatalf("account views = %d", len(views))
	}
	for _, view := range views {
		if view.ID == organizer.ID {
			continue
		}
		if !view.FamilyMember || view.FamilyOrganizer || view.FamilyOrganizerAccount != organizer.ID {
			t.Fatalf("member %s was not bound to organizer: %#v", view.ID, view)
		}
	}
}

func TestFamilyCacheRoundTripAndReplacement(t *testing.T) {
	account := &Account{ID: "04-account", Name: "organizer", Dir: t.TempDir()}
	family := &control.FamilyInfo{Member: true, Members: 3, Organizers: 1, Self: 1, Organizer: true, OrganizerAccount: account.ID, OrganizerName: account.Name, StorageKnown: true, StorageParticipating: true, StorageAvailable: true, StorageEnabled: true, StorageBytes: 50 << 30}
	first, err := writeFamilyCache(account, family)
	if err != nil {
		t.Fatal(err)
	}
	family.Members = 4
	second, err := writeFamilyCache(account, family)
	if err != nil {
		t.Fatal(err)
	}
	got, checkedAt, stale := readFamilyCache(account)
	if got == nil || got.Members != 4 || !got.Organizer || got.OrganizerAccount != account.ID || stale || checkedAt.Before(first) || second.Before(first) {
		t.Fatalf("unexpected cache state: %#v checked=%v stale=%t", got, checkedAt, stale)
	}
	info, err := os.Stat(familyCachePath(account))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		t.Fatalf("family cache permissions are not owner-only: %v %#o", err, info.Mode().Perm())
	}
}

func TestLegacyNegativeFamilyCacheRequiresRevalidation(t *testing.T) {
	account := &Account{ID: "organizer", Dir: t.TempDir()}
	for _, member := range []bool{false, true} {
		record := familyCacheRecord{Version: 1, AccountID: account.ID, CheckedAt: time.Now().UTC(), Family: control.FamilyInfo{Member: member, Organizer: member}}
		data, _ := json.Marshal(record)
		if err := os.WriteFile(familyCachePath(account), data, 0600); err != nil {
			t.Fatal(err)
		}
		got, _, _ := readFamilyCache(account)
		if (got != nil) != member {
			t.Fatalf("legacy membership %v accepted incorrectly", member)
		}
	}
	if _, err := writeFamilyCache(account, &control.FamilyInfo{Member: false, Eligible: true, EligibilityKnown: true}); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := readFamilyCache(account); got == nil || got.Member {
		t.Fatal("validated negative status not preserved")
	}
}

func TestFamilyCacheRejectsWrongAccountAndMarksOldStateStale(t *testing.T) {
	account := &Account{ID: "01-primary", Dir: t.TempDir()}
	record := familyCacheRecord{Version: familyCacheVersion, AccountID: "other", CheckedAt: time.Now().UTC(), Family: control.FamilyInfo{Member: true, Members: 3, Organizers: 1, Self: 1}}
	data, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(account.Dir, familyCacheFile), data, 0600); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := readFamilyCache(account); got != nil {
		t.Fatal("cache bound to another account was accepted")
	}
	record.AccountID = account.ID
	record.CheckedAt = time.Now().UTC().Add(-familyCacheFreshFor - time.Minute)
	data, _ = json.Marshal(record)
	if err := os.WriteFile(filepath.Join(account.Dir, familyCacheFile), data, 0600); err != nil {
		t.Fatal(err)
	}
	got, _, stale := readFamilyCache(account)
	if got == nil || !stale {
		t.Fatalf("old family cache was not retained and marked stale: %#v stale=%t", got, stale)
	}
}
