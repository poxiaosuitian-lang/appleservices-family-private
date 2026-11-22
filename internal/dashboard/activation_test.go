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
