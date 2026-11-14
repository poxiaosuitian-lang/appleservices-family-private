//go:build !windows

package main

import "errors"

func (s fileStore) CheckAppPasswordStorage(string) error {
	return errors.New("application-specific password storage requires Windows DPAPI")
}

func (s fileStore) SaveAppPassword(string, *appPasswordRecord) error {
	return errors.New("application-specific password storage requires Windows DPAPI")
}

func (s fileStore) LoadAppPassword(string) (*appPasswordRecord, error) {
	return nil, errors.New("application-specific password storage requires Windows DPAPI")
}
