package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Laky-64/appleservices/icloud"
	"github.com/Laky-64/appleservices/internal/apppassword"
	"github.com/Laky-64/appleservices/internal/control"
)

const appPasswordLabel = "icloud-hme"

func automaticAppPasswordEligible(ctx context.Context, dir string, source any) (bool, error) {
	if requireHMEFamilyMember(dir, time.Now().UTC()) == nil {
		return true, nil
	}
	reader, ok := source.(interface {
		StorageInfo(context.Context) (*icloud.StorageInfo, error)
	})
	if !ok {
		return false, errors.New("storage query unavailable")
	}
	quota, err := reader.StorageInfo(ctx)
	return err == nil && quota != nil && quota.PaidBytes > 0, err
}

type appPasswordVault interface {
	LoadAppPassword(string) (*appPasswordRecord, error)
	SaveAppPassword(string, *appPasswordRecord) error
	CheckAppPasswordStorage(string) error
}

type appPasswordPortal interface {
	Resume(context.Context) error
	Authenticate(context.Context, string, func(context.Context) (string, error)) error
	ListAppSpecificPasswords(context.Context) ([]icloud.AppSpecificPassword, error)
	CreateAppSpecificPassword(context.Context, string, string) (*icloud.AppSpecificPassword, error)
}

// automaticEligibility limits unsolicited background provisioning only. Explicit
// configuration passes nil: an app password does not require a subscription.
func appPasswordAction(ctx context.Context, dir, appleID, password, manual string, vault appPasswordVault, automaticEligibility func(context.Context) (bool, error), portal func() (appPasswordPortal, error), code func(context.Context) (string, error)) control.Result {
	preserveUncertain := false
	finish := func(state, message string) control.Result {
		if preserveUncertain && state != "ready" {
			state = "unknown"
		}
		status := &apppassword.Status{State: state, Message: message, Label: appPasswordLabel}
		if apppassword.Save(dir, appleID, status) != nil {
			return control.Result{Message: "应用专用密码状态保存失败，请检查账号目录", Stage: "app-password-state"}
		}
		return control.Result{OK: state == "ready", Message: message, Stage: "app-password", AppPassword: status}
	}
	manual = normalizeAppPassword(manual)
	if manual != "" && !validAppPassword(manual) {
		return control.Result{Message: "请输入 Apple 生成的 xxxx-xxxx-xxxx-xxxx 格式应用专用密码", Stage: "app-password-format"}
	}
	previous, stateErr := apppassword.Load(dir, appleID)
	if stateErr != nil && !os.IsNotExist(stateErr) {
		return control.Result{Message: "应用专用密码状态无法核对，未生成或覆盖凭据", Stage: "app-password-state"}
	}
	preserveUncertain = previous != nil && (previous.State == "pending" || previous.State == "unknown" || previous.State == "ready")
	saved, err := vault.LoadAppPassword(appleID)
	if err == nil && saved != nil && manual == "" {
		return finish("ready", "应用专用密码已加密保存")
	}
	if err != nil && !os.IsNotExist(err) {
		return finish("failed", "无法解密本账号的应用专用密码，未生成或覆盖凭据")
	}

	if manual == "" && previous != nil && (previous.State == "pending" || previous.State == "unknown" || previous.State == "ready") {
		return finish("unknown", "上次生成结果或本地记录待核对；请在 Apple 官网检查，并填入可用的应用专用密码")
	}
	ctx, cancel := context.WithTimeout(ctx, 165*time.Second)
	defer cancel()
	if automaticEligibility != nil {
		eligible, err := automaticEligibility(ctx)
		if err != nil {
			return finish("failed", "自动配置条件未能核验，可手动配置应用专用密码")
		}
		if !eligible {
			return finish("not_subscribed", "应用专用密码尚未配置")
		}
	}
	if vault.CheckAppPasswordStorage(appleID) != nil {
		return finish("failed", "本机加密存储不可用，未生成应用专用密码")
	}
	if manual != "" {
		if vault.SaveAppPassword(appleID, &appPasswordRecord{Password: manual, Label: appPasswordLabel, ID: "manual", CreatedAt: time.Now().UTC()}) != nil {
			return finish("failed", "应用专用密码未能保存，请保留官网生成的密码")
		}
		return finish("ready", "应用专用密码已加密保存；尚未验证收信")
	}
	client, err := portal()
	if err != nil {
		return finish("failed", "无法初始化 Apple 账户管理会话，尚未生成密码")
	}
	if client.Resume(ctx) != nil {
		if password == "" || client.Authenticate(ctx, password, code) != nil {
			return finish("verification_required", "Apple 账户管理需要验证；可重新连接后配置，或填入官网生成的应用专用密码")
		}
	}
	entries, err := client.ListAppSpecificPasswords(ctx)
	if errors.Is(err, icloud.ErrAccountManagementAuthenticationRequired) && password != "" {
		if client.Authenticate(ctx, password, code) == nil {
			entries, err = client.ListAppSpecificPasswords(ctx)
		}
	}
	if err != nil {
		return finish("verification_required", "Apple 未允许读取应用专用密码列表，请完成账户验证或从官网配置")
	}
	for _, entry := range entries {
		if strings.EqualFold(strings.TrimSpace(entry.Label), appPasswordLabel) {
			return finish("unknown", "Apple 已有 icloud-hme 应用专用密码，但本机没有记录；请填入原密码或在官网处理")
		}
	}
	if len(entries) >= 25 {
		return finish("verification_required", "Apple 应用专用密码数量已达上限，请在官网管理后再配置")
	}
	if ctx.Err() != nil {
		return finish("failed", "检查已超时，尚未生成应用专用密码")
	}
	// Durable intent precedes the one-time secret-producing request. If the
	// process dies or the response is lost, startup never silently creates another.
	intent := &apppassword.Status{State: "pending", Message: "正在生成并加密保存应用专用密码", Label: appPasswordLabel}
	if apppassword.Save(dir, appleID, intent) != nil {
		return control.Result{Message: "无法保存生成任务，尚未提交", Stage: "app-password-state"}
	}
	generated, err := client.CreateAppSpecificPassword(ctx, appPasswordLabel, password)
	if err != nil {
		var failure *icloud.AppSpecificPasswordError
		if errors.As(err, &failure) && !failure.CreationUncertain {
			return finish("verification_required", "Apple 未完成应用专用密码验证；请在官网处理后填入密码")
		}
		return finish("unknown", "生成结果未确认，不会自动重试；请在 Apple 官网核对应用专用密码")
	}
	if generated != nil {
		generated.Password = normalizeAppPassword(generated.Password)
	}
	if generated == nil || !validAppPassword(generated.Password) || generated.ID == "" {
		return finish("unknown", "Apple 返回的密码记录不完整，请在官网核对；不会自动重复生成")
	}
	if vault.SaveAppPassword(appleID, &appPasswordRecord{Password: generated.Password, Label: appPasswordLabel, ID: generated.ID, CreatedAt: time.Now().UTC()}) != nil {
		return finish("unknown", "Apple 已生成密码，但本机加密保存失败；请在官网核对后手动配置")
	}
	return finish("ready", "应用专用密码已生成并加密保存")
}

