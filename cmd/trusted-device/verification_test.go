package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Laky-64/appleservices"
)

type verificationLogin struct {
	localCalls int
	attempted  bool
	localErr   error
	requests   []appleservices.TwoFactorMethod
	submits    []appleservices.TwoFactorMethod
	submitErr  error
}

func (v *verificationLogin) CompleteLocalTwoFactor(func() (string, error)) (bool, error) {
	v.localCalls++
	return v.attempted, v.localErr
}

func (v *verificationLogin) RequestCode(factor appleservices.TwoFactorMethod) error {
	v.requests = append(v.requests, factor)
	return nil
}

func (v *verificationLogin) SubmitCode(factor appleservices.TwoFactorMethod, _ string) error {
	v.submits = append(v.submits, factor)
	return v.submitErr
}

func TestInitialStatusRetainsSavedPasswordWhileAwaitingVerification(t *testing.T) {
	for _, remembered := range []bool{false, true} {
		status := initialRuntimeStatus(123, remembered)
		dir := t.TempDir()
		for _, state := range []string{"starting", "awaiting_device_code", "failed"} {
			status.State = state
			if err := status.save(dir); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(dir, "runtime.json"))
			if err != nil {
				t.Fatal(err)
			}
			var saved runtimeStatus
			if err := json.Unmarshal(data, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.PasswordRemembered != remembered || saved.Authenticated || saved.LocalVerification != localVerificationNotAttempted {
				t.Fatal("pre-authentication status misreported available credential or verification")
			}
		}
	}
}

func TestLocalVerificationHonorsSelectedFactorAndReportsActualAttempt(t *testing.T) {
	for _, test := range []struct {
		name      string
		method    string
		available bool
		attempted bool
		err       error
		wantCalls int
		wantState string
	}{
		{"sms", "sms", true, true, nil, 0, localVerificationNotAttempted},
		{"existing-sms", "sms-existing", true, true, nil, 0, localVerificationNotAttempted},
		{"circle", "circle", true, true, nil, 0, localVerificationNotAttempted},
		{"no-local-module", "trusted-device", false, true, nil, 0, localVerificationNotAttempted},
		{"ineligible", "trusted-device", true, false, nil, 1, localVerificationNotAttempted},
		{"accepted", "trusted-device", true, true, nil, 1, localVerificationSucceeded},
		{"rejected", "trusted-device", true, true, errors.New("code rejected"), 1, localVerificationFailed},
		{"request-failed", "trusted-device", true, true, appleservices.ErrLocalCodeRequest, 1, localVerificationRequestFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			login := &verificationLogin{attempted: test.attempted, localErr: test.err}
			attempted, state, err := completeLocalVerification(test.method, test.available, login, nil)
			wantAttempt := test.wantCalls != 0 && test.attempted
			if login.localCalls != test.wantCalls || attempted != wantAttempt || state != test.wantState || (err != nil) != (test.wantCalls != 0 && test.err != nil) {
				t.Fatal("selected verification method or attempt outcome was not preserved")
			}
			if test.method == "sms" || test.method == "sms-existing" {
				lastSent := time.Now()
				result, completed := pendingVerificationAction(login, initialVerificationFactor(test.method), "submit-sms", "123456", &lastSent, time.Now())
				if !result.OK || !completed || len(login.submits) != 1 || login.submits[0] != appleservices.SMS {
					t.Fatal("manual SMS submission changed verification factor")
				}
			}
		})
	}
}

func TestPendingVerificationCannotGenerateMoreLocalCodesAndKeepsManualSubmission(t *testing.T) {
	login := &verificationLogin{attempted: true, localErr: errors.New("local attempt failed")}
	_, state, _ := completeLocalVerification("trusted-device", true, login, nil)
	if state != localVerificationFailed {
		t.Fatal("expected unsuccessful local verification")
	}
	lastSent := time.Now().Add(-2 * time.Minute)
	for i := 0; i < 3; i++ {
		result, completed := pendingVerificationAction(login, appleservices.TrustedDevice, "code", "", &lastSent, time.Now())
		if result.OK || completed || result.Code != "" || result.CodeKind != "" {
			t.Fatal("pending login exposed a reusable local-code generation path")
		}
	}
	result, completed := pendingVerificationAction(login, appleservices.TrustedDevice, "resend-sms", "", &lastSent, time.Now())
	if result.OK || completed || len(login.requests) != 0 || len(login.submits) != 0 || login.localCalls != 1 {
		t.Fatal("device-code challenge was retried or described as an SMS resend")
	}
	login.submitErr = errors.New("expired manual code")
	result, completed = pendingVerificationAction(login, appleservices.TrustedDevice, "submit-sms", "123456", &lastSent, time.Now())
	if result.OK || completed {
		t.Fatal("rejected manual verification completed login")
	}
	login.submitErr = nil
	result, completed = pendingVerificationAction(login, appleservices.TrustedDevice, "submit-sms", "654321", &lastSent, time.Now())
	if !result.OK || !completed || len(login.submits) != 2 || login.submits[1] != appleservices.TrustedDevice {
		t.Fatal("failed automatic verification blocked manual completion")
	}
}

func TestSMSResendStillRequiresOneMinuteInterval(t *testing.T) {
	login := &verificationLogin{}
	now := time.Now()
	lastSent := now.Add(-30 * time.Second)
	result, _ := pendingVerificationAction(login, appleservices.SMS, "resend-sms", "", &lastSent, now)
	if result.OK || len(login.requests) != 0 {
		t.Fatal("SMS resend ignored rate limit")
	}
	result, _ = pendingVerificationAction(login, appleservices.SMS, "resend-sms", "", &lastSent, now.Add(time.Minute))
	if !result.OK || len(login.requests) != 1 || login.requests[0] != appleservices.SMS {
		t.Fatal("explicit SMS resend did not retain SMS factor")
	}
}
