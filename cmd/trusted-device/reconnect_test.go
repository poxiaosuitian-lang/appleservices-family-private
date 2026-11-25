package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Laky-64/appleservices"
	"github.com/Laky-64/appleservices/gsa"
	"github.com/Laky-64/appleservices/icloud"
)

func TestReconnectOnlyAfterExplicitAppleAuthenticationRejection(t *testing.T) {
	for _, test := range []struct {
		err   error
		allow bool
	}{
		{nil, false}, {errors.New("network timed out"), false}, {errors.New("different account"), false},
		{&gsa.DeviceRequestError{HTTPStatus: 401}, true},
		{appleservices.ErrDeviceIdentityRefreshRequired, true},
		{fmt.Errorf("wrapped: %w", &icloud.StorageError{HTTPStatus: 403}), true},
		{&icloud.DelegateError{HTTPStatus: 401}, true},
		{errDeviceSessionExpired, true},
		{&icloud.StorageError{HTTPStatus: 429}, false}, {&gsa.DeviceRequestError{HTTPStatus: 503}, false},
		{errVerificationRequired, false},
	} {
		gate := reconnectGate{}
		if gate.allow(test.err, "") {
			t.Fatal("reconnect without password")
		}
		if gate.allow(test.err, "synthetic") != test.allow {
			t.Fatal("wrong reconnect decision")
		}
		if test.allow && gate.allow(test.err, "synthetic") {
			t.Fatal("failed authentication would be repeated")
		}
	}
}

func TestNetworkFailureDuringPasswordFallbackDoesNotRequireOTP(t *testing.T) {
	gate := reconnectGate{}
	calls := 0
	begin := func() (maintainedLogin, error) { return nil, &icloud.StorageError{HTTPStatus: 401} }
	fresh := func() (maintainedLogin, error) {
		calls++
		if calls == 1 {
			return nil, fmt.Errorf("transport: %w", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("disconnected")})
		}
		return &fakeMaintainedLogin{account: "owner"}, nil
	}
	_, _, err := renewWithReconnect(begin, fresh, "owner", appleservices.DeviceRegistration{}, false, "fixture", &gate)
	if !transientLoginFailure(err) || gate.blocked {
		t.Fatal("network failure became an authentication block")
	}
	login, _, err := renewWithReconnect(begin, fresh, "owner", appleservices.DeviceRegistration{}, false, "fixture", &gate)
	if err != nil || login == nil || gate.blocked || calls != 2 {
		t.Fatal("network restoration did not recover session")
	}
}

func TestInterruptedHandshakeHasBoundedAutomaticRecovery(t *testing.T) {
	var gate reconnectGate
	var retry renewalRetry
	defer retry.stop()
	begin := func() (maintainedLogin, error) { return nil, &icloud.StorageError{HTTPStatus: 401} }
	attempts := 0
	fresh := func() (maintainedLogin, error) {
		attempts++
		return nil, errors.Join(errors.New("incomplete handshake"), nil)
	}
	renew := func() {
		_, _, err := renewWithReconnect(begin, fresh, "owner", appleservices.DeviceRegistration{}, false, "fixture", &gate)
		retry.afterFailure(err, gate.retryAt)
	}
	for n := 1; n <= 3; n++ {
		renew()
		if attempts != n || (retry.channel() != nil) != (n < 3) {
			t.Fatalf("attempt %d: calls=%d, scheduled=%t", n, attempts, retry.channel() != nil)
		}
		renew()
		if attempts != n {
			t.Fatal("ordinary renewal bypassed the cooldown")
		}
		if n < 3 {
			gate.retryAt = time.Now().Add(-time.Second)
		}
	}
}

func TestRejectedPasswordRemainsBlocked(t *testing.T) {
	var gate reconnectGate
	denied := &gsa.AuthenticationError{Code: -20101, ServerMessage: "private"}
	begin := func() (maintainedLogin, error) { return nil, &icloud.StorageError{HTTPStatus: 401} }
	fresh := func() (maintainedLogin, error) { return nil, errors.Join(denied, nil) }
	_, _, err := renewWithReconnect(begin, fresh, "owner", appleservices.DeviceRegistration{}, false, "fixture", &gate)
	if !gate.blocked || !gate.retryAt.IsZero() || renewalDiagnostic(err) != "gsa http=0 code=-20101" {
		t.Fatal("explicit authentication rejection must stop retries and log only numeric codes")
	}
}

