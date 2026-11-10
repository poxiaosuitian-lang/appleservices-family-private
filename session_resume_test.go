package appleservices

import (
	"errors"
	"testing"

	"github.com/Laky-64/appleservices/gsa"
	"github.com/Laky-64/appleservices/icloud"
)

type checkpointStore struct {
	memoryStore
	accepted *RestoredICloudSession
	writeErr error
}

func (s *checkpointStore) SaveAcceptedICloud(appleID string, accepted RestoredICloudSession) error {
	if s.writeErr != nil {
		return s.writeErr
	}
	if s.session == nil || appleID != s.session.AppleID || accepted.ADSID != s.session.DSID {
		return errors.New("account binding mismatch")
	}
	s.accepted = &accepted
	return nil
}

func verifiedTestSession() *Session {
	return &Session{AppleID: "owner@example.test", DSID: "test-adsid", DeviceIdentityToken: "test-idms", Tokens: map[string]string{
		"com.apple.gs.idms.pet": "test-pet", "com.apple.gs.idms.hb": "test-hb", "com.apple.gs.appleid.auth": "test-auth",
	}}
}

type passwordResumeStore struct {
	memoryStore
	password string
	loadErr  error
	loads    int
}

func (s *passwordResumeStore) LoadLoginPassword(string) (string, error) {
	s.loads++
	return s.password, s.loadErr
}

func TestResumeUsesProtectedPasswordOnlyAfterAccountBinding(t *testing.T) {
	store := &passwordResumeStore{memoryStore: memoryStore{session: verifiedTestSession()}, password: "synthetic-remembered-password"}
	login, err := ResumeVerifiedLogin("owner@example.test", store, testOptions(&fakeBackend{}, testDelegate)...)
	if err != nil || login.creds.Password != store.password || store.loads != 1 {
		t.Fatal("bound encrypted credential was not available for reconnect")
	}
	if _, err := ResumeVerifiedLogin("other@example.test", store, testOptions(&fakeBackend{}, testDelegate)...); err == nil || store.loads != 1 {
		t.Fatal("cross-account password was read")
	}
	store.loadErr = errors.New("cannot decrypt")
	login, err = ResumeVerifiedLogin("owner@example.test", store, testOptions(&fakeBackend{}, testDelegate)...)
	if err != nil || login.creds.Password != "" {
		t.Fatal("missing credential should retain token-only resume")
	}
}

func TestLegacyVerifiedResumeRequiresAppleDelegateAcceptance(t *testing.T) {
	for _, refused := range []bool{false, true} {
		store := &checkpointStore{memoryStore: memoryStore{session: verifiedTestSession()}}
		backend := &fakeBackend{}
		calls := 0
		fetch := func(h map[string]string, id, adsid, pet string) (icloud.DelegateTokens, error) {
			calls++
			if id != "owner@example.test" || adsid != "test-adsid" || pet != "test-pet" {
				t.Fatal("wrong delegate binding")
			}
			if refused {
				return icloud.DelegateTokens{}, errors.New("refused")
			}
			return icloud.DelegateTokens{DSID: "123", MMEAuthToken: "mme", ServiceData: map[string]any{}}, nil
		}
		login, err := ResumeVerifiedLogin("owner@example.test", store, testOptions(backend, fetch)...)
		if err != nil {
			t.Fatal(err)
		}
		if store.accepted != nil || login.client != nil {
			t.Fatal("saved state alone declared iCloud ready")
		}
		_, err = login.Client()
		if (err != nil) != refused || calls != 1 || backend.calls != 0 || len(backend.requests) != 0 {
			t.Fatal("legacy resume bypassed acceptance or started password/OTP login")
		}
		if (store.accepted == nil) != refused {
			t.Fatal("delegate checkpoint not gated by Apple acceptance")
		}
	}
}

func TestDelegateCheckpointWriteFailureIsReported(t *testing.T) {
	store := &checkpointStore{memoryStore: memoryStore{session: verifiedTestSession()}, writeErr: errors.New("disk full")}
	login, err := ResumeVerifiedLogin("owner@example.test", store, testOptions(&fakeBackend{}, testDelegate)...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := login.Client(); err == nil || login.client != nil {
		t.Fatal("failed checkpoint was reported ready")
	}
}

func TestLocalTwoFactorOnlyUsesBoundExistingDeviceChallengeOnce(t *testing.T) {
	for _, name := range []string{"accepted", "rejected", "new-account", "other-account", "other-adsid", "sms", "missing-identity"} {
		t.Run(name, func(t *testing.T) {
			store := &memoryStore{session: verifiedTestSession()}
			first := testResult(true)
			first.TwoFactorMethod = "trustedDeviceSecondaryAuth"
			backend := &fakeBackend{results: []*gsa.LoginResult{first, testResult(false)}}
			switch name {
			case "rejected":
				backend.submitErr = errors.New("rejected")
			case "new-account":
				store.session = nil
			case "other-account":
				store.session.AppleID = "other@example.test"
			case "other-adsid":
				store.session.DSID = "other"
			case "sms":
				first.TwoFactorMethod = "secondaryAuth"
			case "missing-identity":
				store.session.DeviceIdentityToken = ""
			}
			login, err := BeginLogin(Credentials{"owner@example.test", "test-password"}, store, testOptions(backend, testDelegate)...)
			if err != nil {
				t.Fatal(err)
			}
			generated := 0
			provider := func() (string, error) { generated++; return "123456", nil }
			attempted, err := login.CompleteLocalTwoFactor(provider)
			wantAttempt := name == "accepted" || name == "rejected"
			if attempted != wantAttempt || (err != nil) != (name == "rejected") {
				t.Fatal("unexpected local verification result")
			}
			login.CompleteLocalTwoFactor(provider)
			wantCount := 0
			if wantAttempt {
				wantCount = 1
			}
			if generated != wantCount || len(backend.requests) != wantCount || len(backend.submits) != wantCount {
				t.Fatal("cross-account code use or automatic retry")
			}
			if login.NeedsTwoFactor() == (name == "accepted") {
				t.Fatal("challenge completion was assumed")
			}
		})
	}
}
