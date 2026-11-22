package main

import (
	"context"
	"errors"
	"testing"

	"github.com/Laky-64/appleservices/icloud"
)

type syncWebFixture struct {
	info             *icloud.HideMyEmailInfo
	listErr, authErr error
	lists, auths     int
}

func (f *syncWebFixture) List(context.Context) (*icloud.HideMyEmailInfo, error) {
	f.lists++
	return f.info, f.listErr
}

func (f *syncWebFixture) Authenticate(context.Context, string, func(context.Context) (string, error)) error {
	f.auths++
	if f.authErr == nil {
		f.listErr = nil
	}
	return f.authErr
}

func TestSyncWebSessionWithoutSubscriptionOrAliases(t *testing.T) {
	for _, expired := range []bool{false, true} {
		client := &syncWebFixture{info: &icloud.HideMyEmailInfo{ForwardToEmails: []string{"owner@icloud.com"}}}
		if expired {
			client.listErr = icloud.ErrHMEAuthenticationRequired
		}
		info, err := loadSyncHMESession(context.Background(), client, "synthetic", nil)
		if err != nil || info != client.info {
			t.Fatal("valid empty Web session rejected", err)
		}
		mailbox, err := serverSyncMailbox(info)
		if err != nil || mailbox != "owner@icloud.com" {
			t.Fatal("empty alias list must not require forwarding setup", err)
		}
		if expired && (client.auths != 1 || client.lists != 2) || !expired && (client.auths != 0 || client.lists != 1) {
			t.Fatal("unexpected authentication or read count")
		}
	}
}

func TestSyncWebSessionStillRequiresAppleVerification(t *testing.T) {
	denied := errors.New("session rejected")
	for _, client := range []*syncWebFixture{
		{listErr: denied},
		{listErr: icloud.ErrHMEAuthenticationRequired, authErr: denied},
	} {
		if _, err := loadSyncHMESession(context.Background(), client, "synthetic", nil); !errors.Is(err, denied) {
			t.Fatal("session failure was bypassed", err)
		}
	}
}

func TestSyncMailboxRejectsAmbiguousOrMisroutedAccounts(t *testing.T) {
	for _, info := range []*icloud.HideMyEmailInfo{
		nil,
		{},
		{ForwardToEmails: []string{"one@icloud.com", "two@icloud.com"}},
		{ForwardToEmails: []string{"alias@icloud.com"}, Aliases: []icloud.HideMyEmailAlias{{Email: "alias@icloud.com"}}},
		{ForwardToEmails: []string{"owner@icloud.com"}, SelectedForward: "other@example.test", Aliases: []icloud.HideMyEmailAlias{{Active: true, ForwardToEmail: "other@example.test"}}},
		{ForwardToEmails: []string{"owner@icloud.com"}, SelectedForward: "owner@icloud.com", Aliases: []icloud.HideMyEmailAlias{{Active: true, ForwardToEmail: "other@example.test"}}},
	} {
		if _, err := serverSyncMailbox(info); err == nil {
			t.Fatal("unverified mailbox or incorrect active forwarding accepted")
		}
	}
	info := &icloud.HideMyEmailInfo{ForwardToEmails: []string{"owner@icloud.com"}, SelectedForward: "owner@icloud.com", Aliases: []icloud.HideMyEmailAlias{{Active: true, ForwardToEmail: "owner@icloud.com"}}}
	if _, err := serverSyncMailbox(info); err != nil {
		t.Fatal("valid active forwarding rejected", err)
	}
}

type syncRepairFixture struct {
	syncWebFixture
	pendingErr, authRepairErr error
	pendingCalls, authRepairs int
}

func (f *syncRepairFixture) AcceptPendingTerms(context.Context) error {
	f.pendingCalls++
	if f.pendingErr == nil {
		f.listErr = nil
	}
	return f.pendingErr
}

func (f *syncRepairFixture) AuthenticateAndAcceptTerms(context.Context, string, func(context.Context) (string, error)) error {
	f.authRepairs++
	if f.authRepairErr == nil {
		f.listErr = nil
	}
	return f.authRepairErr
}

func TestSyncWebSessionAcceptsPendingWebTermsAutomatically(t *testing.T) {
	client := &syncRepairFixture{
		syncWebFixture: syncWebFixture{
			info:    &icloud.HideMyEmailInfo{ForwardToEmails: []string{"owner@icloud.com"}},
			listErr: icloud.ErrHMEAuthenticationRequired,
			authErr: icloud.ErrHMEWebRepairRequired,
		},
		pendingErr: icloud.ErrHMEWebRepairSessionUnavailable,
	}
	info, err := loadSyncHMESession(context.Background(), client, "synthetic", nil)
	if err != nil || info != client.info {
		t.Fatalf("expected Web repair to succeed, got info=%v err=%v", info, err)
	}
	if client.auths != 1 || client.pendingCalls != 1 || client.authRepairs != 1 || client.lists != 2 {
		t.Fatalf("unexpected call counts: auths=%d pending=%d authRepairs=%d lists=%d", client.auths, client.pendingCalls, client.authRepairs, client.lists)
	}
}
