//go:build windows

package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Laky-64/appleservices/internal/control"
	"github.com/Laky-64/appleservices/internal/hmesync"
)

func TestRecoveryOptInIdentityAndPersistence(t *testing.T) {
	s, err := New(Config{Dir: t.TempDir(), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer s.listener.Close()
	a := &Account{ID: "fixture", AppleID: "owner@example.test"}
	s.accounts = []*Account{a}
	cfg, err := s.changeSyncProfiles(syncProfileInput{Operation: "save", URL: "https://fixture.example.test", Token: strings.Repeat("k", 32)})
	if err != nil {
		t.Fatal(err)
	}
	profile := cfg.Profiles[0]
	update := func(enabled bool, address string) int {
		body, _ := json.Marshal(map[string]any{"profile_id": profile.ID, "url": address, "enabled": enabled})
		r := httptest.NewRequest("POST", s.URL()+"/api/sync-recovery?account=fixture", strings.NewReader(string(body)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-App-Token", s.token)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w.Code
	}
	if update(true, "https://other.example.test") != 400 {
		t.Fatal("stale destination accepted")
	}
	if update(true, profile.URL) != 200 {
		t.Fatal("opt in failed")
	}
	restarted := &Server{config: s.config}
	restored, err := restarted.readSyncProfiles()
	if err != nil {
		t.Fatal(err)
	}
	profile = restored.Profiles[0]
	source := hmesync.SourceID(a.AppleID)
	if profile.RecoverySources[a.ID] != source {
		t.Fatal("identity binding did not persist")
	}
	job := hmesync.RecoveryJob{ID: "job1", SourceID: source}
	if _, reserved := s.reserveRecovery(profile, job); !reserved {
		t.Fatal("first job not reserved")
	}
	if outcome, reserved := s.reserveRecovery(profile, job); reserved || outcome != "failed" {
		t.Fatal("duplicate crash replay allowed")
	}
	s.finishRecovery(profile, job, "ready")
	if outcome, reserved := restarted.reserveRecovery(profile, job); reserved || outcome != "ready" {
		t.Fatal("completed job re-executed after restart")
	}
	if _, reserved := s.reserveRecovery(profile, hmesync.RecoveryJob{ID: "job2", SourceID: source}); reserved {
		t.Fatal("distinct jobs bypassed cooldown")
	}
	s.finishRecovery(profile, job, "verification_required")
	if outcome, reserved := s.reserveRecovery(profile, hmesync.RecoveryJob{ID: "job2", SourceID: source}); reserved || outcome != "verification_required" {
		t.Fatal("manual verification was retried")
	}
	if update(false, profile.URL) != 200 {
		t.Fatal("disable failed")
	}
	if _, reserved := s.reserveRecovery(profile, job); reserved {
		t.Fatal("disabled identity accepted")
	}
	if update(true, profile.URL) != 200 {
		t.Fatal("explicit resume failed")
	}
	if _, reserved := s.reserveRecovery(profile, job); !reserved {
		t.Fatal("explicit resume did not clear pause")
	}
	if _, err := s.changeSyncProfiles(syncProfileInput{Operation: "save", ID: profile.ID, URL: "https://new.example.test", Token: strings.Repeat("x", 32)}); err != nil {
		t.Fatal(err)
	}
	changed, _ := s.readSyncProfiles()
	if len(changed.Profiles[0].RecoverySources) != 0 {
		t.Fatal("authorization migrated to different destination")
	}
	if outcome := s.handleRecoveryJob(context.Background(), profile, job); outcome != "local_unavailable" {
		t.Fatal("stale task executed")
	}
}

func TestRecoveryJobCallsOnlyBoundWorkerAndDeduplicates(t *testing.T) {
	for _, state := range []string{"ready", "otp_required", "wrong_identity", "web_ready"} {
		t.Run(state, func(t *testing.T) {
			dir := t.TempDir()
			executable, _ := os.Executable()
			s, err := New(Config{Dir: dir, Worker: executable, Port: 0})
			if err != nil {
				t.Fatal(err)
			}
			defer s.listener.Close()
			a := &Account{ID: "fixture", AppleID: "owner@example.test", Dir: dir}
			s.accounts = []*Account{a}
			source := hmesync.SourceID(a.AppleID)
			cfg, err := s.changeSyncProfiles(syncProfileInput{Operation: "save", URL: "https://destination.example.test", Token: strings.Repeat("k", 32)})
			if err != nil {
				t.Fatal(err)
			}
			cfg.Profiles[0].RecoverySources = map[string]string{a.ID: source}
			if err := s.writeSyncProfiles(cfg); err != nil {
				t.Fatal(err)
			}
			p := cfg.Profiles[0]
			var calls atomic.Int32
			worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var action control.Action
				wantAction := "hme-server-login"
				if state == "web_ready" {
					wantAction = "hme-server-web-recover"
				}
				if r.URL.Path != "/action" || json.NewDecoder(r.Body).Decode(&action) != nil || action.Action != wantAction || action.Note != p.URL || action.Code != p.Token {
					t.Error("unexpected worker action")
				}
				if state == "web_ready" {
					var payload hmesync.WebRecoveryPayload
					if json.Unmarshal([]byte(action.Label), &payload) != nil || payload.SourceID != source || payload.ClientID != p.ID || payload.JobID != "job1" {
						t.Error("web task lost its lease binding")
					}
				}
				result := &hmesync.LoginResult{State: state, SourceID: source}
				if state == "web_ready" {
					result.State = "ready"
				}
				if state == "wrong_identity" {
					result.State = "ready"
					result.SourceID = "other"
				}
				json.NewEncoder(w).Encode(control.Result{OK: true, ServerLogin: result})
			}))
			defer worker.Close()
			for name, value := range map[string]any{
				"control.json": control.Record{PID: os.Getpid(), URL: worker.URL, Token: strings.Repeat("a", 64)},
				"runtime.json": Status{PID: os.Getpid(), Running: true, Authenticated: true},
			} {
				raw, _ := json.Marshal(value)
				if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			job := hmesync.RecoveryJob{ID: "job1", SourceID: source}
			if state == "web_ready" {
				job.Kind = "web_session"
			}
			foreign := job
			foreign.SourceID = "other"
			if s.handleRecoveryJob(context.Background(), p, foreign) != "local_unavailable" || calls.Load() != 0 {
				t.Fatal("foreign account invoked worker")
			}
			expected := "ready"
			if state == "otp_required" {
				expected = "verification_required"
			}
			if state == "wrong_identity" {
				expected = "failed"
			}
			for i := 0; i < 2; i++ {
				if got := s.handleRecoveryJob(context.Background(), p, job); got != expected {
					t.Fatalf("outcome %s, want %s", got, expected)
				}
			}
			if calls.Load() != 1 {
				t.Fatal("job replay submitted another login")
			}
		})
	}
}

func TestRecoveryCanceledAndNoProfilesNeverContactsWorker(t *testing.T) {
	s := &Server{config: Config{Dir: t.TempDir()}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.runRecoveryListeners(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("listener did not stop")
	}
	if waitRecovery(ctx, time.Hour) {
		t.Fatal("cancellation ignored")
	}
}

func TestRecoveryStatusDoesNotLeakAcrossDisabledAccounts(t *testing.T) {
	s, err := New(Config{Dir: t.TempDir(), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer s.listener.Close()
	s.accounts = []*Account{{ID: "enabled", AppleID: "one@example.test"}, {ID: "disabled", AppleID: "two@example.test"}}
	cfg, err := s.changeSyncProfiles(syncProfileInput{Operation: "save", URL: "https://fixture.example.test", Token: strings.Repeat("k", 32)})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Profiles[0].RecoverySources = map[string]string{"enabled": hmesync.SourceID("one@example.test")}
	if err = s.writeSyncProfiles(cfg); err != nil {
		t.Fatal(err)
	}
	s.setRecoveryStatus(cfg.Profiles[0].ID, "listening", "waiting")
	for _, id := range []string{"enabled", "disabled"} {
		r := httptest.NewRequest("GET", s.URL()+"/api/sync-profiles?account="+id, nil)
		r.Header.Set("X-App-Token", s.token)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		var out struct {
			Profiles []syncProfilePublic `json:"profiles"`
		}
		if json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Profiles) != 1 {
			t.Fatal("invalid profile response")
		}
	if (out.Profiles[0].RecoveryStatus != nil) != (id == "enabled") {
			t.Fatal("another account's listener was shown for disabled account")
		}
		if id == "enabled" && (out.Profiles[0].RecoveryEnabled == nil || !*out.Profiles[0].RecoveryEnabled) {
			t.Fatal("enabled account missing recovery_enabled=true")
		}
		if id == "disabled" && out.Profiles[0].RecoveryEnabled != nil {
			t.Fatal("unconfigured account should omit recovery_enabled so UI defaults to opt-in")
		}
	}
	// Explicit opt-out persists recovery_enabled=false while unconfigured accounts remain omitted.
	body, _ := json.Marshal(map[string]any{"profile_id": cfg.Profiles[0].ID, "url": cfg.Profiles[0].URL, "enabled": false})
	req := httptest.NewRequest("POST", s.URL()+"/api/sync-recovery?account=disabled", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-App-Token", s.token)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal("explicit disable failed")
	}
	req = httptest.NewRequest("GET", s.URL()+"/api/sync-profiles?account=disabled", nil)
	req.Header.Set("X-App-Token", s.token)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, req)
	var out struct {
		Profiles []syncProfilePublic `json:"profiles"`
	}
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Profiles) != 1 || out.Profiles[0].RecoveryEnabled == nil || *out.Profiles[0].RecoveryEnabled {
		t.Fatal("explicit opt-out did not return recovery_enabled=false")
	}
	if _, err := s.changeSyncProfiles(syncProfileInput{Operation: "save", ID: cfg.Profiles[0].ID, URL: "https://changed.example.test", Token: strings.Repeat("x", 32)}); err != nil {
		t.Fatal(err)
	}
	changed, _ := s.readSyncProfiles()
	if len(changed.Profiles[0].RecoveryDisabled) != 0 {
		t.Fatal("explicit opt-out leaked across changed destination URL")
	}
}
