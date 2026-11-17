package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Laky-64/appleservices/internal/control"
	"github.com/Laky-64/appleservices/internal/hmesync"
)

// Sources bind opt-in to both the account ID and its verified login identity.
// They live inside the DPAPI-encrypted server profile, never browser storage.
type recoveryAttempt struct {
	JobID       string    `json:"job_id"`
	Outcome     string    `json:"outcome"`
	NextAllowed time.Time `json:"next_allowed"`
}
type recoveryStatus struct {
	State     string    `json:"state"`
	Message   string    `json:"message"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Server) setRecoveryStatus(profileID, state, message string) {
	s.recoveryStatus.Store(profileID, recoveryStatus{state, message, time.Now().UTC()})
}
func (s *Server) serveSyncRecovery(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ProfileID string `json:"profile_id"`
		URL       string `json:"url"`
		Enabled   bool   `json:"enabled"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	account := s.lookup(r.URL.Query().Get("account"))
	if account == nil {
		http.Error(w, `{"message":"账号不存在"}`, 400)
		return
	}
	s.syncProfilesMu.Lock()
	defer s.syncProfilesMu.Unlock()
	value, err := s.readSyncProfiles()
	if err != nil {
		http.Error(w, `{"message":"无法读取服务器配置"}`, 400)
		return
	}
	for i := range value.Profiles {
		p := &value.Profiles[i]
		if p.ID != input.ProfileID || p.URL != input.URL {
			continue
		}
		if p.RecoverySources == nil {
			p.RecoverySources = map[string]string{}
		}
		if input.Enabled {
			p.RecoverySources[account.ID] = hmesync.SourceID(account.AppleID)
			delete(p.RecoveryHandled, hmesync.SourceID(account.AppleID))
			delete(p.RecoveryHandled, hmesync.SourceID(account.AppleID)+":web")
		} else {
			delete(p.RecoverySources, account.ID)
		}
		p.RecoveryRevision++
		if s.writeSyncProfiles(value) != nil {
			http.Error(w, `{"message":"无法保存按需恢复配置"}`, 400)
			return
		}
		json.NewEncoder(w).Encode(map[string]bool{"ok": true, "enabled": input.Enabled})
		return
	}
	http.Error(w, `{"message":"服务器配置已更改，请重新打开窗口"}`, 400)
}

