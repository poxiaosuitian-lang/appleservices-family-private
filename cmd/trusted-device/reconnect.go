package main

import (
	"context"
	"errors"
	"fmt"
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
		gate.recovering = true
		candidate, loginErr := fresh()
		if loginErr != nil {
			err = loginErr
			gate.deferHandshakeRetry(loginErr, time.Now())
		} else {
			login, devices, err = renewSession(func() (maintainedLogin, error) { return candidate, nil }, accountID, registration)
		}
		// A disconnected password request is not a rejected password or an OTP
		// challenge. Leave it eligible for the next backed-off renewal.
		if transientLoginFailure(err) {
			gate.blocked = false
		}
	}
	if err == nil {
		*gate = reconnectGate{}
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

// The retry timer is inactive while the session is healthy.
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

func (r *renewalRetry) afterFailure(err error, retryAt time.Time) {
	if delay := time.Until(retryAt); delay > 0 {
		r.stop()
		r.timer = time.NewTimer(delay)
	} else if transientLoginFailure(err) {
		r.schedule()
	} else {
		r.stop()
	}
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

type reconnectGate struct {
	blocked    bool
	recovering bool
	retryAt    time.Time
	retries    int
}

// Only a failed handshake without a definitive response gets another password
// attempt. Never replay a challenge or a failure while adopting a fresh login.
func (g *reconnectGate) deferHandshakeRetry(err error, now time.Time) {
	var auth *gsa.AuthenticationError
	var response interface{ HTTPStatusCode() int }
	if errors.As(err, &auth) || (errors.As(err, &response) && response.HTTPStatusCode()/100 == 4) || errors.Is(err, errVerificationRequired) ||
		errors.Is(err, context.Canceled) || transientLoginFailure(err) || g.retries >= 2 {
		return
	}
	g.retries++
	delay := time.Minute
	if g.retries == 2 {
		delay = 5 * time.Minute
	}
	g.retryAt = now.Add(delay)
}

func (g *reconnectGate) allow(err error, password string) bool {
	if password == "" || !sessionAuthenticationRejected(err) {
		return false
	}
	if g.blocked && (g.retryAt.IsZero() || time.Now().Before(g.retryAt)) {
		return false
	}
	g.retryAt = time.Time{}
	g.blocked = true
	return true
}

// Log only error classes and numeric response codes, including wrapped GSA errors.
func renewalDiagnostic(err error) string {
	var response interface{ HTTPStatusCode() int }
	var auth *gsa.AuthenticationError
	var device *gsa.DeviceRequestError
	var storage *icloud.StorageError
	var delegate *icloud.DelegateError
	switch {
	case errors.As(err, &auth):
		return fmt.Sprintf("gsa http=%d code=%d", auth.HTTPStatus, auth.Code)
	case errors.As(err, &device):
		return fmt.Sprintf("devices http=%d code=%d", device.HTTPStatus, device.Code)
	case errors.As(err, &storage):
		return fmt.Sprintf("storage http=%d", storage.HTTPStatus)
	case errors.As(err, &delegate):
		return fmt.Sprintf("delegate http=%d", delegate.HTTPStatus)
	case errors.As(err, &response):
		return fmt.Sprintf("handshake http=%d", response.HTTPStatusCode())
	default:
		return fmt.Sprintf("%T", err)
	}
}