func TestDueHandshakeRetryRestoresSession(t *testing.T) {
	var gate reconnectGate
	begin := func() (maintainedLogin, error) { return nil, &icloud.StorageError{HTTPStatus: 401} }
	interrupted := func() (maintainedLogin, error) { return nil, errors.New("incomplete handshake") }
	renewWithReconnect(begin, interrupted, "owner", appleservices.DeviceRegistration{}, false, "fixture", &gate)
	if gate.retryAt.IsZero() {
		t.Fatal("no automatic retry scheduled")
	}
	gate.retryAt = time.Now()
	session := &fakeMaintainedLogin{account: "owner"}
	fresh := func() (maintainedLogin, error) { return session, nil }
	got, _, err := renewWithReconnect(begin, fresh, "owner", appleservices.DeviceRegistration{}, false, "fixture", &gate)
	if err != nil || got != session || session.saved != 1 || gate != (reconnectGate{}) {
		t.Fatal("automatic retry did not restore and checkpoint the account")
	}
}

func TestPendingRenewalResumesVerifiedCandidateWithoutAnotherPasswordLogin(t *testing.T) {
	gate := reconnectGate{}
	candidate := &fakeMaintainedLogin{account: "owner", challenge: true}
	freshCalls := 0
	fresh := func() (maintainedLogin, error) { freshCalls++; return candidate, nil }
	begin := func() (maintainedLogin, error) { return nil, &icloud.StorageError{HTTPStatus: 401} }
	_, _, err := renewWithReconnect(begin, fresh, "owner", appleservices.DeviceRegistration{}, false, "fixture", &gate)
	if !errors.Is(err, errVerificationRequired) || !gate.blocked || candidate.saved != 0 {
		t.Fatal("challenge not retained as unverified")
	}
	// Verification completes on the existing challenge. Adoption still needs
	// same-account identity, registration and a successful readback.
	candidate.challenge = false
	resume := func() (maintainedLogin, error) { return candidate, nil }
	got, _, err := renewWithReconnect(resume, fresh, "owner", appleservices.DeviceRegistration{}, true, "fixture", &gate)
	if err != nil || got != candidate || gate.blocked || freshCalls != 1 || candidate.saved != 1 || candidate.registered != 1 {
		t.Fatal("verified candidate was not adopted without restarting login")
	}
}

func TestTransientLoginFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{nil, false}, {context.Canceled, false}, {context.DeadlineExceeded, true}, {io.EOF, true},
		{&icloud.StorageError{Stage: "transport"}, true}, {&icloud.StorageError{Stage: "settings-transport"}, true},
		{&icloud.StorageError{Stage: "authentication"}, false}, {&icloud.StorageError{HTTPStatus: 403}, false},
		{&gsa.AuthenticationError{HTTPStatus: 503}, true}, {&gsa.AuthenticationError{Code: -20101}, false},
		{&gsa.DeviceRequestError{HTTPStatus: 429}, false}, {errVerificationRequired, false},
		{errors.New("different account"), false},
	} {
		if transientLoginFailure(tc.err) != tc.want {
			t.Fatalf("classification mismatch for %T", tc.err)
		}
	}
}

func TestRenewalRetryIsInactiveUntilFailureAndStopsAfterRecovery(t *testing.T) {
	var retry renewalRetry
	defer retry.stop()
	if retry.channel() != nil {
		t.Fatal("healthy session has a retry timer")
	}
	for _, want := range []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute, 5 * time.Minute} {
		retry.schedule()
		if retry.delay != want || retry.channel() == nil {
			t.Fatal("incorrect retry backoff")
		}
	}
	retry.stop()
	if retry.channel() != nil || retry.delay != 0 {
		t.Fatal("recovery retained timer")
	}
}

