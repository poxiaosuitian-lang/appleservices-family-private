package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/Laky-64/appleservices/internal/control"
)

func familyJSONLine(info control.FamilyInfo) string {
	data, _ := json.Marshal(info)
	return familyStatusPrefix + string(data) + "\n"
}

func TestEnsureOrganizerFamilyReadyCreatesFamilyAndSharesStorageForStandaloneHost(t *testing.T) {
	var calls [][]string
	run := func(_ context.Context, _ string, args, _ []string) (string, error) {
		calls = append(calls, append([]string(nil), args...))
		switch {
		case slices.Contains(args, "-create-family"):
			return familyJSONLine(control.FamilyInfo{Member: true, Organizer: true, Members: 1, Organizers: 1, Self: 1}), nil
		case slices.Contains(args, "-share-icloud-storage"):
			return familyJSONLine(control.FamilyInfo{Member: true, Organizer: true, Members: 1, Organizers: 1, Self: 1, StorageKnown: true, StorageEnabled: true, StorageAvailable: true, StorageParticipating: true}), nil
		default:
			return familyJSONLine(control.FamilyInfo{Member: false, EligibilityKnown: true, Eligible: true}), nil
		}
	}
	if err := ensureOrganizerFamilyReady(context.Background(), "familyinspect.exe", "state/accounts/01-account", run); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("expected 3 helper calls (inspect, create, share), got %d: %#v", len(calls), calls)
	}
}

func TestEnsureOrganizerFamilyReadyNoopsForExistingOrganizerWithSharedStorage(t *testing.T) {
	var calls int
	run := func(_ context.Context, _ string, args, _ []string) (string, error) {
		calls++
		if slices.Contains(args, "-create-family") || slices.Contains(args, "-share-icloud-storage") {
			t.Fatalf("unexpected mutation call: %#v", args)
		}
		return familyJSONLine(control.FamilyInfo{Member: true, Organizer: true, Members: 3, Organizers: 1, Self: 1, StorageKnown: true, StorageEnabled: true}), nil
	}
	if err := ensureOrganizerFamilyReady(context.Background(), "familyinspect.exe", "state/accounts/02-account", run); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 read-only call, got %d", calls)
	}
}

func TestEnsureOrganizerFamilyReadyRejectsNonOrganizerMember(t *testing.T) {
	run := func(_ context.Context, _ string, args, _ []string) (string, error) {
		if slices.Contains(args, "-create-family") || slices.Contains(args, "-share-icloud-storage") {
			t.Fatalf("unexpected mutation call: %#v", args)
		}
		return familyJSONLine(control.FamilyInfo{Member: true, Organizer: false, Members: 3, Organizers: 1, Self: 1}), nil
	}
	if err := ensureOrganizerFamilyReady(context.Background(), "familyinspect.exe", "state/accounts/04-account", run); err == nil {
		t.Fatal("non-organizer member was accepted as host")
	}
}

func TestEnsureOrganizerFamilyReadyPropagatesCreateFailure(t *testing.T) {
	run := func(_ context.Context, _ string, args, _ []string) (string, error) {
		if slices.Contains(args, "-create-family") {
			return "apple refused createFamily\n", errors.New("exit status 1")
		}
		return familyJSONLine(control.FamilyInfo{Member: false, EligibilityKnown: true, Eligible: true}), nil
	}
	if err := ensureOrganizerFamilyReady(context.Background(), "familyinspect.exe", "state/accounts/01-account", run); err == nil {
		t.Fatal("createFamily failure was ignored")
	}
}