// Apple's API returns 16 letters; its portal displays groups of four.
func normalizeAppPassword(value string) string {
	value = strings.TrimSpace(value)
	if len(value) == 16 {
		return value[:4] + "-" + value[4:8] + "-" + value[8:12] + "-" + value[12:]
	}
	return value
}

func nextAppPasswordLabel(entries []icloud.AppSpecificPassword) string {
	taken := make(map[string]bool, len(entries))
	for _, entry := range entries {
		taken[strings.ToLower(strings.TrimSpace(entry.Label))] = true
	}
	if !taken[strings.ToLower(appPasswordLabel)] {
		return appPasswordLabel
	}
	for n := 2; n <= 25; n++ {
		candidate := fmt.Sprintf("%s-%d", appPasswordLabel, n)
		if !taken[strings.ToLower(candidate)] {
			return candidate
		}
	}
	return appPasswordLabel
}

func rotateAppPassword(ctx context.Context, dir, appleID, password string, vault appPasswordVault, portal func() (appPasswordPortal, error), code func(context.Context) (string, error)) (*appPasswordRecord, control.Result) {
	finish := func(state, label, message string) (*appPasswordRecord, control.Result) {
		if label == "" {
			label = appPasswordLabel
		}
		status := &apppassword.Status{State: state, Message: message, Label: label}
		_ = apppassword.Save(dir, appleID, status)
		return nil, control.Result{OK: state == "ready", Message: message, Stage: "app-password", AppPassword: status}
	}
	if strings.TrimSpace(password) == "" {
		return finish("verification_required", appPasswordLabel, "原应用专用密码已失效；请先点击“重新登录”加载当前密码后自动重新生成")
	}
	if vault.CheckAppPasswordStorage(appleID) != nil {
		return finish("failed", appPasswordLabel, "本机加密存储不可用，未重新生成应用专用密码")
	}
	client, err := portal()
	if err != nil {
		return finish("failed", appPasswordLabel, "无法初始化 Apple 账户管理会话，未重新生成应用专用密码")
	}
	if client.Resume(ctx) != nil {
		if client.Authenticate(ctx, password, code) != nil {
			if diag, ok := client.(interface{ AuthenticationFailure() (string, int) }); ok {
				stage, status := diag.AuthenticationFailure()
				fmt.Fprintf(os.Stderr, "App password portal auth failed: stage=%s http=%d\n", stage, status)
			}
			return finish("verification_required", appPasswordLabel, "原应用专用密码已失效，且本机保存的登录密码未通过 Apple 验证；请先点击“重新登录”更新当前账号密码后再同步")
		}
	}
	entries, err := client.ListAppSpecificPasswords(ctx)
	if errors.Is(err, icloud.ErrAccountManagementAuthenticationRequired) {
		if client.Authenticate(ctx, password, code) != nil {
			if diag, ok := client.(interface{ AuthenticationFailure() (string, int) }); ok {
				stage, status := diag.AuthenticationFailure()
				fmt.Fprintf(os.Stderr, "App password portal reauth failed: stage=%s http=%d\n", stage, status)
			}
			return finish("verification_required", appPasswordLabel, "原应用专用密码已失效，且本机保存的登录密码未通过 Apple 验证；请先点击“重新登录”更新当前账号密码后再同步")
		}
		entries, err = client.ListAppSpecificPasswords(ctx)
	}
	if err != nil {
		return finish("verification_required", appPasswordLabel, "原应用专用密码已失效，且无法读取 Apple 应用专用密码列表；请先重新登录账号后再同步")
	}
	if len(entries) >= 25 {
		return finish("verification_required", appPasswordLabel, "Apple 应用专用密码数量已达上限（25 个），请在官网清理后再同步")
	}
	label := nextAppPasswordLabel(entries)
	intent := &apppassword.Status{State: "pending", Message: "原应用专用密码已失效，正在自动重新生成", Label: label}
	if apppassword.Save(dir, appleID, intent) != nil {
		return nil, control.Result{Message: "无法保存应用专用密码更新任务", Stage: "app-password-state"}
	}
	generated, err := client.CreateAppSpecificPassword(ctx, label, password)
	if err != nil {
		var failure *icloud.AppSpecificPasswordError
		if errors.As(err, &failure) && failure.Code == "password-rejected" {
			return finish("verification_required", label, "本机保存的登录密码已被更改，请先点击“重新登录”输入最新密码后再同步")
		}
		if errors.As(err, &failure) && !failure.CreationUncertain {
			return finish("verification_required", label, "Apple 未完成新应用专用密码验证，请重新登录账号或从官网配置")
		}
		return finish("unknown", label, "新应用专用密码生成结果未确认，请在官网核对")
	}
	if generated != nil {
		generated.Password = normalizeAppPassword(generated.Password)
	}
	if generated == nil || !validAppPassword(generated.Password) || generated.ID == "" {
		return finish("unknown", label, "Apple 返回的新应用专用密码不完整，请在官网核对")
	}
	record := &appPasswordRecord{Password: generated.Password, Label: label, ID: generated.ID, CreatedAt: time.Now().UTC()}
	if vault.SaveAppPassword(appleID, record) != nil {
		return finish("unknown", label, "Apple 已生成新应用专用密码，但本机保存失败")
	}
	status := &apppassword.Status{State: "ready", Message: "已自动重新生成并保存应用专用密码", Label: label}
	_ = apppassword.Save(dir, appleID, status)
	return record, control.Result{OK: true, Message: status.Message, Stage: "app-password", AppPassword: status}
}
