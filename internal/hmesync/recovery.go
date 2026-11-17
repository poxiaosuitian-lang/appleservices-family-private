package hmesync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const RecoveryPath = "/api/integrations/appleservices/recovery/"

type RecoveryJob struct {
	ID       string `json:"id"`
	Lease    string `json:"lease"`
	SourceID string `json:"source_id"`
	Kind     string `json:"kind"`
}
type RecoveryRequest struct {
	Kinds    []string `json:"kinds,omitempty"`
	Version  int      `json:"version"`
	ClientID string   `json:"client_id"`
	Sources  []string `json:"sources,omitempty"`
	JobID    string   `json:"job_id,omitempty"`
	Lease    string   `json:"lease,omitempty"`
	Outcome  string   `json:"outcome,omitempty"`
}
type RecoveryClient struct{ http *http.Client }

func NewRecoveryClient() *RecoveryClient {
	return &RecoveryClient{&http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 35 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *RecoveryClient) Close() { c.http.CloseIdleConnections() }
func (c *RecoveryClient) Wait(ctx context.Context, base, token, clientID string, sources []string) (*RecoveryJob, error) {
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Job *RecoveryJob `json:"job"`
		} `json:"data"`
	}
	if err := c.call(ctx, base, token, "wait", RecoveryRequest{Version: 1, ClientID: clientID, Sources: sources, Kinds: []string{"apple_login", "web_session"}}, &result); err != nil {
		return nil, err
	}
	if !result.Success {
		return nil, errors.New("服务器未接受登录恢复监听")
	}
	job := result.Data.Job
	if job == nil {
		return nil, nil
	}
	allowed := false
	for _, source := range sources {
		if source == job.SourceID {
			allowed = true
			break
		}
	}
	if !allowed || (job.Kind != "apple_login" && job.Kind != "web_session") || !regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`).MatchString(job.ID) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(job.Lease) {
		return nil, errors.New("服务器返回了不匹配的登录恢复任务")
	}
	return job, nil
}
func (c *RecoveryClient) Complete(ctx context.Context, base, token, clientID string, job RecoveryJob, outcome string) error {
	switch outcome {
	case "ready", "verification_required", "failed", "local_unavailable":
	default:
		return errors.New("无效恢复结果")
	}
	var result struct {
		Success bool `json:"success"`
	}
	if err := c.call(ctx, base, token, "complete", RecoveryRequest{Version: 1, ClientID: clientID, JobID: job.ID, Lease: job.Lease, Outcome: outcome}, &result); err != nil {
		return err
	}
	if !result.Success {
		return errors.New("服务器未确认登录恢复结果")
	}
	return nil
}
func (c *RecoveryClient) call(ctx context.Context, base, token, operation string, input any, output any) error {
	endpoint, err := Endpoint(base)
	if err != nil {
		return err
	}
	if len(token) < 6 || len(token) > 512 || strings.ContainsAny(token, " \t\r\n") {
		return errors.New("同步密钥无效")
	}
	body, err := json.Marshal(input)
	if err != nil {
		return errors.New("恢复请求无效")
	}
	defer clear(body)
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimSuffix(endpoint, Path)+RecoveryPath+operation, bytes.NewReader(body))
	if err != nil {
		return errors.New("恢复请求无效")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New("服务器任务连接中断，稍后重新连接")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return ErrSyncUnauthorized
	}
	if resp.StatusCode == 409 && operation == "wait" {
		return ErrRecoveryBinding
	}
	if resp.StatusCode == 429 {
		return errors.New("服务器已限流（HTTP 429），稍后恢复连接")
	}
	if resp.StatusCode == 404 {
		return errors.New("服务器尚未部署按需登录恢复接口")
	}
	if resp.StatusCode != 200 {
		return errors.New("服务器暂未接受登录恢复请求")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16385))
	defer clear(raw)
	if err != nil || len(raw) > 16384 || json.Unmarshal(raw, output) != nil {
		return errors.New("恢复响应无效")
	}
	return nil
}
