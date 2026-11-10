package icloud

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

var ErrPrimaryMailAuthentication = errors.New("iCloud 邮箱需要网页会话验证，请重新连接账号或在 iCloud.com/mail 完成验证")
var ErrPrimaryMailUnconfirmed = errors.New("邮箱创建结果尚未确认；已暂停重复创建，请稍后检查邮箱状态")

func primaryMailLoginError(status int, data []byte) error {
	var result struct {
		AuthType      string `json:"authType"`
		ServiceErrors []struct {
			Code string `json:"code"`
		} `json:"serviceErrors"`
	}
	_ = json.Unmarshal(data, &result)
	if status == http.StatusPreconditionFailed && result.AuthType == "sa" {
		return errors.New("Apple 要求安全提示问题验证（HTTP 412）；此账号需补完安全验证或启用双重认证后，再重新连接继续邮箱任务")
	}
	code := ""
	if len(result.ServiceErrors) > 0 && regexp.MustCompile(`^-?[0-9]{1,8}$`).MatchString(result.ServiceErrors[0].Code) {
		code = "，代码 " + result.ServiceErrors[0].Code
	}
	return fmt.Errorf("Apple 网页登录未完成（HTTP %d%s）；请在 iCloud.com/mail 核实账号验证要求后重新连接", status, code)
}

// PrimaryMailClient uses the same SRP transport as HME, with a separate,
// account-bound session and no iCloud+ or family-membership prerequisite.
type PrimaryMailClient struct{ web *HideMyEmailClient }

func (c *PrimaryMailClient) SetSecurityQuestionsProvider(provider SecurityQuestionsProvider) {
	c.web.SetSecurityQuestionsProvider(provider)
}

type PrimaryMailInfo struct {
	Exists bool   `json:"exists"`
	Email  string `json:"email,omitempty"`
}

func NewPrimaryMailClient(dir, appleID string) (*PrimaryMailClient, error) {
	w, err := NewHideMyEmailClient(dir, appleID)
	if err != nil {
		return nil, err
	}
	w.serviceName = "mccgateway"
	w.sessionPath = filepath.Join(dir, "primary-mail-session.json")
	// Do not accidentally adopt the HME session loaded by its constructor.
	w.session = hmeSession{}
	w.resetHTTP()
	_ = w.loadSession()
	return &PrimaryMailClient{web: w}, nil
}

func (c *PrimaryMailClient) Authenticate(ctx context.Context, password string, code func(context.Context) (string, error)) error {
	if password == "" {
		return ErrPrimaryMailAuthentication
	}
	return c.web.Authenticate(ctx, password, code)
}

// Check follows Mail's own provisioning test: the authenticated setup response
// must advertise the mail service. A missing/invalid response is never treated
// as permission to create. Apple ID login addresses are not mailbox addresses.
func (c *PrimaryMailClient) Check(ctx context.Context) (*PrimaryMailInfo, error) {
	w := c.web
	if w.session.DSID == "" {
		return nil, ErrPrimaryMailAuthentication
	}
	base := w.setupBase
	if strings.EqualFold(w.session.AccountCountry, "CHN") || strings.EqualFold(w.session.AccountCountry, "CN") {
		base = "https://setup.icloud.com.cn/setup/ws/1"
	}
	var result hmeAccountLoginResult
	if err := c.request(ctx, base+"/validate", map[string]any{}, &result); err != nil {
		return nil, err
	}
	if result.DSInfo.DSID != w.session.DSID || result.Webservices == nil {
		return nil, errors.New("Apple 返回的邮箱状态不完整或账号不匹配，未创建邮箱")
	}
	if result.TermsUpdateNeeded {
		return nil, ErrHMEWebRepairRequired
	}
	if result.Webservices["mail"].URL != "" {
		return &PrimaryMailInfo{Exists: true}, nil
	}
	gateway := result.Webservices["mccgateway"].URL
	if !appleMailServiceURL(gateway) {
		return nil, errors.New("Apple 未提供邮箱创建服务，请在 iCloud.com/mail 检查账号资格")
	}
	w.session.ServiceURL = strings.TrimRight(gateway, "/")
	if err := w.saveSession(); err != nil {
		return nil, errors.New("无法保存邮箱网页会话")
	}
	return &PrimaryMailInfo{}, nil
}

func appleMailServiceURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.User == nil && u.RawQuery == "" && u.Fragment == "" &&
		(strings.HasSuffix(u.Hostname(), ".icloud.com") || strings.HasSuffix(u.Hostname(), ".icloud.com.cn"))
}