func TestExpiredSessionReconnectsOnceAndAdoptsOnlyVerifiedAccount(t *testing.T) {
	for _, test := range []struct {
		name    string
		fresh   fakeMaintainedLogin
		success bool
	}{
		{"accepted", fakeMaintainedLogin{account: "owner"}, true},
		{"challenge", fakeMaintainedLogin{account: "owner", challenge: true}, false},
		{"different-account", fakeMaintainedLogin{account: "other"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			gate := reconnectGate{}
			begin := func() (maintainedLogin, error) {
				return &fakeMaintainedLogin{account: "owner", listErr: &gsa.DeviceRequestError{HTTPStatus: 401}}, nil
			}
			fresh := func() (maintainedLogin, error) { calls++; return &test.fresh, nil }
			got, _, err := renewWithReconnect(begin, fresh, "owner", appleservices.DeviceRegistration{}, false, "synthetic", &gate)
			if (err == nil) != test.success || calls != 1 {
				t.Fatal("expired-session fallback did not run exactly once")
			}
			if test.success && (got != &test.fresh || test.fresh.saved != 1 || gate.blocked) {
				t.Fatal("verified reconnect not adopted")
			}
			if !test.success {
				if got != nil || test.fresh.saved != 0 || !gate.blocked {
					t.Fatal("unsafe reconnect was adopted")
				}
				renewWithReconnect(begin, fresh, "owner", appleservices.DeviceRegistration{}, false, "synthetic", &gate)
				if calls != 1 {
					t.Fatal("rejected credentials or 2FA were retried automatically")
				}
			}
		})
	}
}

type automaticRenewalLogin struct {
	fakeMaintainedLogin
	verificationErr error
}

func (l *automaticRenewalLogin) CompleteLocalTwoFactor(code func() (string, error)) (bool, error) {
	if _, err := code(); err != nil {
		return true, err
	}
	if l.verificationErr != nil {
		return true, l.verificationErr
	}
	l.challenge = false
	return true, nil
}

func TestDeviceExpirationAutomaticallyVerifiesBeforeAdoptingRenewal(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		t.Run(fmt.Sprintf("rejected=%t", rejected), func(t *testing.T) {
			old := &quotaCheckedLogin{fakeMaintainedLogin: fakeMaintainedLogin{
				account: "owner", listErr: &gsa.DeviceRequestError{HTTPStatus: 401},
			}}
			candidate := &automaticRenewalLogin{fakeMaintainedLogin: fakeMaintainedLogin{account: "owner", challenge: true}}
			if rejected {
				candidate.verificationErr = errors.New("local verification rejected")
			}
			passwordLogins, generated := 0, 0
			begin := func() (maintainedLogin, error) { return old, nil }
			fresh := func() (maintainedLogin, error) {
				passwordLogins++
				method := renewalVerificationMethod("sms", true)
				completeLocalVerification(method, true, candidate, func() (string, error) {
					generated++
					return "123456", nil
				})
				return candidate, nil
			}
			gate := reconnectGate{}
			got, devices, err := renewWithReconnect(begin, fresh, "owner", appleservices.DeviceRegistration{}, false, "fixture", &gate)
			if passwordLogins != 1 || generated != 1 {
				t.Fatal("expired device identity did not trigger automatic verification")
			}
			if rejected {
				if !errors.Is(err, errVerificationRequired) || got != nil || candidate.saved != 0 {
					t.Fatal("rejected verification was adopted")
				}
				renewWithReconnect(begin, fresh, "owner", appleservices.DeviceRegistration{}, false, "fixture", &gate)
				if passwordLogins != 1 || generated != 1 {
					t.Fatal("rejected verification was automatically replayed")
				}
			} else if err != nil || got != candidate || len(devices) == 0 || candidate.saved != 1 || candidate.registered != 1 {
				t.Fatal("automatically verified renewal did not complete registration and checkpoint")
			}
		})
	}
}

