package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/Laky-64/appleservices/icloud"
	"github.com/Laky-64/appleservices/internal/apppassword"
	"github.com/Laky-64/appleservices/internal/control"
)

const appPasswordLabel = "icloud-hme"

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

func appPasswordAction(ctx context.Context, dir, appleID, password, manual string, vault appPasswordVault, paid func(context.Context) (bool, error), portal func() (appPasswordPortal, error), code func(context.Context) (string, error)) control.Result {
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
	eligible, err := paid(ctx)
	if err != nil {
		return finish("failed", "未能核验 iCloud+ 订阅，尚未生成应用专用密码")
	}
	if !eligible {
		return finish("not_subscribed", "开通 iCloud+ 后配置应用专用密码")
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
