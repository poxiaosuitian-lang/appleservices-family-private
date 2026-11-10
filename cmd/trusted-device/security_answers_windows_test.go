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
