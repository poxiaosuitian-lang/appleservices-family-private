package hmesync

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"time"
)

type WebRecoveryPayload struct {
	Version  int               `json:"version"`
	ClientID string            `json:"client_id"`
	JobID    string            `json:"job_id"`
	Lease    string            `json:"lease"`
	SourceID string            `json:"source_id"`
	AppleID  string            `json:"apple_id"`
	Host     string            `json:"host"`
	Cookies  map[string]string `json:"cookies"`
}

func RefreshWebSession(ctx context.Context, base, token string, payload WebRecoveryPayload) (*LoginResult, error) {
	client := &RecoveryClient{&http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 75 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	defer client.Close()
	return client.refreshWebSession(ctx, base, token, payload)
}
func (c *RecoveryClient) refreshWebSession(ctx context.Context, base, token string, payload WebRecoveryPayload) (*LoginResult, error) {
	if payload.Version != 1 || payload.SourceID != SourceID(payload.AppleID) || payload.AppleID == "" || len(payload.Cookies) == 0 || (payload.Host != "icloud.com" && payload.Host != "icloud.com.cn") || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(payload.ClientID) || !regexp.MustCompile(`^job_[A-Za-z0-9_-]{1,124}$`).MatchString(payload.JobID) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(payload.Lease) {
		return nil, errors.New("Cookie 恢复任务绑定无效")
	}
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			SourceID string `json:"source_id"`
			State    string `json:"state"`
			Verified bool   `json:"web_verified"`
		} `json:"data"`
	}
	if err := c.call(ctx, base, token, "web-session", payload, &response); err != nil {
		return nil, err
	}
	if !response.Success || response.Data.SourceID != payload.SourceID || response.Data.State != "ready" || !response.Data.Verified {
		return nil, errors.New("服务器未确认 Web 会话恢复")
	}
	return &LoginResult{SourceID: payload.SourceID, State: "ready"}, nil
}
