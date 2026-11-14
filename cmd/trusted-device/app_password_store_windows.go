//go:build windows

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type protectedAppPassword struct {
	Version int
	AppleID string
	DSID    string
	Record  appPasswordRecord
}

func appPasswordEntropy(appleID, dsid string) []byte {
	hash := sha256.Sum256([]byte("appleservices-app-password-v1\x00" + appleID + "\x00" + dsid))
	return hash[:]
}

func (s fileStore) appPasswordBinding(appleID string) (string, string, error) {
	canonical := strings.ToLower(strings.TrimSpace(appleID))
	session, err := s.LoadSession()
	if err != nil || session == nil || canonical == "" || strings.ToLower(strings.TrimSpace(session.AppleID)) != canonical || strings.TrimSpace(session.DSID) == "" {
		return "", "", errors.New("application-specific password account binding is incomplete")
	}
	return canonical, session.DSID, nil
}

// CheckAppPasswordStorage must succeed before requesting a password that Apple
// will only display once. The probe writes ciphertext only and leaves any saved
// credential untouched.
func (s fileStore) CheckAppPasswordStorage(appleID string) error {
	canonical, dsid, err := s.appPasswordBinding(appleID)
	if err != nil {
		return err
	}
	probe := []byte("application-specific password storage probe")
	encrypted, err := protectStoreData(probe, appPasswordEntropy(canonical, dsid), false)
	if err != nil {
		return errors.New("Windows application-specific password protection is unavailable")
	}
	plain, err := protectStoreData(encrypted, appPasswordEntropy(canonical, dsid), true)
	if err != nil {
		return errors.New("Windows application-specific password protection is unavailable")
	}
	defer clear(plain)
	if !bytes.Equal(plain, probe) {
		return errors.New("Windows application-specific password protection is unavailable")
	}
	path := filepath.Join(s.dir, appPasswordFile)
	if stat, err := os.Stat(path); err == nil {
		if !stat.Mode().IsRegular() || stat.Mode().Perm()&0200 == 0 {
			return errors.New("application-specific password storage is not writable")
		}
		file, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			return errors.New("application-specific password storage is not writable")
		}
		if err := file.Close(); err != nil {
			return errors.New("application-specific password storage is not writable")
		}
	} else if !os.IsNotExist(err) {
		return errors.New("application-specific password storage is not writable")
	}
	file, err := os.CreateTemp(s.dir, ".app-password-check-*.tmp")
	if err != nil {
		return errors.New("application-specific password storage is not writable")
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return errors.New("application-specific password storage is not writable")
	}
	if _, err := file.Write(encrypted); err != nil {
		return errors.New("application-specific password storage is not writable")
	}
	if err := file.Sync(); err != nil {
		return errors.New("application-specific password storage is not writable")
	}
	if err := file.Close(); err != nil {
		return errors.New("application-specific password storage is not writable")
	}
	return nil
}

func (s fileStore) SaveAppPassword(appleID string, record *appPasswordRecord) error {
	canonical, dsid, err := s.appPasswordBinding(appleID)
	if err != nil {
		return err
	}
	if !validAppPasswordRecord(record) {
		return errors.New("invalid application-specific password record")
	}
	plain, err := json.Marshal(protectedAppPassword{Version: 1, AppleID: canonical, DSID: dsid, Record: *record})
	if err != nil {
		return errors.New("invalid application-specific password record")
	}
	defer clear(plain)
	encrypted, err := protectStoreData(plain, appPasswordEntropy(canonical, dsid), false)
	if err != nil {
		return errors.New("Windows application-specific password protection is unavailable")
	}
	if err := writeAppPasswordCiphertext(filepath.Join(s.dir, appPasswordFile), encrypted); err != nil {
		return errors.New("could not save protected application-specific password")
	}
	return nil
}

// Apple displays a new password only once. Flush its ciphertext before the
// atomic replacement, preserving the previous record if any write step fails.
func writeAppPasswordCiphertext(path string, encrypted []byte) error {
	data, err := json.Marshal(encrypted)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".app-password-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func (s fileStore) LoadAppPassword(appleID string) (*appPasswordRecord, error) {
	canonical, dsid, err := s.appPasswordBinding(appleID)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(filepath.Join(s.dir, appPasswordFile))
	if os.IsNotExist(err) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, errors.New("could not read protected application-specific password")
	}
	defer file.Close()
	const maxSize = 32 << 10
	data, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		return nil, errors.New("could not read protected application-specific password")
	}
	var encrypted []byte
	if len(data) > maxSize || json.Unmarshal(data, &encrypted) != nil {
		return nil, errors.New("invalid protected application-specific password")
	}
	plain, err := protectStoreData(encrypted, appPasswordEntropy(canonical, dsid), true)
	if err != nil {
		return nil, errors.New("could not restore protected application-specific password")
	}
	defer clear(plain)
	var saved protectedAppPassword
	if json.Unmarshal(plain, &saved) != nil || saved.Version != 1 || saved.AppleID != canonical || saved.DSID != dsid || !validAppPasswordRecord(&saved.Record) {
		return nil, errors.New("protected application-specific password binding or record is invalid")
	}
	return &saved.Record, nil
}
