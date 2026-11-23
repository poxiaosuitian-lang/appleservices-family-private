package main

import (
	"context"
	"errors"
	"testing"

	"github.com/Laky-64/appleservices/icloud"
	"github.com/Laky-64/appleservices/internal/mailsetup"
)

type fakePrimaryMail struct {
	exists, authenticated, allow          bool
	termsRequired, pendingTermsAvailable  bool
	creates, auths, pendingRepairs, repairs int
	fail                                  bool
}

func (f *fakePrimaryMail) Check(context.Context) (*icloud.PrimaryMailInfo, error) {
	if !f.authenticated {
		return nil, icloud.ErrPrimaryMailAuthentication
	}
	if f.termsRequired {
		return nil, icloud.ErrHMEWebRepairRequired
	}
	return &icloud.PrimaryMailInfo{Exists: f.exists}, nil
}
func (f *fakePrimaryMail) Authenticate(context.Context, string, func(context.Context) (string, error)) error {
	f.auths++
	f.authenticated = true
	if f.termsRequired {
		f.pendingTermsAvailable = true
		return icloud.ErrHMEWebRepairRequired
	}
	return nil
}
func (f *fakePrimaryMail) AcceptPendingTerms(context.Context) error {
	if !f.pendingTermsAvailable {
		return icloud.ErrHMEWebRepairSessionUnavailable
	}
	f.pendingRepairs++
	f.pendingTermsAvailable = false
	f.termsRequired = false
	return nil
}
func (f *fakePrimaryMail) AuthenticateAndAcceptTerms(context.Context, string, func(context.Context) (string, error)) error {
	f.repairs++
	f.authenticated = true
	f.pendingTermsAvailable = false
	f.termsRequired = false
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

func TestPrimaryMailRepairsWebTermsBeforeCreating(t *testing.T) {
	dir := t.TempDir()
	id := "alice@example.test"
	f := &fakePrimaryMail{authenticated: false, termsRequired: true}
	r := primaryMailAction(context.Background(), dir, id, "password", nil, false, f)
	s, err := mailsetup.Load(dir, id)
	if err != nil || !r.OK || s.Phase != "ready" || s.Email != "alice@icloud.com" || f.auths != 1 || f.pendingRepairs != 1 || f.repairs != 0 || f.creates != 1 {
		t.Fatalf("pending-repair case: got result=%+v state=%+v auths=%d pendingRepairs=%d repairs=%d creates=%d err=%v", r, s, f.auths, f.pendingRepairs, f.repairs, f.creates, err)
	}

	dirFallback := t.TempDir()
	fFallback := &fakePrimaryMail{authenticated: true, termsRequired: true}
	rFallback := primaryMailAction(context.Background(), dirFallback, id, "password", nil, false, fFallback)
	sFallback, err := mailsetup.Load(dirFallback, id)
	if err != nil || !rFallback.OK || sFallback.Phase != "ready" || sFallback.Email != "alice@icloud.com" || fFallback.pendingRepairs != 0 || fFallback.repairs != 1 || fFallback.creates != 1 {
		t.Fatalf("fallback case: got result=%+v state=%+v pendingRepairs=%d repairs=%d creates=%d err=%v", rFallback, sFallback, fFallback.pendingRepairs, fFallback.repairs, fFallback.creates, err)
	}
}
