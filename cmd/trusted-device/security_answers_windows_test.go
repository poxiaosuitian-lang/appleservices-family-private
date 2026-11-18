//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Laky-64/appleservices"
	"github.com/Laky-64/appleservices/icloud"
)

func TestSecurityAnswersEncryptedBoundAndReused(t *testing.T) {
	f := fileStore{dir: t.TempDir()}
	if err := f.SaveSession(&appleservices.Session{AppleID: "owner@example.test", DSID: "synthetic-adid"}); err != nil {
		t.Fatal(err)
	}
	p := accountSecurityAnswers{store: f, appleID: "owner@example.test"}
	questions := []icloud.SecurityQuestion{{ID: 136, Question: "Question one?"}, {ID: 142, Question: "Question two?"}}
	if _, err := p.Answers(context.Background(), questions); !errors.Is(err, icloud.ErrSecurityAnswersMissing) {
		t.Fatal("unconfigured answers accepted")
	}
	values := []icloud.SecurityQuestionAnswer{{ID: 142, Answer: "synthetic-answer-two"}, {ID: 136, Answer: "synthetic-answer-one"}}
	if err := p.update(values, false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"security-answers.dpapi", "security-questions-status.json"} {
		data, err := os.ReadFile(filepath.Join(f.dir, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range values {
			if bytes.Contains(data, []byte(v.Answer)) {
				t.Fatal("plaintext answer persisted")
			}
		}
	}
	// A fresh provider after restart restores exactly the requested order.
	p = accountSecurityAnswers{store: f, appleID: "owner@example.test"}
	got, err := p.Answers(context.Background(), questions)
	if err != nil || got[0].ID != 136 || got[0].Answer != values[1].Answer {
		t.Fatal("encrypted answers not restored by question ID")
	}
	if err := p.Pause(context.Background(), "rejected"); err != nil {
		t.Fatal(err)
	}
	p = accountSecurityAnswers{store: f, appleID: "owner@example.test"}
	if _, err := p.Answers(context.Background(), questions); !errors.Is(err, icloud.ErrSecurityAnswersRejected) {
		t.Fatal("rejection did not survive restart")
	}
	if err := p.resume(); !errors.Is(err, icloud.ErrSecurityAnswersRejected) {
		t.Fatal("manual check re-enabled known wrong answers")
	}
	if err := p.update(values, false); err == nil {
		t.Fatal("unchanged wrong answers re-enabled")
	}
	values[0].Answer = "synthetic-corrected-two"
	if err := p.update(values, false); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Answers(context.Background(), questions); err != nil {
		t.Fatal("corrected answers still blocked")
	}
	if err := p.Pause(context.Background(), "additional-verification"); err != nil {
		t.Fatal(err)
	}
	if err := p.resume(); err != nil {
		t.Fatal("explicit continuation after additional verification failed")
	}
	// Copying ciphertext to a different account / changing ADSID must fail.
	if err := f.SaveSession(&appleservices.Session{AppleID: "other@example.test", DSID: "other-adid"}); err != nil {
		t.Fatal(err)
	}
	other := accountSecurityAnswers{store: f, appleID: "other@example.test"}
	if _, err := other.load(); err == nil {
		t.Fatal("cross-account ciphertext accepted")
	}
	if err := f.SaveSession(&appleservices.Session{AppleID: "owner@example.test", DSID: "synthetic-adid"}); err != nil {
		t.Fatal(err)
	}
	if err := p.update(nil, true); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Answers(context.Background(), questions); !errors.Is(err, icloud.ErrSecurityAnswersMissing) {
		t.Fatal("forget retained answers")
	}
	if err := p.update([]icloud.SecurityQuestionAnswer{{ID: 999, Answer: "test"}}, false); err == nil {
		t.Fatal("unknown question accepted")
	}
	// Tampering is rejected, never treated as an empty credential.
	path := filepath.Join(f.dir, "security-answers.dpapi")
	data, _ := os.ReadFile(path)
	var encrypted []byte
	json.Unmarshal(data, &encrypted)
	encrypted[len(encrypted)-1] ^= 1
	writeJSONFile(path, encrypted)
	if _, err := p.load(); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
}

func TestSecurityBirthdayEncryptedAndIndependentOfQuestionVerification(t *testing.T) {
	f := fileStore{dir: t.TempDir()}
	if err := f.SaveSession(&appleservices.Session{AppleID: "owner@example.test", DSID: "synthetic-adid"}); err != nil {
		t.Fatal(err)
	}
	p := accountSecurityAnswers{store: f, appleID: "owner@example.test"}
	const birthday = "2000-02-29"
	if err := p.updateWithBirthday(nil, birthday, false); err != nil {
		t.Fatal(err)
	}
	got, err := p.load()
	if err != nil || got.Birthday != birthday || !securityAnswersStatus(got).BirthdaySaved {
		t.Fatal("birthday was not restored")
	}
	for _, name := range []string{"security-answers.dpapi", "security-birthday.dpapi", "security-questions-status.json"} {
		data, err := os.ReadFile(filepath.Join(f.dir, name))
		if err != nil || bytes.Contains(data, []byte(birthday)) {
			t.Fatal("birthday missing or exposed in plaintext")
		}
	}
	for _, invalid := range []string{"2001-02-29", "2000/2/29", "1899-12-31", "9999-01-01"} {
		if err := p.updateWithBirthday(nil, invalid, false); err == nil {
			t.Fatal("invalid birthday accepted")
		}
	}
	questions := []icloud.SecurityQuestion{{ID: 136, Question: "Fixture?"}}
	_, _ = p.Answers(context.Background(), questions)
	if err := p.update([]icloud.SecurityQuestionAnswer{{ID: 136, Answer: "synthetic-answer"}}, false); err != nil {
		t.Fatal(err)
	}
	if err := p.Pause(context.Background(), "rejected"); err != nil {
		t.Fatal(err)
	}
	if err := p.updateWithBirthday(nil, "2000-03-01", false); err != nil {
		t.Fatal(err)
	}
	got, err = p.load()
	if err != nil || !got.Blocked || got.Entries[0].Answer != "synthetic-answer" || got.Birthday != "2000-03-01" {
		t.Fatal("birthday edit changed verification guard or answers")
	}
	// An old worker can rewrite its answer file without erasing the new field.
	plain, _ := json.Marshal(protectedSecurityAnswers{Version: 1, AppleID: p.appleID, ADSID: "synthetic-adid"})
	encrypted, err := protectStoreData(plain, securityAnswersEntropy(p.appleID, "synthetic-adid"), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONFile(filepath.Join(f.dir, "security-answers.dpapi"), encrypted); err != nil {
		t.Fatal(err)
	}
	got, err = p.load()
	if err != nil || got.Birthday != "2000-03-01" {
		t.Fatal("old answer writer erased birthday")
	}
	// Even a valid answer record cannot decrypt a different account's birthday.
	otherDir := t.TempDir()
	otherStore := fileStore{dir: otherDir}
	if err := otherStore.SaveSession(&appleservices.Session{AppleID: "other@example.test", DSID: "other-adid"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(f.dir, "security-birthday.dpapi"))
	if err := os.WriteFile(filepath.Join(otherDir, "security-birthday.dpapi"), data, 0600); err != nil {
		t.Fatal(err)
	}
	other := accountSecurityAnswers{store: otherStore, appleID: "other@example.test"}
	if _, err := other.load(); err == nil {
		t.Fatal("cross-account birthday decrypted")
	}
	if err := p.update(nil, true); err != nil {
		t.Fatal(err)
	}
	got, err = p.load()
	if err != nil || got.Birthday != "" || securityAnswersStatus(got).BirthdaySaved {
		t.Fatal("clear retained birthday")
	}
	if _, err := os.Stat(filepath.Join(f.dir, "security-birthday.dpapi")); !os.IsNotExist(err) {
		t.Fatal("clear retained ciphertext")
	}
}
