package main

import (
	"context"
	"errors"
	"testing"

	"github.com/Laky-64/appleservices/icloud"
	"github.com/Laky-64/appleservices/internal/control"
)

func TestAutomaticForwardStartsOnlyAfterVerifiedSubscription(t *testing.T) {
	queue := make(chan struct{}, 1)
	for _, result := range []control.Result{
		{OK: true, Stage: "purchase-prepared"},
		{OK: true, Stage: "purchase-completed", Purchase: &icloud.PurchaseResult{State: "completed"}},
		{OK: false, Stage: "purchase-completed", Purchase: &icloud.PurchaseResult{State: "completed", QuotaVerified: true}},
		{OK: true, Stage: "purchase-verification", Purchase: &icloud.PurchaseResult{State: "verification-pending"}},
	} {
		queueHMEForwardAfterPurchase(result, queue)
		if len(queue) != 0 {
			t.Fatal("forwarding started before confirmed subscription")
		}
	}
	verified := control.Result{OK: true, Stage: "purchase-completed", Purchase: &icloud.PurchaseResult{State: "completed", QuotaVerified: true}}
	queueHMEForwardAfterPurchase(verified, queue)
	queueHMEForwardAfterPurchase(verified, queue)
	if len(queue) != 1 {
		t.Fatal("verified purchase not queued exactly once")
	}
}

type forwardFixture struct {
	info    *icloud.HideMyEmailInfo
	writes  int
	fail    bool
	applied bool
}

func (f *forwardFixture) List(context.Context) (*icloud.HideMyEmailInfo, error) { return f.info, nil }
func (f *forwardFixture) UpdateForwardTo(_ context.Context, target string) error {
	f.writes++
	if !f.fail || f.applied {
		f.info.SelectedForward = target
		for i := range f.info.Aliases {
			f.info.Aliases[i].ForwardToEmail = target
		}
	}
	if f.fail {
		return errors.New("lost response")
	}
	return nil
}
func forwardInfo() *icloud.HideMyEmailInfo {
	return &icloud.HideMyEmailInfo{ForwardToEmails: []string{"login@example.test", "own@icloud.com"}, SelectedForward: "login@example.test", Aliases: []icloud.HideMyEmailAlias{{Email: "alias@icloud.com", Active: true, ForwardToEmail: "login@example.test"}}}
}
func TestAutomaticForwardDefersUntilFirstAliasAndPreservesManualOverride(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	f := &forwardFixture{info: forwardInfo()}
	aliases := f.info.Aliases
	f.info.Aliases = nil
	if _, e := ensureHMEICloudForward(ctx, dir, "owner@example.test", f, f.info); e != nil || f.writes != 0 {
		t.Fatal("empty account mutated")
	}
	f.info.Aliases = aliases
	if _, e := ensureHMEICloudForward(ctx, dir, "owner@example.test", f, f.info); e != nil || f.writes != 1 || !hmeForwardMatches(f.info, "own@icloud.com") {
		t.Fatal("first alias not configured")
	}
	f.info.SelectedForward = "manual@example.test"
	f.info.Aliases[0].ForwardToEmail = f.info.SelectedForward
	if _, e := ensureHMEICloudForward(ctx, dir, "owner@example.test", f, f.info); e != nil || f.writes != 1 {
		t.Fatal("manual setting overwritten")
	}
	if _, e := ensureHMEICloudForward(ctx, dir, "different@example.test", f, f.info); e == nil || f.writes != 1 {
		t.Fatal("account binding ignored")
	}
}
func TestAutomaticForwardReconcilesLostResponseWithoutReplay(t *testing.T) {
	for _, applied := range []bool{false, true} {
		ctx := context.Background()
		dir := t.TempDir()
		f := &forwardFixture{info: forwardInfo(), fail: true, applied: applied}
		_, e := ensureHMEICloudForward(ctx, dir, "owner@example.test", f, f.info)
		if (e == nil) != applied {
			t.Fatal("incorrect reconciliation")
		}
		_, _ = ensureHMEICloudForward(ctx, dir, "owner@example.test", f, f.info)
		if f.writes != 1 {
			t.Fatal("uncertain write replayed")
		}
	}
}
func TestAutomaticForwardDoesNotGuess(t *testing.T) {
	for _, emails := range [][]string{{"login@example.test"}, {"one@icloud.com", "two@icloud.com"}, {"alias@icloud.com"}, {"fake@icloud.com.example.test"}, {"@icloud.com"}} {
		f := &forwardFixture{info: forwardInfo()}
		f.info.ForwardToEmails = emails
		if _, e := ensureHMEICloudForward(context.Background(), t.TempDir(), "owner@example.test", f, f.info); e == nil || f.writes != 0 {
			t.Fatal("unverified or ambiguous target selected")
		}
	}
}