// The timer only reconciles local configuration and long-lived wait requests.
// No timer in this service performs an Apple status check or sign-in.
func (s *Server) runRecoveryListeners(ctx context.Context) {
	type running struct {
		signature string
		cancel    context.CancelFunc
		done      chan struct{}
	}
	active := map[string]running{}
	defer func() {
		for _, v := range active {
			v.cancel()
		}
	}()
	reconcile := func() {
		s.syncProfilesMu.Lock()
		config, err := s.readSyncProfiles()
		s.syncProfilesMu.Unlock()
		if err != nil {
			return
		}
		wanted := map[string]syncProfile{}
		for _, p := range config.Profiles {
			if len(p.RecoverySources) > 0 {
				wanted[p.ID] = p
			}
		}
		for id, v := range active {
			p, ok := wanted[id]
			p.RecoveryHandled = nil
			raw, _ := json.Marshal(p)
			if !ok || v.signature != string(raw) {
				v.cancel()
				select {
				case <-v.done:
					delete(active, id)
				default:
				}
				continue
			}
		}
		for id, p := range wanted {
			if _, ok := active[id]; ok {
				continue
			}
			p.RecoveryHandled = nil
			raw, _ := json.Marshal(p)
			child, cancel := context.WithCancel(ctx)
			done := make(chan struct{})
			active[id] = running{string(raw), cancel, done}
			go func(p syncProfile) { defer close(done); s.listenRecovery(child, p) }(p)
		}
	}
	reconcile()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconcile()
		}
	}
}
func (s *Server) listenRecovery(ctx context.Context, p syncProfile) {
	client := hmesync.NewRecoveryClient()
	defer client.Close()
	sources := make([]string, 0, len(p.RecoverySources))
	for _, source := range p.RecoverySources {
		sources = append(sources, source)
	}
	failures := 0
	for ctx.Err() == nil {
		s.setRecoveryStatus(p.ID, "listening", "等待服务器登录恢复任务")
		job, err := client.Wait(ctx, p.URL, p.Token, p.ID, sources)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			failures++
			s.setRecoveryStatus(p.ID, "disconnected", err.Error())
			// Configuration errors require a changed key/binding, not retries.
			// The listener supervisor restarts when that profile changes.
			if errors.Is(err, hmesync.ErrSyncUnauthorized) || errors.Is(err, hmesync.ErrRecoveryBinding) {
				return
			}
			if !waitRecovery(ctx, time.Duration(1<<min(failures, 6))*time.Second) {
				return
			}
			continue
		}
		failures = 0
		if job == nil {
			if !waitRecovery(ctx, time.Second) {
				return
			}
			continue
		}
		s.setRecoveryStatus(p.ID, "recovering", "正在响应服务器请求，恢复 Apple 登录")
		outcome := s.handleRecoveryJob(ctx, p, *job)
		if ctx.Err() != nil {
			return
		}
		ackCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = client.Complete(ackCtx, p.URL, p.Token, p.ID, *job, outcome)
		cancel()
		if err != nil {
			s.setRecoveryStatus(p.ID, "disconnected", "恢复结果未获服务器确认，不重复提交登录")
			if !waitRecovery(ctx, 30*time.Second) {
				return
			}
			continue
		}
		message := "服务器登录已恢复"
		switch outcome {
		case "verification_required":
			message = "Apple 要求人工验证，请打开同步窗口完成验证"
		case "failed":
			message = "登录恢复失败，等待服务器处理"
		case "local_unavailable":
			message = "本机账号离线或正在处理其他请求"
		}
		s.setRecoveryStatus(p.ID, outcome, message)
		if !waitRecovery(ctx, time.Second) {
			return
		}
	}
}
func waitRecovery(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (s *Server) handleRecoveryJob(ctx context.Context, p syncProfile, job hmesync.RecoveryJob) string {
	// Serialize assisted logins with manual sync. Never interrupt an active OTP.
	if !s.syncActionMu.TryLock() {
		return "local_unavailable"
	}
	defer s.syncActionMu.Unlock()
	var account *Account
	for id, source := range p.RecoverySources {
		if source == job.SourceID {
			a := s.lookup(id)
			if a != nil && hmesync.SourceID(a.AppleID) == source {
				account = a
				break
			}
		}
	}
	if account == nil {
		return "local_unavailable"
	}
	s.syncProfilesMu.Lock()
	latest, err := s.readSyncProfiles()
	s.syncProfilesMu.Unlock()
	permitted := false
	if err == nil {
		for _, current := range latest.Profiles {
			if current.ID == p.ID && current.URL == p.URL && current.Token == p.Token && current.RecoverySources[account.ID] == job.SourceID {
				permitted = true
			}
		}
	}
	if !permitted || ctx.Err() != nil {
		return "local_unavailable"
	}
	status := s.accountStatus(account)
	if !status.Running || !status.Authenticated {
		return "local_unavailable"
	}
	previous, reserved := s.reserveRecovery(p, job)
	if !reserved {
		return previous
	}
	outcome := "failed"
	defer func() { s.finishRecovery(p, job, outcome) }()
	actionCtx, cancel := context.WithTimeout(ctx, 185*time.Second)
	defer cancel()
	var result control.Result
	action := control.Action{Action: "hme-server-login", Note: p.URL, Code: p.Token}
	if job.Kind == "web_session" {
		metadata, _ := json.Marshal(hmesync.WebRecoveryPayload{Version: 1, ClientID: p.ID, JobID: job.ID, Lease: job.Lease, SourceID: job.SourceID})
		action.Action = "hme-server-web-recover"
		action.Label = string(metadata)
	}
	if s.callFor(actionCtx, account.Dir, "/action", action, &result) != nil {
		return "failed"
	}
	if result.OK && result.ServerLogin != nil && result.ServerLogin.State == "ready" && result.ServerLogin.SourceID == job.SourceID {
		outcome = "ready"
		return outcome
	}
	if result.Stage == "web-verification-required" || result.ServerLogin != nil && result.ServerLogin.State == "otp_required" {
		outcome = "verification_required"
		return outcome
	}
	return "failed"
}

func (s *Server) reserveRecovery(p syncProfile, job hmesync.RecoveryJob) (string, bool) {
	s.syncProfilesMu.Lock()
	defer s.syncProfilesMu.Unlock()
	cfg, err := s.readSyncProfiles()
	if err != nil {
		return "failed", false
	}
	for i := range cfg.Profiles {
		current := &cfg.Profiles[i]
		if current.ID != p.ID || current.URL != p.URL || current.Token != p.Token {
			continue
		}
		allowed := false
		for _, source := range current.RecoverySources {
			if source == job.SourceID {
				allowed = true
			}
		}
		if !allowed {
			return "local_unavailable", false
		}
		if current.RecoveryHandled == nil {
			current.RecoveryHandled = map[string]recoveryAttempt{}
		}
		key := recoveryAttemptKey(job)
		previous := current.RecoveryHandled[key]
		if previous.JobID == job.ID {
			return previous.Outcome, false
		}
		if previous.Outcome == "verification_required" {
			return "verification_required", false
		}
		if time.Now().Before(previous.NextAllowed) {
			return "local_unavailable", false
		}
		current.RecoveryHandled[key] = recoveryAttempt{job.ID, "failed", time.Now().Add(5 * time.Minute)}
		if s.writeSyncProfiles(cfg) != nil {
			return "failed", false
		}
		return "", true
	}
	return "local_unavailable", false
}
func (s *Server) finishRecovery(p syncProfile, job hmesync.RecoveryJob, outcome string) {
	s.syncProfilesMu.Lock()
	defer s.syncProfilesMu.Unlock()
	cfg, err := s.readSyncProfiles()
	if err != nil {
		return
	}
	for i := range cfg.Profiles {
		current := &cfg.Profiles[i]
		if current.ID != p.ID || current.URL != p.URL || current.Token != p.Token {
			continue
		}
		key := recoveryAttemptKey(job)
		attempt := current.RecoveryHandled[key]
		if attempt.JobID != job.ID {
			return
		}
		attempt.Outcome = outcome
		current.RecoveryHandled[key] = attempt
		if s.writeSyncProfiles(cfg) != nil {
			s.setRecoveryStatus(p.ID, "failed", "恢复结果保存失败，请检查本机配置")
		}
		return
	}
}

func recoveryAttemptKey(job hmesync.RecoveryJob) string {
	if job.Kind == "web_session" {
		return job.SourceID + ":web"
	}
	return job.SourceID
}
