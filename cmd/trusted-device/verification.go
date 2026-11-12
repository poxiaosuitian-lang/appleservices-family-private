package main

import (
	"errors"
	"time"

	"github.com/Laky-64/appleservices"
	"github.com/Laky-64/appleservices/internal/control"
)

const (
	localVerificationNotAttempted  = "not_attempted"
	localVerificationSucceeded     = "succeeded"
	localVerificationFailed        = "failed"
	localVerificationRequestFailed = "request_failed"
)

func initialRuntimeStatus(pid int, passwordRemembered bool) runtimeStatus {
	return runtimeStatus{PID: pid, Running: true, State: "starting", PasswordRemembered: passwordRemembered, LocalVerification: localVerificationNotAttempted}
}

type localTwoFactorLogin interface {
	CompleteLocalTwoFactor(func() (string, error)) (bool, error)
}

// Selecting SMS must never start a device-code challenge or replace the user's
// selected factor after a failed local attempt. The Login owns the bound-account
// eligibility check and the single-attempt guard.
func completeLocalVerification(method string, localAvailable bool, login localTwoFactorLogin, code func() (string, error)) (bool, string, error) {
	if method != "trusted-device" || !localAvailable {
		return false, localVerificationNotAttempted, nil
	}
	attempted, err := login.CompleteLocalTwoFactor(code)
	if !attempted {
		return false, localVerificationNotAttempted, err
	}
	if errors.Is(err, appleservices.ErrLocalCodeRequest) {
		return true, localVerificationRequestFailed, err
	}
	if err != nil {
		return true, localVerificationFailed, err
	}
	return true, localVerificationSucceeded, nil
}

func initialVerificationFactor(method string) appleservices.TwoFactorMethod {
	if method == "trusted-device" {
		return appleservices.TrustedDevice
	}
	return appleservices.SMS
}

type pendingVerificationLogin interface {
	RequestCode(appleservices.TwoFactorMethod) error
	SubmitCode(appleservices.TwoFactorMethod, string) error
}

// A pending challenge accepts manual verification, but never exposes raw local
// codes as a way around CompleteLocalTwoFactor's eligibility and retry guards.
func pendingVerificationAction(login pendingVerificationLogin, factor appleservices.TwoFactorMethod, action, code string, lastSent *time.Time, now time.Time) (control.Result, bool) {
	result := control.Result{}
	device := factor == appleservices.TrustedDevice
	switch action {
	case "code":
		if device {
			result.Message = "当前登录仍需 Apple 验证；本机自动验证不会重复尝试，请使用受信任设备验证码或改用短信验证"
		} else {
			result.Message = "当前验证方式是短信，请填写手机收到的验证码"
		}
	case "resend-sms":
		if device {
			result.Message = "当前为受信任设备验证；如需短信，请重新连接并选择短信验证码"
		} else if now.Sub(*lastSent) < time.Minute {
			result.Message = "请间隔一分钟再重发短信"
		} else if login.RequestCode(factor) != nil {
			result.Message = "短信发送失败，请稍后重试"
		} else {
			*lastSent = now
			result.OK, result.Message = true, "短信已发送"
		}
	case "submit-sms":
		if err := login.SubmitCode(factor, code); err != nil {
			result.Message = "验证码未被接受：" + err.Error()
		} else {
			result.OK, result.Message = true, "验证码已验证，正在完成登录"
			return result, true
		}
	default:
		if device {
			result.Message = "请先完成受信任设备验证"
		} else {
			result.Message = "请先完成短信验证"
		}
	}
	return result, false
}