func TestStartupResumeAutomaticallyReauthenticatesExpiredDeviceSession(t *testing.T) {
	ctx := context.Background()

	t.Run("saved-password-upgrades-expired-device-session-immediately", func(t *testing.T) {
		for _, deviceErr := range []error{
			&gsa.DeviceRequestError{HTTPStatus: 401},
			appleservices.ErrDeviceIdentityRefreshRequired,
		} {
			resumed := &quotaCheckedLogin{fakeMaintainedLogin: fakeMaintainedLogin{
				account: "owner", listErr: deviceErr,
			}}
			freshCandidate := &quotaCheckedLogin{fakeMaintainedLogin: fakeMaintainedLogin{
				account: "owner", challenge: true,
			}}
			freshCalls, localCodes := 0, 0
			var gate reconnectGate
			got, stillResumed, err := resumeWithReconnect(ctx,
				func() (*quotaCheckedLogin, error) { return resumed, nil },
				func() (*quotaCheckedLogin, error) {
					freshCalls++
					localCodes++
					freshCandidate.challenge = false
					return freshCandidate, nil
				},
				"saved-secret", &gate)
			if err != nil || stillResumed || got != freshCandidate || freshCalls != 1 || localCodes != 1 {
				t.Fatalf("startup resume did not auto-relogin expired device session (%v): err=%v stillResumed=%t freshCalls=%d", deviceErr, err, stillResumed, freshCalls)
			}
		}
	})

	t.Run("passwordless-retains-valid-icloud-session", func(t *testing.T) {
		resumed := &quotaCheckedLogin{fakeMaintainedLogin: fakeMaintainedLogin{
			account: "owner", listErr: &gsa.DeviceRequestError{HTTPStatus: 401},
		}}
		freshCalls := 0
		var gate reconnectGate
		got, stillResumed, err := resumeWithReconnect(ctx,
			func() (*quotaCheckedLogin, error) { return resumed, nil },
			func() (*quotaCheckedLogin, error) { freshCalls++; return nil, nil },
			"", &gate)
		if err != nil || !stillResumed || got != resumed || freshCalls != 0 {
			t.Fatalf("passwordless startup lost valid iCloud session: err=%v stillResumed=%t freshCalls=%d", err, stillResumed, freshCalls)
		}
	})

	t.Run("unverified-2fa-retains-valid-icloud-session-without-loop", func(t *testing.T) {
		resumed := &quotaCheckedLogin{fakeMaintainedLogin: fakeMaintainedLogin{
			account: "owner", listErr: &gsa.DeviceRequestError{HTTPStatus: 401},
		}}
		challenged := &quotaCheckedLogin{fakeMaintainedLogin: fakeMaintainedLogin{
			account: "owner", challenge: true,
		}}
		var gate reconnectGate
		got, stillResumed, err := resumeWithReconnect(ctx,
			func() (*quotaCheckedLogin, error) { return resumed, nil },
			func() (*quotaCheckedLogin, error) { return challenged, nil },
			"saved-secret", &gate)
		if err != nil || !stillResumed || got != resumed || !gate.blocked {
			t.Fatalf("unverified startup fallback broke valid iCloud session or left gate open: err=%v stillResumed=%t blocked=%t", err, stillResumed, gate.blocked)
		}
	})

	t.Run("transient-login-failure-retains-valid-icloud-and-unblocks-retry", func(t *testing.T) {
		resumed := &quotaCheckedLogin{fakeMaintainedLogin: fakeMaintainedLogin{
			account: "owner", listErr: &gsa.DeviceRequestError{HTTPStatus: 401},
		}}
		var gate reconnectGate
		got, stillResumed, err := resumeWithReconnect(ctx,
			func() (*quotaCheckedLogin, error) { return resumed, nil },
			func() (*quotaCheckedLogin, error) {
				return nil, &gsa.AuthenticationError{HTTPStatus: 503}
			},
			"saved-secret", &gate)
		if err != nil || !stillResumed || got != resumed || gate.blocked {
			t.Fatalf("transient startup fallback broke valid iCloud session or blocked retry: err=%v stillResumed=%t blocked=%t", err, stillResumed, gate.blocked)
		}
	})
}
