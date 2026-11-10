package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Laky-64/appleservices"
)

func TestSavedLoginRequiresCompleteAccountBoundState(t *testing.T) {
	for _, name := range []string{"legacy", "accepted", "wrong-account", "missing-heartbeat", "missing-identity", "corrupt-delegate", "wrong-delegate"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			session := appleservices.Session{AppleID: "owner@example.test", DSID: "gsa", DeviceIdentityToken: "idms", Tokens: map[string]string{
				"com.apple.gs.idms.pet": "pet", "com.apple.gs.idms.hb": "hb", "com.apple.gs.appleid.auth": "auth",
			}}
			switch name {
			case "wrong-account":
				session.AppleID = "other@example.test"
			case "missing-heartbeat":
				delete(session.Tokens, "com.apple.gs.idms.hb")
			case "missing-identity":
				session.DeviceIdentityToken = ""
			}
			data, _ := json.Marshal(session)
			if err := os.WriteFile(filepath.Join(dir, "session.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if name == "accepted" || name == "wrong-delegate" {
				checkpoint := acceptedICloudCheckpoint{Session: appleservices.Session{AppleID: session.AppleID, DSID: "123", Tokens: map[string]string{"mmeAuthToken": "mme"}}, ADSID: session.DSID, ServiceData: map[string]any{}}
				if name == "wrong-delegate" {
					checkpoint.ADSID = "other"
				}
				data, _ = json.Marshal(checkpoint)
				if err := os.WriteFile(filepath.Join(dir, "icloud-accepted.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if name == "corrupt-delegate" {
				if err := os.WriteFile(filepath.Join(dir, "icloud-accepted.json"), []byte("invalid"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if got := savedLoginReady(dir, "OWNER@example.test"); got != (name == "legacy" || name == "accepted") {
				t.Fatal("unsafe or incomplete automatic resume eligibility")
			}
		})
	}
}
