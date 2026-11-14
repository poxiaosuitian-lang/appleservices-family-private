//go:build windows

package dashboard

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Laky-64/appleservices/internal/control"
)

func TestSyncProfilesPersistSelectAndProtectSecrets(t *testing.T) {
	s := &Server{config: Config{Dir: t.TempDir()}}
	token := strings.Repeat("fixture-secret-", 4)
	first, err := s.changeSyncProfiles(syncProfileInput{Operation: "save", URL: "https://one.example.test/", Token: token})
	if err != nil {
		t.Fatal(err)
	}
	id := first.Selected
	raw, err := os.ReadFile(filepath.Join(s.config.Dir, "sync-profiles.dpapi"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(token)) || bytes.Contains(raw, []byte("one.example.test")) {
		t.Fatal("plaintext persisted")
	}
	restarted := &Server{config: s.config}
	p, err := restarted.resolveSyncProfile(id, "https://one.example.test")
	if err != nil || p.Token != token {
		t.Fatal("restart did not restore credential")
	}
	kept, err := restarted.changeSyncProfiles(syncProfileInput{Operation: "save", ID: id, URL: p.URL})
	if err != nil || kept.Profiles[0].Token != token {
		t.Fatal("blank edit must preserve key")
	}
	if _, err = restarted.changeSyncProfiles(syncProfileInput{Operation: "save", ID: id, URL: "https://other.example.test"}); err == nil {
		t.Fatal("old secret reused at new address")
	}
	second, err := restarted.changeSyncProfiles(syncProfileInput{Operation: "save", URL: "https://two.example.test", Token: strings.Repeat("x", 32)})
	if err != nil {
		t.Fatal(err)
	}
	secondID := second.Selected
	selected, err := restarted.changeSyncProfiles(syncProfileInput{Operation: "select", ID: id})
	if err != nil || selected.Selected != id {
		t.Fatal("select failed")
	}
	if _, err = restarted.resolveSyncProfile(id, "https://two.example.test"); err == nil {
		t.Fatal("stale destination accepted")
	}
	changed, err := restarted.changeSyncProfiles(syncProfileInput{Operation: "save", ID: id, URL: p.URL, Token: strings.Repeat("y", 32)})
	if err != nil || changed.Profiles[0].Token != strings.Repeat("y", 32) {
		t.Fatal("key replacement failed")
	}
	deleted, err := restarted.changeSyncProfiles(syncProfileInput{Operation: "delete", ID: id})
	if err != nil || deleted.Selected != secondID || len(deleted.Profiles) != 1 {
		t.Fatal("delete failed")
	}
	if _, err = restarted.resolveSyncProfile(id, p.URL); err == nil {
		t.Fatal("deleted key usable")
	}
}

func TestSyncActionResolvesSelectedProfileOnlyInBackend(t *testing.T) {
	dir := t.TempDir()
	executable, _ := os.Executable()
	s, err := New(Config{Dir: dir, Worker: executable, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer s.listener.Close()
	secret := strings.Repeat("fixture-key", 4)
	value, err := s.changeSyncProfiles(syncProfileInput{Operation: "save", URL: "https://destination.example.test", Token: secret})
	if err != nil {
		t.Fatal(err)
	}
	var got control.Action
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error("invalid worker command")
		}
		json.NewEncoder(w).Encode(control.Result{OK: true})
	}))
	defer worker.Close()
	record, _ := json.Marshal(control.Record{PID: os.Getpid(), URL: worker.URL, Token: strings.Repeat("a", 64)})
	if err := os.WriteFile(filepath.Join(dir, "control.json"), record, 0600); err != nil {
		t.Fatal(err)
	}
	status, _ := json.Marshal(Status{PID: os.Getpid(), Running: true})
	if err := os.WriteFile(filepath.Join(dir, "runtime.json"), status, 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hme-server-sync", "hme-server-login"} {
		body, _ := json.Marshal(map[string]string{"action": name, "profile_id": value.Selected, "note": "https://destination.example.test"})
		r := httptest.NewRequest("POST", s.URL()+"/api/action", bytes.NewReader(body))
		r.Header.Set("X-App-Token", s.token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) || strings.Contains(w.Body.String(), secret) {
			t.Fatal("sync action failed or exposed key")
		}
		if got.Code != secret || got.Note != "https://destination.example.test" || got.Action != name {
			t.Fatal("profile did not resolve into worker credentials")
		}
	}

}

func TestSyncProfilesAPIOnlyExposesMetadata(t *testing.T) {
	s, err := New(Config{Dir: t.TempDir(), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer s.listener.Close()
	secret := strings.Repeat("s", 32)
	call := func(method, body, capability string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, s.URL()+"/api/sync-profiles", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-App-Token", capability)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	input, _ := json.Marshal(syncProfileInput{Operation: "save", URL: "https://one.example.test", Token: secret})
	if call("POST", string(input), "").Code != 403 {
		t.Fatal("unauthorized save allowed")
	}
	for _, w := range []*httptest.ResponseRecorder{call("POST", string(input), s.token), call("GET", "", s.token)} {
		if w.Code != 200 || strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), `"token"`) || !strings.Contains(w.Body.String(), `"key_saved":true`) {
			t.Fatal("metadata response invalid or leaked key")
		}
	}
	file := filepath.Join(s.config.Dir, "sync-profiles.dpapi")
	if err = os.WriteFile(file, []byte("corrupt-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if call("POST", string(input), s.token).Code != 400 {
		t.Fatal("corrupt credentials silently overwritten")
	}
	raw, _ := os.ReadFile(file)
	if string(raw) != "corrupt-fixture" {
		t.Fatal("corrupt file lost")
	}
}