// Ensure checks first, checks name availability, persists a submission marker,
// then makes at most ONE activation request. Even a lost response is reconciled
// read-only; callers must retain the marker across process restarts.
func (c *PrimaryMailClient) Ensure(ctx context.Context, allowCreate bool, beforeCreate func(string) error) (*PrimaryMailInfo, error) {
	info, err := c.Check(ctx)
	if err != nil || info.Exists {
		return info, err
	}
	if !allowCreate {
		return info, ErrPrimaryMailUnconfirmed
	}
	for _, email := range primaryMailCandidates(c.web.appleID) {
		var available struct {
			Email     string `json:"email"`
			Available *bool  `json:"available"`
		}
		body := map[string]any{"email": email}
		if err := c.request(ctx, c.web.session.ServiceURL+"/mailacct/v1/web/emailAvailability", body, &available); err != nil {
			return nil, err
		}
		if available.Available == nil || !strings.EqualFold(available.Email, email) {
			return nil, errors.New("Apple 未返回有效的邮箱名称可用性结果")
		}
		if !*available.Available {
			continue
		}
		if beforeCreate == nil {
			return nil, errors.New("缺少邮箱创建持久化检查点，未提交")
		}
		if err := beforeCreate(email); err != nil {
			return nil, err
		}
		var created struct {
			Email string `json:"email"`
		}
		createErr := c.request(ctx, c.web.session.ServiceURL+"/mailacct/v1/web/activateEmail", body, &created)
		// Read back even on an error: a timeout does not imply Apple did nothing.
		verified, verifyErr := c.Check(ctx)
		if verifyErr == nil && verified.Exists {
			if createErr == nil && strings.EqualFold(created.Email, email) {
				verified.Email = email
			}
			return verified, nil
		}
		if createErr != nil {
			return nil, fmt.Errorf("%w：%v", ErrPrimaryMailUnconfirmed, createErr)
		}
		return nil, ErrPrimaryMailUnconfirmed
	}
	return nil, errors.New("默认邮箱名及备用名称均已被占用，请在 iCloud.com/mail 自选名称后检查状态")
}

var nonMailName = regexp.MustCompile(`[^a-z0-9]`)

func primaryMailCandidates(appleID string) []string {
	id := strings.ToLower(strings.TrimSpace(appleID))
	name := nonMailName.ReplaceAllString(strings.SplitN(id, "@", 2)[0], "")
	if len(name) == 0 || name[0] < 'a' || name[0] > 'z' {
		name = "mail" + name
	}
	if len(name) < 3 {
		name += "mail"
	}
	if len(name) > 20 {
		name = name[:20]
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(id)))
	candidates := []string{name + "@icloud.com"}
	for _, size := range []int{4, 6, 8} {
		prefix := name
		if len(prefix) > 20-size {
			prefix = prefix[:20-size]
		}
		candidates = append(candidates, prefix+digest[:size]+"@icloud.com")
	}
	return candidates
}

// Only known public validation codes and short numeric codes may be shown.
// Unknown response fields can contain account data and must not reach the UI.
func primaryMailServiceError(path string, status int, code string) error {
	stage := "邮箱服务请求"
	switch path {
	case "/setup/ws/1/validate":
		stage = "邮箱状态查询"
	case "/mailacct/v1/web/emailAvailability":
		stage = "邮箱名称检查"
	case "/mailacct/v1/web/activateEmail":
		stage = "邮箱创建"
	}
	if status == 401 || status == 421 || code == "-9008" {
		return fmt.Errorf("%s未完成（HTTP %d）：%w", stage, status, ErrPrimaryMailAuthentication)
	}
	detail := ""
	switch code {
	case "MIN_LENGTH_VIOLATION", "MAX_LENGTH_VIOLATION":
		detail = "，邮箱名称须为 3–20 个字符，代码 " + code
	case "ALLOWED_CHARACTER_VIOLATION", "START_WITH_LETTER_VIOLATION", "END_WITH_LETTER_OR_NUMBER_VIOLATION", "FORMAT_VIOLATION", "CONSECUTIVE_DOTS_VIOLATION", "CONSECUTIVE_UNDERSCORE_VIOLATION":
		detail = "，邮箱名称不符合要求，代码 " + code
	default:
		if regexp.MustCompile(`^-?[0-9]{1,8}$`).MatchString(code) {
			detail = "，代码 " + code
		}
	}
	return fmt.Errorf("Apple 未接受%s（HTTP %d%s）", stage, status, detail)
}

// Errors intentionally exclude raw responses, URLs, cookies and credentials.
func (c *PrimaryMailClient) request(ctx context.Context, endpoint string, body any, out any) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return errors.New("无效的邮箱接口地址")
	}
	q := u.Query()
	q.Set("clientBuildNumber", hmeBuildNumber)
	q.Set("clientId", c.web.session.ClientID)
	q.Set("dsid", c.web.session.DSID)
	u.RawQuery = q.Encode()
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(b))
	if err != nil {
		return errors.New("无法构造邮箱请求")
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://www.icloud.com")
	r.Header.Set("Referer", "https://www.icloud.com/")
	r.Header.Set("User-Agent", hmeWebUserAgent)
	resp, err := c.web.http.Do(r)
	if err != nil {
		return errors.New("邮箱服务网络请求未完成")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 421 {
		return primaryMailServiceError(u.Path, resp.StatusCode, "")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return errors.New("邮箱服务响应读取失败")
	}
	var failure struct {
		Code string `json:"errorCode"`
	}
	_ = json.Unmarshal(data, &failure)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || failure.Code != "" {
		return primaryMailServiceError(u.Path, resp.StatusCode, failure.Code)
	}
	if json.Unmarshal(data, out) != nil {
		return errors.New("Apple 邮箱响应格式不完整")
	}
	return nil
}
