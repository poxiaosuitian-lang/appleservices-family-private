// Package hmesync transfers credentials only to an explicitly selected server.
package hmesync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const Path = "/api/integrations/appleservices/accounts"

type Payload struct {
	Version     int               `json:"version"`
	SourceID    string            `json:"source_id"`
	AppleID     string            `json:"apple_id"`
	Name        string            `json:"name"`
	ICloudEmail string            `json:"icloud_email"`
	Host        string            `json:"host"`
	Cookies     map[string]string `json:"cookies"`
	AppPassword string            `json:"app_password"`
}

func SourceID(appleID string) string {
	digest := sha256.Sum256([]byte("appleservices-hme-v1\x00" + strings.ToLower(strings.TrimSpace(appleID))))
	return "appleservices-" + hex.EncodeToString(digest[:])
}

func Endpoint(base string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("请填写有效的 HTTPS 服务器地址")
	}
	u.Path = strings.TrimRight(u.Path, "/") + Path
	u.RawPath = ""
	return u.String(), nil
}

// Import never redirects or retries a credential-bearing POST. All messages are
// local constants; an untrusted server response cannot echo credentials to UI.
func Import(ctx context.Context, base, token string, payload Payload) (string, error) {
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 110 * time.Second}
	defer client.CloseIdleConnections()
	return importWithClient(ctx, base, token, payload, client)
}

func importWithClient(ctx context.Context, base, token string, payload Payload, client *http.Client) (string, error) {
	endpoint, err := Endpoint(base)
	if err != nil {
		return "", err
	}
	if len(token) < 32 || len(token) > 512 || strings.ContainsAny(token, " \t\r\n") {
		return "", errors.New("同步密钥至少 32 位且不能包含空白")
	}
	if payload.Version != 1 || payload.SourceID != SourceID(payload.AppleID) || payload.AppleID == "" || payload.ICloudEmail == "" || payload.AppPassword == "" || len(payload.Cookies) == 0 {
		return "", errors.New("账号同步资料不完整")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", errors.New("同步资料编码失败")
	}
	defer clear(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", errors.New("同步请求无效")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	bounded := *client
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := bounded.Do(req)
	if err != nil {
		return "", errors.New("未收到同步结果，请先在服务器核对账号；不会自动重试")
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 401, 403:
		return "", errors.New("服务器未接受同步密钥，请使用专用导入密钥")
	case 404:
		return "", errors.New("服务器尚未安装账号对接接口，请先执行服务器改造提示词")
	case 409:
		return "", errors.New("服务器账号绑定冲突，请核对已有账号")
	case 422:
		return "", errors.New("服务器未通过账号会话或 IMAP 收信验证，未完成导入")
	}
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return "", errors.New("服务器未确认导入成功，请核对服务器账号；不会自动重试")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	defer clear(raw)
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			ID           string `json:"id"`
			SourceID     string `json:"source_id"`
			Status       string `json:"status"`
			HMEVerified  bool   `json:"hme_verified"`
			IMAPVerified bool   `json:"imap_verified"`
		} `json:"data"`
	}
	if err != nil || len(raw) > 65536 || json.Unmarshal(raw, &result) != nil || !result.Success || result.Data.SourceID != payload.SourceID || result.Data.Status != "active" || !result.Data.HMEVerified || !result.Data.IMAPVerified || !regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`).MatchString(result.Data.ID) {
		return "", errors.New("服务器尚未确认隐藏邮箱及收信均可用，请核对导入结果")
	}
	return result.Data.ID, nil
}
