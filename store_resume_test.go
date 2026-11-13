package appleservices

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/Laky-64/appleservices/icloud"
)

type commerceCheckpointStore struct {
	memoryStore
	data       []byte
	loads      int
	storeSaves int
}

func (s *commerceCheckpointStore) LoadStoreSession(string, string) ([]byte, error) {
	s.loads++
	return append([]byte(nil), s.data...), nil
}

func (s *commerceCheckpointStore) SaveStoreSession(_, _ string, data []byte) error {
	s.storeSaves++
	s.data = append([]byte(nil), data...)
	return nil
}

func rejectedCommerceCheckpoint(t *testing.T) []byte {
	t.Helper()
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range interfaces {
		if len(iface.HardwareAddr) != 6 {
			continue
		}
		data, _ := json.Marshal(map[string]any{
			"version": 1, "type": "result", "ok": true, "email": "owner@example.test", "dsid": "1234",
			"guid": strings.ToUpper(hex.EncodeToString(iface.HardwareAddr)), "reauthentication_required": true,
			"state": []byte(`{"Identifier":"synthetic-machine-state"}`),
		})
		if _, err := icloud.RestoreFrameworkStore(data, "owner@example.test", "1234"); err == nil {
			return data
		}
	}
	t.Skip("no usable physical Store identity on this test host")
	return nil
}

func TestRejectedStoreCheckpointSurvivesRestartWithoutTokenReuse(t *testing.T) {
	store := &commerceCheckpointStore{data: rejectedCommerceCheckpoint(t)}
	l := &Login{store: store, creds: Credentials{AppleID: "owner@example.test"}, client: &Client{DSID: "1234"}}
	ready, err := l.RestoreStorePurchaseSession(context.Background())
	if err != nil || ready || l.StorePurchaseSessionReady() || !l.storeCommerce.NeedsReauthentication() ||
		!l.storeCommerce.CanReuseFrameworkState(l.creds.AppleID, l.client.DSID) {
		t.Fatal("rejected persisted session was reported ready")
	}
	if _, err := l.RestoreStorePurchaseSession(context.Background()); err != nil || store.loads != 1 {
		t.Fatal("rejected session reloaded the stale checkpoint")
	}
	_, err = l.CheckSkillStoragePurchaseSession(context.Background(), nil)
	var failure *icloud.StorageError
	if !errors.As(err, &failure) || failure.Stage != "purchase-store-reconnect-required" || store.loads != 1 || store.storeSaves != 1 {
		t.Fatal("preflight reused a rejected token instead of requiring fresh authentication")
	}
	if err := l.saveStorePurchaseSession(); err != nil || store.storeSaves != 2 {
		t.Fatal("rejected state could not be checkpointed")
	}
}

func TestExplicitStoreRefreshDoesNotAcceptSavedSessionWithoutPassword(t *testing.T) {
	store := &commerceCheckpointStore{data: rejectedCommerceCheckpoint(t)}
	l := &Login{store: store, creds: Credentials{AppleID: "owner@example.test"}, client: &Client{DSID: "1234"}}
	_, _ = l.RestoreStorePurchaseSession(context.Background())
	_, err := l.RefreshSkillStoragePurchaseSession(context.Background(), nil)
	var failure *icloud.StorageError
	if !errors.As(err, &failure) || failure.Stage != "purchase-store-reconnect-required" || store.loads != 1 || store.storeSaves != 1 || l.StorePurchaseSessionReady() {
		t.Fatal("explicit refresh silently reused saved credentials")
	}
}
