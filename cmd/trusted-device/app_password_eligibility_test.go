package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Laky-64/appleservices/icloud"
	"github.com/Laky-64/appleservices/internal/control"
)

func TestAutomaticAppPasswordEligibility(t *testing.T) {
	for _, tc := range []struct {
		name          string
		family        bool
		stale         bool
		participating bool
		paid          uint64
		readErr       bool
		want          bool
	}{
		{name: "personal", paid: 50 << 30, want: true},
		{name: "free"},
		{name: "shared", family: true, participating: true, want: true},
		{name: "shared with quota failure", family: true, participating: true, readErr: true, want: true},
		{name: "stale shared", family: true, participating: true, stale: true},
		{name: "not using shared storage", family: true},
		{name: "failed personal query", paid: 50 << 30, readErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.family {
				checked := time.Now().UTC()
				if tc.stale {
					checked = checked.Add(-16 * time.Minute)
				}
				data, err := json.Marshal(hmeFamilyCache{Version: 2, AccountID: filepath.Base(dir), CheckedAt: checked,
					Family: control.FamilyInfo{Member: true, Organizers: 1, Self: 1, StorageKnown: true, StorageParticipating: tc.participating, StorageAvailable: true, StorageEnabled: true, StorageBytes: 50 << 30}})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "family-status.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			source := hmeQuotaFixture{quota: &icloud.StorageInfo{PaidBytes: tc.paid}}
			if tc.readErr {
				source.err = errors.New("quota unavailable")
			}
			got, err := automaticAppPasswordEligible(context.Background(), dir, source)
			if got != tc.want || (tc.want && err != nil) {
				t.Fatalf("eligible=%v err=%v", got, err)
			}
		})
	}
}
