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

const LoginPath = "/api/integrations/appleservices/apple-login"

type LoginResult struct {
	SourceID     string `json:"source_id"`
	State        string `json:"state"`
	Challenge    string `json:"challenge,omitempty"`
	Verification string `json:"verification,omitempty"`
}
type loginRequest struct {
	Version   int    `json:"version"`
	SourceID  string `json:"source_id"`
	AppleID   string `json:"apple_id"`
	Operation string `json:"operation"`
	Password  string `json:"password,omitempty"`
	Code      string `json:"otp_code,omitempty"`
	Challenge string `json:"challenge,omitempty"`
}
type CodeProvider func(context.Context) (string, error)

// Login checks the server's own management session, then signs in only if
// required. There are no retry loops, automatic SMS requests or alias mutations.
func Login(ctx context.Context, base, token, appleID, password, code, challenge string, provider CodeProvider) (*LoginResult, error) {
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 115 * time.Second}
	defer client.CloseIdleConnections()
	return loginWithClient(ctx, client, base, token, appleID, password, code, challenge, provider)
}
func loginWithClient(ctx context.Context, client *http.Client, base, token, appleID, password, code, challenge string, provider CodeProvider) (*LoginResult, error) {
	request := loginRequest{Version: 1, SourceID: SourceID(appleID), AppleID: appleID, Operation: "status"}
	if code != "" || challenge != "" {
		if !regexp.MustCompile(`^[0-9]{6}$`).MatchString(code) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(challenge) {
			return nil, errors.New("请输入本次登录的六位验证码")
		}
		request.Operation = "verify"
		request.Code = code
		request.Challenge = challenge
		return loginCall(ctx, client, base, token, request)
	}
	result, err := loginCall(ctx, client, base, token, request)
	if err != nil || result.State == "ready" {
		return result, err
	}
	if password == "" {
		return nil, errors.New("本机未载入账号密码，请重新连接账号后同步")
	}
	request.Operation = "login"
	request.Password = password
	result, err = loginCall(ctx, client, base, token, request)
	request.Password = ""
	if err != nil || result.State == "ready" {
		return result, err
	}
	if provider == nil || result.Verification == "sms" {
		return result, nil
	}
	code, codeErr := provider(ctx)
	if codeErr != nil || !regexp.MustCompile(`^[0-9]{6}$`).MatchString(code) {
		return result, nil
	}
	request.Operation = "verify"
	request.Code = code
	request.Challenge = result.Challenge
	return loginCall(ctx, client, base, token, request)
}
func loginCall(ctx context.Context, client *http.Client, base, token string, payload loginRequest) (*LoginResult, error) {
	endpoint, err := Endpoint(base)
	if err != nil {
		return nil, err
	}
	endpoint = strings.TrimSuffix(endpoint, Path) + LoginPath
	if len(token) < 32 || len(token) > 512 || strings.ContainsAny(token, " \t\r\n") {
		return nil, errors.New("同步密钥无效")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("登录请求编码失败")
	}
	defer clear(raw)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("登录请求无效")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	bounded := *client
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := bounded.Do(req)
	if err != nil {
		return nil, errors.New("服务器登录结果未确认，请稍后检查；不会自动重复登录")
	}
	defer response.Body.Close()
	if response.StatusCode == 404 {
		return nil, errors.New("Web 和 IMAP 已同步；服务器尚未更新自动登录接口")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 16385))
	defer clear(body)
	var wire struct {
		Success bool        `json:"success"`
		Code    string      `json:"code"`
		Data    LoginResult `json:"data"`
	}
	if err != nil || len(body) > 16384 || json.Unmarshal(body, &wire) != nil {
		return nil, errors.New("服务器登录响应无效")
	}
	if response.StatusCode != 200 || !wire.Success {
		message := "服务器新通道认证失败，请检查服务器状态"
		switch wire.Code {
		case "IMPORT_UNAUTHORIZED":
			message = "服务器未接受同步密钥"
		case "OTP_SESSION_EXPIRED":
			message = "服务器验证会话已过期，请重新同步登录"
		case "APPLE_ACCOUNT_AUTH_FAILED":
			message = "Apple 未接受登录凭据，请检查本机账号登录"
		case "APPLE_ACCOUNT_ACTION_REQUIRED":
			message = "Apple 要求在官网完成账号验证"
		case "ACCOUNT_IDENTITY_CONFLICT":
			message = "服务器账号绑定不一致，请先同步该账号"
		case "UPSTREAM_RATE_LIMITED", "IMPORT_RATE_LIMITED":
			message = "认证请求已限流，请稍后手动重试"
		case "IMPORT_BUSY":
			message = "服务器正在处理认证，请稍后重试"
		}
		return nil, errors.New(message)
	}
	result := wire.Data
	validState := result.State == "ready" || (result.State == "login_required" && payload.Operation == "status") || (result.State == "otp_required" && payload.Operation != "status" && regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(result.Challenge))
	if result.SourceID != payload.SourceID || !validState || (result.State != "otp_required" && result.Challenge != "") {
		return nil, errors.New("服务器未确认当前账号的新通道状态")
	}
	return &result, nil
}
