package main

import (
	"errors"
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
	}
	if err == nil {
		gate.blocked = false
	}
	return login, devices, err
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
