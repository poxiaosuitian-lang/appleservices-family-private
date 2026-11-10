package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/Laky-64/appleservices"
)

type fileStore struct {
	dir string
}

func (s fileStore) devicePath() string   { return filepath.Join(s.dir, "device.json") }
func (s fileStore) sessionPath() string  { return filepath.Join(s.dir, "session.json") }
func (s fileStore) acceptedPath() string { return filepath.Join(s.dir, "icloud-accepted.json") }

func (s fileStore) LoadDevice() (*appleservices.Device, error) {
	data, err := os.ReadFile(s.devicePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var dev appleservices.Device
	if err := json.Unmarshal(data, &dev); err != nil {
		return nil, err
	}
	return &dev, nil
}

func (s fileStore) SaveDevice(dev *appleservices.Device) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	return writeJSONFile(s.devicePath(), dev)
}

func (s fileStore) LoadSession() (*appleservices.Session, error) {
	data, err := os.ReadFile(s.sessionPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var sess appleservices.Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

func (s fileStore) SaveSession(sess *appleservices.Session) error {
	if sess == nil {
		for _, name := range []string{"security-answers.dpapi", "security-questions-status.json"} {
			if err := os.Remove(filepath.Join(s.dir, name)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if err := s.ForgetLoginPassword(); err != nil {
			return err
		}
		if err := os.Remove(filepath.Join(s.dir, "store-session.dpapi")); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.Remove(s.acceptedPath()); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.Remove(s.sessionPath()); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	return writeJSONFile(s.sessionPath(), sess)
}

func (s fileStore) ForgetLoginPassword() error {
	err := os.Remove(filepath.Join(s.dir, "login-password.dpapi"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s fileStore) SaveAcceptedICloud(appleID string, accepted appleservices.RestoredICloudSession) error {
	saved, err := s.LoadSession()
	if err != nil || saved == nil || appleID == "" || !strings.EqualFold(saved.AppleID, appleID) ||
		accepted.ADSID == "" || saved.DSID != accepted.ADSID || accepted.DSID == "" || accepted.MMEAuthToken == "" || accepted.ServiceData == nil {
		return errors.New("accepted iCloud session is incomplete or belongs to another account")
	}
	return writeJSONFile(s.acceptedPath(), map[string]any{
		"session": appleservices.Session{AppleID: saved.AppleID, DSID: accepted.DSID,
			Tokens: map[string]string{"mmeAuthToken": accepted.MMEAuthToken, "cloudKitToken": accepted.CloudKitToken}},
		"adsid": accepted.ADSID, "service-data": accepted.ServiceData,
	})
}

// Only a missing delegate checkpoint can use the legacy migration path. A
// malformed or mismatched checkpoint requires reconnecting, not silent reuse.
func resumeSavedLogin(appleID string, store fileStore, options ...appleservices.Option) (*appleservices.Login, error) {
	accepted, err := store.LoadAcceptedICloud()
	if os.IsNotExist(err) {
		return appleservices.ResumeVerifiedLogin(appleID, store, options...)
	}
	if err != nil {
		return nil, errors.New("saved login session cannot be restored; reconnect with the current password")
	}
	return appleservices.ResumeLogin(appleID, store, accepted, options...)
}

func (s fileStore) LoadAcceptedICloud() (appleservices.RestoredICloudSession, error) {
	data, err := os.ReadFile(s.acceptedPath())
	if err != nil {
		return appleservices.RestoredICloudSession{}, err
	}
	var checkpoint struct {
		Session struct {
			AppleID string            `json:"AppleID"`
			DSID    string            `json:"DSID"`
			Tokens  map[string]string `json:"Tokens"`
		} `json:"session"`
		ADSID       string         `json:"adsid"`
		ServiceData map[string]any `json:"service-data"`
	}
	if len(data) > 2<<20 || json.Unmarshal(data, &checkpoint) != nil {
		return appleservices.RestoredICloudSession{}, errors.New("accepted iCloud checkpoint is invalid")
	}
	saved, err := s.LoadSession()
	if err != nil || saved == nil || !strings.EqualFold(checkpoint.Session.AppleID, saved.AppleID) || checkpoint.ADSID != saved.DSID {
		return appleservices.RestoredICloudSession{}, errors.New("accepted iCloud checkpoint account binding failed")
	}
	return appleservices.RestoredICloudSession{DSID: checkpoint.Session.DSID, ADSID: checkpoint.ADSID,
		MMEAuthToken: checkpoint.Session.Tokens["mmeAuthToken"], CloudKitToken: checkpoint.Session.Tokens["cloudKitToken"], ServiceData: checkpoint.ServiceData}, nil
}
