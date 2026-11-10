package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Laky-64/appleservices"
	"github.com/Laky-64/appleservices/internal/mailsetup"
)

func TestFirstLoginCreatesIdentityBeforePushAndReusesItOnRetry(t *testing.T) {
	root := t.TempDir()
	worker := filepath.Join(root, "worker.exe")
	if err := os.WriteFile(worker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{Dir: root, Worker: worker, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer s.listener.Close()
	var first []byte
	var accountDir string
	calls := 0
	s.provisionMacPush = func(_ context.Context, dir string) error {
		calls++
		if filepath.Dir(dir) != accountsDir(root) {
			t.Fatal("first login did not use an isolated account directory")
		}
		data, err := os.ReadFile(filepath.Join(dir, "device.json"))
		if err != nil {
			t.Fatal("push bootstrap was called without a saved identity")
		}
		var device appleservices.Device
		if json.Unmarshal(data, &device) != nil || len(device.Identifier) != 16 || device.SerialNumber == "" {
			t.Fatal("invalid first-login identity")
		}
		if first == nil {
			first, accountDir = data, dir
		} else if dir != accountDir || !bytes.Equal(first, data) {
			t.Fatal("retry replaced the account identity")
		}
		// No real Apple connection or worker process is needed for this regression.
		return errors.New("simulated network failure")
	}
	creds := credentials{AppleID: "first@example.test", Password: "test-only-password"}
	for i := 0; i < 2; i++ {
		if result := s.Start(context.Background(), creds); result.OK {
			t.Fatal("failed bootstrap reported successful login")
		}
	}
	if calls != 4 || len(s.accounts) != 1 {
		t.Fatalf("bootstrap calls=%d accounts=%d; want 4 and 1", calls, len(s.accounts))
	}
	entries, err := loadRegistry(root, root)
	if err != nil || len(entries) != 1 || entries[0].Dir != accountDir {
		t.Fatal("first account was not persisted for a retry after restart")
	}
	data, err := os.ReadFile(registryPath(root))
	mail, mailErr := mailsetup.Load(accountDir, creds.AppleID)
	if mailErr != nil || !mail.AutoCreate || mail.Phase != "pending" || mail.Submitted {
		t.Fatal("new account did not retain its unsubmitted mailbox task across failed login/retry")
	}
	if err != nil || bytes.Contains(data, []byte(creds.Password)) {
		t.Fatal("registry unavailable or contains the password")
	}
	if _, err := os.Stat(filepath.Join(root, "device.json")); !os.IsNotExist(err) {
		t.Fatal("first login wrote shared root identity")
	}
}

func TestEmptyFirstLoginDoesNotCreateAccount(t *testing.T) {
	root := t.TempDir()
	s, err := New(Config{Dir: root, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer s.listener.Close()
	if result := s.Start(context.Background(), credentials{}); result.OK {
		t.Fatal("empty credentials accepted")
	}
	files, err := os.ReadDir(root)
	if err != nil || len(files) != 0 || len(s.accounts) != 0 {
		t.Fatal("empty login created account state")
	}
}
