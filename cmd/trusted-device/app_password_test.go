package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Laky-64/appleservices/icloud"
	"github.com/Laky-64/appleservices/internal/apppassword"
)

const testAppSecret = "abcd-efgh-ijkl-mnop"

type fakeAppVault struct {
	record                     *appPasswordRecord
	loadErr, saveErr, probeErr error
	writes                     int
}

func (v *fakeAppVault) LoadAppPassword(string) (*appPasswordRecord, error) {
	if v.loadErr != nil {
		return nil, v.loadErr
	}
	if v.record == nil {
		return nil, os.ErrNotExist
	}
	return v.record, nil
}
func (v *fakeAppVault) CheckAppPasswordStorage(string) error { return v.probeErr }
func (v *fakeAppVault) SaveAppPassword(_ string, r *appPasswordRecord) error {
	v.writes++
	if v.saveErr != nil {
		return v.saveErr
	}
	v.record = r
	return nil
}

type fakeAppPortal struct {
	list               []icloud.AppSpecificPassword
	authErr, createErr error
	creates            int
	beforeCreate       func()
}

func (p *fakeAppPortal) Resume(context.Context) error { return p.authErr }
func (p *fakeAppPortal) Authenticate(context.Context, string, func(context.Context) (string, error)) error {
	return p.authErr
}
func (p *fakeAppPortal) ListAppSpecificPasswords(context.Context) ([]icloud.AppSpecificPassword, error) {
	return p.list, nil
}
func (p *fakeAppPortal) CreateAppSpecificPassword(context.Context, string, string) (*icloud.AppSpecificPassword, error) {
	p.creates++
	if p.beforeCreate != nil {
		p.beforeCreate()
	}
	if p.createErr != nil {
		return nil, p.createErr
	}
	return &icloud.AppSpecificPassword{ID: "1", Label: appPasswordLabel, Password: strings.ReplaceAll(testAppSecret, "-", "")}, nil
}

func TestAppPasswordCreatesOnceAndNeverExposesSecret(t *testing.T) {
	dir := t.TempDir()
	vault := &fakeAppVault{}
	portal := &fakeAppPortal{}
	portal.beforeCreate = func() {
		s, err := apppassword.Load(dir, "owner@example.test")
		if err != nil || s.State != "pending" {
			t.Fatal("creation must follow durable intent")
		}
	}
	for i := 0; i < 2; i++ {
		r := appPasswordAction(context.Background(), dir, "owner@example.test", "primary-secret", "", vault, func(context.Context) (bool, error) { return true, nil }, func() (appPasswordPortal, error) { return portal, nil }, nil)
		if !r.OK || r.AppPassword == nil || r.AppPassword.State != "ready" {
			t.Fatalf("not ready: %+v", r)
		}
		b, _ := json.Marshal(r)
		if strings.Contains(string(b), testAppSecret) || strings.Contains(string(b), "primary-secret") {
			t.Fatal("public response contains credential")
		}
	}
	if portal.creates != 1 || vault.writes != 1 {
		t.Fatal("must reuse saved password")
	}
}

func TestAppPasswordGuards(t *testing.T) {
	for _, mode := range []string{"unpaid", "probe", "decrypt", "auth", "duplicate-label", "limit", "unknown", "pending", "ready-missing", "wrong-account"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			vault := &fakeAppVault{}
			portal := &fakeAppPortal{}
			eligible := true
			switch mode {
			case "unpaid":
				eligible = false
			case "probe":
				vault.probeErr = errors.New("unavailable")
			case "decrypt":
				vault.loadErr = errors.New("invalid vault")
			case "auth":
				portal.authErr = errors.New("verification needed")
			case "duplicate-label":
				portal.list = []icloud.AppSpecificPassword{{ID: "old", Label: appPasswordLabel}}
			case "limit":
				portal.list = make([]icloud.AppSpecificPassword, 25)
			case "unknown", "pending":
				if apppassword.Save(dir, "owner@example.test", &apppassword.Status{State: mode}) != nil {
					t.Fatal("fixture")
				}
			case "ready-missing":
				_ = apppassword.Save(dir, "owner@example.test", &apppassword.Status{State: "ready"})
			case "wrong-account":
				_ = apppassword.Save(dir, "different@example.test", &apppassword.Status{State: "failed"})
			}
			r := appPasswordAction(context.Background(), dir, "owner@example.test", "primary-secret", "", vault, func(context.Context) (bool, error) { return eligible, nil }, func() (appPasswordPortal, error) { return portal, nil }, nil)
			if r.OK || portal.creates != 0 || vault.writes != 0 {
				t.Fatalf("must stop before create: %+v", r)
			}
		})
	}
}

func TestAppPasswordUnknownCreationAndSaveFailureDoNotReplay(t *testing.T) {
	for _, lostSave := range []bool{false, true} {
		dir := t.TempDir()
		vault := &fakeAppVault{}
		portal := &fakeAppPortal{}
		if lostSave {
			vault.saveErr = errors.New("disk unavailable")
		} else {
			portal.createErr = errors.New("lost response")
		}
		for i := 0; i < 2; i++ {
			r := appPasswordAction(context.Background(), dir, "owner@example.test", "primary-secret", "", vault, func(context.Context) (bool, error) { return true, nil }, func() (appPasswordPortal, error) { return portal, nil }, nil)
			if r.OK || r.AppPassword.State != "unknown" {
				t.Fatalf("unexpected: %+v", r)
			}
		}
		if portal.creates != 1 {
			t.Fatal("ambiguous generation replayed")
		}
	}
}

func TestAppPasswordManualSaveResolvesPendingWithoutRemoteCreation(t *testing.T) {
	dir := t.TempDir()
	vault := &fakeAppVault{}
	_ = apppassword.Save(dir, "owner@example.test", &apppassword.Status{State: "unknown"})
	r := appPasswordAction(context.Background(), dir, "owner@example.test", "", testAppSecret, vault, func(context.Context) (bool, error) { return true, nil }, func() (appPasswordPortal, error) {
		t.Fatal("manual save must not authenticate or generate")
		return nil, nil
	}, nil)
	if !r.OK || vault.record == nil || vault.record.Password != testAppSecret {
		t.Fatalf("not saved: %+v", r)
	}
}

func TestAppPasswordFailedManualSavePreservesUncertainGeneration(t *testing.T) {
	for _, mode := range []string{"unpaid", "probe", "save", "quota"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			vault, portal := &fakeAppVault{}, &fakeAppPortal{}
			_ = apppassword.Save(dir, "owner@example.test", &apppassword.Status{State: "unknown"})
			if mode == "probe" {
				vault.probeErr = errors.New("probe failed")
			}
			if mode == "save" {
				vault.saveErr = errors.New("save failed")
			}
			paid := func(context.Context) (bool, error) {
				if mode == "quota" {
					return false, errors.New("quota failed")
				}
				return mode != "unpaid", nil
			}
			factory := func() (appPasswordPortal, error) { return portal, nil }
			r := appPasswordAction(context.Background(), dir, "owner@example.test", "", testAppSecret, vault, paid, factory, nil)
			if r.OK || r.AppPassword.State != "unknown" {
				t.Fatal("must retain submission marker")
			}
			vault.probeErr, vault.saveErr = nil, nil
			r = appPasswordAction(context.Background(), dir, "owner@example.test", "primary-secret", "", vault, func(context.Context) (bool, error) { return true, nil }, factory, nil)
			if r.OK || portal.creates != 0 {
				t.Fatal("failed manual import must not enable automatic generation")
			}
		})
	}
}
