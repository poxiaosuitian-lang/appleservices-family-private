package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	appleservices "github.com/Laky-64/appleservices"
)

func TestParseActivationPlanReturnsOnlySafeTermsMetadata(t *testing.T) {
	output := []byte("diagnostic\n" + `{"terms-plan":true,"region":"US","icloud-version":"630101","media-version":"290571"}` + "\n")
	plan, err := parseActivationPlan(output)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Required || activationTuple(plan) != "US:630101:290571" || plan.ICloudTermsURL == "" || plan.MediaTermsURL == "" {
		t.Fatalf("unexpected activation plan: %#v", plan)
	}
}

func TestVerifiedGSACheckpointRequiresAccountBoundPET(t *testing.T) {
	dir := t.TempDir()
	session := appleservices.Session{AppleID: "owner@example.test", DSID: "123", Tokens: map[string]string{"com.apple.gs.idms.pet": "private"}}
	data, _ := json.Marshal(session)
	if err := os.WriteFile(filepath.Join(dir, "session.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if !verifiedGSACheckpointReady(dir, "OWNER@example.test") {
		t.Fatal("valid account-bound GSA checkpoint was rejected")
	}
	if verifiedGSACheckpointReady(dir, "other@example.test") {
		t.Fatal("GSA checkpoint crossed account binding")
	}
	session.Tokens = nil
	data, _ = json.Marshal(session)
	if err := os.WriteFile(filepath.Join(dir, "session.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if verifiedGSACheckpointReady(dir, session.AppleID) {
		t.Fatal("checkpoint without PET was accepted")
	}
}

func TestFilteredActivationEnvSetsRunAsInvokerAndStripsSecrets(t *testing.T) {
	t.Setenv("APPLESERVICES_PASSWORD", "secret")
	t.Setenv("HTTP_PROXY", "http://proxy.invalid")
	t.Setenv("__COMPAT_LAYER", "RequireAdministrator")
	env := filteredActivationEnv("owner@example.test")
	if !slices.Contains(env, "__COMPAT_LAYER=RunAsInvoker") {
		t.Fatal("activation env did not set __COMPAT_LAYER=RunAsInvoker")
	}
	if !slices.Contains(env, "APPLESERVICES_APPLEID=owner@example.test") || !slices.Contains(env, "APPLESERVICES_DELEGATE_PATH=v2") || !slices.Contains(env, "NO_PROXY=*") {
		t.Fatal("activation env missing required settings")
	}
	for _, entry := range env {
		if entry == "APPLESERVICES_PASSWORD=secret" || entry == "HTTP_PROXY=http://proxy.invalid" || entry == "__COMPAT_LAYER=RequireAdministrator" {
			t.Fatalf("unexpected inherited env entry: %s", entry)
		}
	}
}

func TestShouldAutoActivateICloudRequiresDelegateRefusalAndUnactivatedPET(t *testing.T) {
	dir := t.TempDir()
	account := &Account{ID: "06-account", AppleID: "owner@example.test", Dir: dir}
	diag := filepath.Join(dir, "daemon.err.log")
	if err := os.WriteFile(diag, []byte("starting...\ntrusted-device: appleservices: iCloud login: icloud: mobileme delegate refused status=1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if shouldAutoActivateICloud(diag, account) {
		t.Fatal("auto-activation allowed without verified GSA PET")
	}
	session := appleservices.Session{AppleID: account.AppleID, DSID: "123", Tokens: map[string]string{"com.apple.gs.idms.pet": "private"}}
	data, _ := json.Marshal(session)
	if err := os.WriteFile(filepath.Join(dir, "session.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if !shouldAutoActivateICloud(diag, account) {
		t.Fatal("expected auto-activation for unactivated account with verified PET and mobileme delegate refusal")
	}
	if shouldAutoActivateICloud(diag, &Account{ID: "06-account", AppleID: "other@example.test", Dir: dir}) {
		t.Fatal("auto-activation crossed account binding")
	}
	if err := os.WriteFile(diag, []byte("trusted-device: gsa: ec=-22406 Enter the correct password\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if shouldAutoActivateICloud(diag, account) {
		t.Fatal("auto-activation triggered on non-delegate failure")
	}
	if err := os.WriteFile(diag, []byte("trusted-device: appleservices: iCloud login: icloud: mobileme delegate refused status=1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	accepted, _ := json.Marshal(map[string]any{
		"session":      map[string]any{"appleId": account.AppleID, "dsid": "123", "tokens": map[string]string{"mmeAuthToken": "private"}},
		"adsid":        "123",
		"service-data": map[string]any{},
	})
	if err := os.WriteFile(filepath.Join(dir, "icloud-accepted.json"), accepted, 0600); err != nil {
		t.Fatal(err)
	}
	if shouldAutoActivateICloud(diag, account) {
		t.Fatal("auto-activation triggered for already activated account")
	}
}

func TestActivationUnauthorizedDetectionAndCheckpointReplace(t *testing.T) {
	if !isActivationUnauthorized([]byte(`{"HTTP":401,"bytes":0,"gsa-authenticated":true}`)) {
		t.Fatal("failed to detect HTTP 401 output")
	}
	if isActivationUnauthorized([]byte(`{"HTTP":200,"bytes":123}`)) {
		t.Fatal("HTTP 200 misclassified as unauthorized")
	}

	dir := t.TempDir()
	dst := filepath.Join(dir, "session.json")
	src := filepath.Join(dir, "refreshed.json")
	valid, _ := json.Marshal(appleservices.Session{
		AppleID: "owner@example.test",
		DSID:    "dsid-1",
		Tokens:  map[string]string{"com.apple.gs.idms.pet": "fresh-pet"},
	})
	if err := os.WriteFile(src, valid, 0600); err != nil {
		t.Fatal(err)
	}
	if err := replaceGSACheckpoint(src, dst, "OWNER@example.test"); err != nil {
		t.Fatalf("unexpected error replacing checkpoint: %v", err)
	}
	if !verifiedGSACheckpointReady(dir, "owner@example.test") {
		t.Fatal("replaced checkpoint failed readiness check")
	}
	if err := replaceGSACheckpoint(src, dst, "other@example.test"); err == nil {
		t.Fatal("replaced checkpoint across mismatched account")
	}
	missingPET, _ := json.Marshal(appleservices.Session{
		AppleID: "owner@example.test",
		DSID:    "dsid-1",
		Tokens:  map[string]string{},
	})
	if err := os.WriteFile(src, missingPET, 0600); err != nil {
		t.Fatal(err)
	}
	if err := replaceGSACheckpoint(src, dst, "owner@example.test"); err == nil {
		t.Fatal("replaced checkpoint without PET")
	}
}
