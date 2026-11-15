package main

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	"github.com/Laky-64/appleservices"
	"github.com/Laky-64/appleservices/gsa"
	"github.com/Laky-64/appleservices/icloud"
)

type loginPasswordStore interface {
	LoadLoginPassword(string) (string, error)
	SaveLoginPassword(string, string) error
}

func renewWithReconnect(begin, fresh func() (maintainedLogin, error), accountID string, registration appleservices.DeviceRegistration, register bool, password string, gate *reconnectGate) (maintainedLogin, []appleservices.AccountDevice, error) {
	login, devices, err := renewSessionMode(begin, accountID, registration, register)
	if gate.allow(err, password) {
		login, devices, err = renewSession(fresh, accountID, registration)
		// A disconnected password request is not a rejected password or an OTP
		// challenge. Leave it eligible for the next backed-off renewal.
		if transientLoginFailure(err) {
			gate.blocked = false
		}
	}
	if err == nil {
		gate.blocked = false
	}
	return login, devices, err
}

func transientLoginFailure(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, errVerificationRequired) {
		return false
	}
	var network net.Error
	if errors.As(err, &network) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var temporary interface{ Temporary() bool }
	if errors.As(err, &temporary) && temporary.Temporary() {
		return true
	}
	var storage *icloud.StorageError
	if errors.As(err, &storage) {
		return storage.HTTPStatus/100 == 5 || storage.Stage == "transport" || storage.Stage == "settings-transport"
	}
	var auth *gsa.AuthenticationError
	if errors.As(err, &auth) {
		return auth.HTTPStatus/100 == 5
	}
	var device *gsa.DeviceRequestError
	if errors.As(err, &device) {
		return device.HTTPStatus/100 == 5
	}
	var delegate *icloud.DelegateError
	return errors.As(err, &delegate) && delegate.HTTPStatus/100 == 5
}

// This timer exists only after a transient renewal failure, never while online.
type renewalRetry struct {
	timer *time.Timer
	delay time.Duration
}

func (r *renewalRetry) channel() <-chan time.Time {
	if r.timer == nil {
		return nil
	}
	return r.timer.C
}
func (r *renewalRetry) stop() {
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	r.delay = 0
}
func (r *renewalRetry) schedule() {
	if r.timer != nil {
		r.timer.Stop()
	}
	if r.delay == 0 {
		r.delay = 30 * time.Second
	} else {
		r.delay = min(2*r.delay, 5*time.Minute)
	}
	r.timer = time.NewTimer(r.delay)
}

func savedLoginPassword(store any, appleID string) string {
	if protected, ok := store.(loginPasswordStore); ok {
		password, _ := protected.LoadLoginPassword(appleID)
		return password
	}
	return ""
}

// Password authentication is a fallback for explicit authentication rejection,
// never for timeouts, network errors, rate limits, or a changed account binding.
func sessionAuthenticationRejected(err error) bool {
	status := 0
	var device *gsa.DeviceRequestError
	var storage *icloud.StorageError
	var delegate *icloud.DelegateError
	switch {
	case errors.As(err, &device):
		status = device.HTTPStatus
	case errors.As(err, &storage):
		status = storage.HTTPStatus
	case errors.As(err, &delegate):
		status = delegate.HTTPStatus
	}
	return status == 401 || status == 403
}

type reconnectGate struct{ blocked bool }

func (g *reconnectGate) allow(err error, password string) bool {
	if g.blocked || password == "" || !sessionAuthenticationRejected(err) {
		return false
	}
	g.blocked = true
	return true
}
