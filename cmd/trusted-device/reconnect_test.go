package main

import (
	"errors"
	"fmt"
	"github.com/Laky-64/appleservices"
	"testing"

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
		{fmt.Errorf("wrapped: %w", &icloud.StorageError{HTTPStatus: 403}), true},
		{&icloud.DelegateError{HTTPStatus: 401}, true},
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
