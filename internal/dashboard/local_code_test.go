package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appleservices "github.com/Laky-64/appleservices"
	"github.com/Laky-64/appleservices/internal/control"
	"github.com/Laky-64/appleservices/nativeadi"
	"github.com/google/uuid"
	"howett.net/plist"
)

func localCodeFixture(t *testing.T, seed byte) *Account {
	t.Helper()
	account := &Account{ID: fmt.Sprintf("fixture-%d", seed), AppleID: fmt.Sprintf("owner%d@example.test", seed), Dir: t.TempDir()}
	identifier := bytes.Repeat([]byte{seed}, 16)
	serial := fmt.Sprintf("TESTSERIAL%d", seed)
	session, _ := json.Marshal(appleservices.Session{AppleID: account.AppleID, DSID: "test-account", DeviceIdentityToken: "test-token"})
	device, _ := json.Marshal(appleservices.Device{Identifier: identifier, SerialNumber: serial})
	hardware, _ := plist.Marshal(map[string]any{
		"os_config": map[string]any{"device_id": uuid.UUID(identifier).String(), "inner": map[string]any{"platform_serial_number": serial}},
		"push":      map[string]any{"token": bytes.Repeat([]byte{seed}, 32)},
	}, plist.XMLFormat)
	provisioned, _ := plist.Marshal(map[string]any{"keychain_identifier": identifier}, plist.XMLFormat)
	files := map[string][]byte{
		"session.json": session, "device.json": device,
		"native-adi/hw_info.plist": hardware, "native-adi/anisette_test/state.plist": provisioned,
	}
	for name, data := range files {
		path := filepath.Join(account.Dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return account
}

func TestLocalCodeRejectsCrossAccountAndDeviceState(t *testing.T) {
	for _, mismatch := range []string{"none", "account", "device", "hardware", "provisioning", "missing"} {
		t.Run(mismatch, func(t *testing.T) {
			account, other := localCodeFixture(t, 1), localCodeFixture(t, 2)
			file := ""
			switch mismatch {
			case "account":
				file = "session.json"
			case "device":
				file = "device.json"
			case "hardware":
				file = "native-adi/hw_info.plist"
			case "provisioning":
				file = "native-adi/anisette_test/state.plist"
			case "missing":
				if err := os.Remove(filepath.Join(account.Dir, "native-adi/anisette_test/state.plist")); err != nil {
					t.Fatal(err)
				}
			}
			if file != "" {
				data, err := os.ReadFile(filepath.Join(other.Dir, file))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(account.Dir, file), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := localDeviceConfig(account)
			if (err == nil) != (mismatch == "none") {
				t.Fatal("incorrect account/device binding decision")
			}
		})
	}
}

func TestOfflineCodeUsesSelectedDeviceWithoutPromotingLogin(t *testing.T) {
	server, err := New(Config{Dir: t.TempDir(), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer server.listener.Close()
	server.accounts = []*Account{localCodeFixture(t, 1), localCodeFixture(t, 2)}
	generated := 0
	server.generateLocalCode = func(_ context.Context, config nativeadi.Config) (string, error) {
		generated++
		if config.ProfileDir != filepath.Join(server.accounts[generated-1].Dir, "native-adi") || config.Identifier[0] != byte(generated) {
			t.Fatal("selected account used another device")
		}
		return "123456", nil
	}
	for _, account := range server.accounts {
		view := server.snapshotFor(context.Background(), account.ID)
		if !view.LocalCodeAvailable || view.Status.Authenticated {
			t.Fatal("offline availability required an authenticated iCloud session")
		}
		before := generated
		request := httptest.NewRequest("POST", server.URL()+"/api/action?account="+account.ID, strings.NewReader(`{"action":"local-device-code"}`))
		request.Header.Set("X-App-Token", server.token)
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		var result control.Result
		if json.Unmarshal(recorder.Body.Bytes(), &result) != nil || !result.OK || result.Code != "123456" || result.AccountID != account.ID || result.CodeKind != "local-device" || result.HideAt.IsZero() || generated != before+1 {
			t.Fatal("offline device code was not bound to the selected account")
		}
		if recorder.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("device code may be cached")
		}
		view = server.snapshotFor(context.Background(), account.ID)
		encoded, _ := json.Marshal(view)
		if view.Status.Authenticated || bytes.Contains(encoded, []byte("123456")) || generated != before+1 {
			t.Fatal("polling exposed/generated a code or promoted authentication")
		}
		if _, err := os.Stat(filepath.Join(account.Dir, "icloud-accepted.json")); !os.IsNotExist(err) {
			t.Fatal("local generation created an accepted iCloud checkpoint")
		}
	}
}
