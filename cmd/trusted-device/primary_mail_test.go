package main

import (
	"context"
	"errors"
	"testing"

	"github.com/Laky-64/appleservices/icloud"
	"github.com/Laky-64/appleservices/internal/mailsetup"
)

type fakePrimaryMail struct {
	exists, authenticated, allow bool
	creates, auths               int
	fail                         bool
}

func (f *fakePrimaryMail) Check(context.Context) (*icloud.PrimaryMailInfo, error) {
	if !f.authenticated {
		return nil, icloud.ErrPrimaryMailAuthentication
	}
	return &icloud.PrimaryMailInfo{Exists: f.exists}, nil
}
func (f *fakePrimaryMail) Authenticate(context.Context, string, func(context.Context) (string, error)) error {
	f.auths++
	f.authenticated = true
	return nil
}
func (f *fakePrimaryMail) Ensure(_ context.Context, allow bool, checkpoint func(string) error) (*icloud.PrimaryMailInfo, error) {
	f.allow = allow
	if !allow {
		return nil, icloud.ErrPrimaryMailUnconfirmed
	}
	if err := checkpoint("alice@icloud.com"); err != nil {
		return nil, err
	}
	f.creates++
	if f.fail {
		return nil, errors.New("network failure")
	}
	f.exists = true
	return &icloud.PrimaryMailInfo{Exists: true, Email: "alice@icloud.com"}, nil
}

func TestPrimaryMailWorkerPersistsAndReconcilesAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	id := "alice@example.test"
	if err := mailsetup.Queue(dir, id); err != nil {
		t.Fatal(err)
	}
	f := &fakePrimaryMail{fail: true}
	if r := primaryMailAction(context.Background(), dir, id, "password", nil, false, f); r.OK {
		t.Fatal("uncertain submission reported ready")
	}
	s, err := mailsetup.Load(dir, id)
	if err != nil || !s.Submitted || s.Phase != "unconfirmed" || f.creates != 1 || f.auths != 1 {
		t.Fatalf("missing checkpoint: %+v %v", s, err)
	}
	// A fresh worker must not replay an uncertain activation request.
	f = &fakePrimaryMail{authenticated: true}
	primaryMailAction(context.Background(), dir, id, "", nil, false, f)
	if f.creates != 0 || f.allow {
		t.Fatal("restart replayed uncertain creation")
	}
	f.exists = true
	r := primaryMailAction(context.Background(), dir, id, "", nil, true, f)
	s, _ = mailsetup.Load(dir, id)
	if !r.OK || s.Phase != "ready" {
		t.Fatal("eventual provisioning not reconciled")
	}
}

func TestPrimaryMailCheckOnlyDoesNotOptExistingAccountIntoAutoCreate(t *testing.T) {
	dir := t.TempDir()
	id := "alice@example.test"
	f := &fakePrimaryMail{authenticated: true}
	r := primaryMailAction(context.Background(), dir, id, "", nil, true, f)
	s, err := mailsetup.Load(dir, id)
	if !r.OK || err != nil || s.AutoCreate || s.Submitted || f.creates != 0 {
		t.Fatal("read-only check opted account into creation")
	}
	if _, err := mailsetup.Load(dir, "other@example.test"); err == nil {
		t.Fatal("cross-account state accepted")
	}
}
