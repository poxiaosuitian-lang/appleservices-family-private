//go:build windows

package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Laky-64/appleservices/icloud"
)

type savedSecurityAnswer struct {
	Question icloud.SecurityQuestion
	Answer   string
}
type protectedSecurityAnswers struct {
	Version        int
	AppleID, ADSID string
	Entries        []savedSecurityAnswer
	Blocked        bool
	BlockedReason  string
	Birthday       string `json:"-"`
}
type accountSecurityAnswers struct {
	store   fileStore
	appleID string
}

func securityAnswersEntropy(id, adsid string) []byte {
	h := sha256.Sum256([]byte("appleservices-security-answers-v1\x00" + strings.ToLower(strings.TrimSpace(id)) + "\x00" + adsid))
	return h[:]
}
func (p accountSecurityAnswers) load() (*protectedSecurityAnswers, error) {
	s, err := p.store.LoadSession()
	if err != nil || s == nil || s.DSID == "" || !strings.EqualFold(s.AppleID, p.appleID) {
		return nil, errors.New("安全问题账号绑定无效")
	}
	data, err := os.ReadFile(filepath.Join(p.store.dir, "security-answers.dpapi"))
	if os.IsNotExist(err) {
		saved := &protectedSecurityAnswers{Version: 1, AppleID: s.AppleID, ADSID: s.DSID}
		return saved, p.loadBirthday(saved)
	}
	if err != nil {
		return nil, errors.New("无法读取加密安全问题")
	}
	var encrypted []byte
	if len(data) > 64<<10 || json.Unmarshal(data, &encrypted) != nil {
		return nil, errors.New("加密安全问题格式无效")
	}
	plain, err := protectStoreData(encrypted, securityAnswersEntropy(p.appleID, s.DSID), true)
	if err != nil {
		return nil, errors.New("无法使用此 Windows 用户解密安全问题")
	}
	defer clear(plain)
	var saved protectedSecurityAnswers
	if json.Unmarshal(plain, &saved) != nil || saved.Version != 1 || !strings.EqualFold(saved.AppleID, p.appleID) || saved.ADSID != s.DSID || len(saved.Entries) > 24 {
		return nil, errors.New("安全问题账号绑定已改变")
	}
	return &saved, p.loadBirthday(&saved)
}

// Keep the new field in a separate encrypted record so an older running
// worker updating question answers cannot discard the saved birthday.
func (p accountSecurityAnswers) loadBirthday(s *protectedSecurityAnswers) error {
	data, err := os.ReadFile(filepath.Join(p.store.dir, "security-birthday.dpapi"))
	if os.IsNotExist(err) {
		return nil
	}
	var encrypted []byte
	if err != nil || len(data) > 4096 || json.Unmarshal(data, &encrypted) != nil {
		return errors.New("无法读取加密生日")
	}
	plain, err := protectStoreData(encrypted, securityAnswersEntropy(p.appleID, s.ADSID), true)
	if err != nil {
		return errors.New("无法解密本账号生日")
	}
	defer clear(plain)
	date, err := time.Parse("2006-01-02", string(plain))
	if err != nil || date.Year() < 1900 || date.After(time.Now()) {
		return errors.New("加密生日格式无效")
	}
	s.Birthday = string(plain)
	return nil
}

func (p accountSecurityAnswers) save(s *protectedSecurityAnswers) error {
	// The answer record stays compatible with old workers; the birthday uses
	// the same Apple ID / DSID binding in its own DPAPI record.
	answers := *s
	answers.Birthday = ""
	plain, err := json.Marshal(&answers)
	if err != nil {
		return err
	}
	defer clear(plain)
	encrypted, err := protectStoreData(plain, securityAnswersEntropy(p.appleID, s.ADSID), false)
	if err != nil {
		return err
	}
	if err = writeJSONFile(filepath.Join(p.store.dir, "security-answers.dpapi"), encrypted); err != nil {
		return err
	}
	if s.Birthday != "" {
		birthday := []byte(s.Birthday)
		defer clear(birthday)
		protected, err := protectStoreData(birthday, securityAnswersEntropy(p.appleID, s.ADSID), false)
		if err != nil {
			return errors.New("无法加密生日")
		}
		if err := writeJSONFile(filepath.Join(p.store.dir, "security-birthday.dpapi"), protected); err != nil {
			return errors.New("无法保存加密生日")
		}
	} else if err := os.Remove(filepath.Join(p.store.dir, "security-birthday.dpapi")); err != nil && !os.IsNotExist(err) {
		return errors.New("无法清除加密生日")
	}
	// Only the UI-safe question catalog/status is written outside DPAPI.
	return writeJSONFile(filepath.Join(p.store.dir, "security-questions-status.json"), struct {
		AppleID string                         `json:"apple_id"`
		Status  icloud.SecurityQuestionsStatus `json:"status"`
	}{p.appleID, securityAnswersStatus(s)})
}
func securityAnswersStatus(s *protectedSecurityAnswers) icloud.SecurityQuestionsStatus {
	status := icloud.SecurityQuestionsStatus{BirthdaySaved: s.Birthday != "", Blocked: s.Blocked, BlockedReason: s.BlockedReason, Questions: []icloud.SecurityQuestionStatus{}}
	for _, e := range s.Entries {
		status.Questions = append(status.Questions, icloud.SecurityQuestionStatus{SecurityQuestion: e.Question, Saved: e.Answer != ""})
	}
	return status
}
func (p accountSecurityAnswers) Answers(_ context.Context, questions []icloud.SecurityQuestion) ([]icloud.SecurityQuestionAnswer, error) {
	s, err := p.load()
	if err != nil {
		return nil, err
	}
	for _, q := range questions {
		found := false
		for i := range s.Entries {
			if s.Entries[i].Question.ID == q.ID {
				s.Entries[i].Question = q
				found = true
				break
			}
		}
		if !found {
			s.Entries = append(s.Entries, savedSecurityAnswer{Question: q})
		}
	}
	if len(s.Entries) > 24 {
		return nil, errors.New("安全问题目录超出预期")
	}
	if err := p.save(s); err != nil {
		return nil, errors.New("无法保存安全问题目录")
	}
	if s.Blocked {
		if s.BlockedReason == "additional-verification" {
			return nil, errors.New("答案已保存，Apple 要求继续完成账号安全验证；完成后点击“读取验证问题”继续")
		}
		if s.BlockedReason != "rejected" {
			return nil, errors.New("上次安全问题验证未完成，已暂停自动提交；请在面板检查验证状态")
		}
		return nil, icloud.ErrSecurityAnswersRejected
	}
	answers := []icloud.SecurityQuestionAnswer{}
	for _, q := range questions {
		for _, e := range s.Entries {
			if e.Question.ID == q.ID && e.Answer != "" {
				answers = append(answers, icloud.SecurityQuestionAnswer{ID: q.ID, Answer: e.Answer})
			}
		}
	}
	if len(answers) != len(questions) {
		return nil, icloud.ErrSecurityAnswersMissing
	}
	return answers, nil
}
func (p accountSecurityAnswers) Pause(_ context.Context, reason string) error {
	s, err := p.load()
	if err != nil {
		return err
	}
	s.Blocked = true
	s.BlockedReason = reason
	return p.save(s)
}

// Explicit checks may resume additional/ambiguous challenges. Incorrect
// answers still require correction, including across restarts.
func (p accountSecurityAnswers) resume() error {
	s, err := p.load()
	if err != nil {
		return err
	}
	if s.Blocked && s.BlockedReason == "rejected" {
		return icloud.ErrSecurityAnswersRejected
	}
	s.Blocked = false
	s.BlockedReason = ""
	return p.save(s)
}
func (p accountSecurityAnswers) update(answers []icloud.SecurityQuestionAnswer, forget bool) error {
	return p.updateWithBirthday(answers, "", forget)
}

func (p accountSecurityAnswers) updateWithBirthday(answers []icloud.SecurityQuestionAnswer, birthday string, forget bool) error {
	if birthday != "" {
		date, err := time.Parse("2006-01-02", birthday)
		if err != nil || date.Year() < 1900 || date.After(time.Now()) {
			return errors.New("请输入有效的出生日期（年-月-日）")
		}
	}
	s, err := p.load()
	if err != nil {
		return err
	}
	if forget {
		s.Birthday = ""
		for i := range s.Entries {
			s.Entries[i].Answer = ""
		}
		s.Blocked = false
		s.BlockedReason = ""
		return p.save(s)
	}
	if (len(answers) == 0 && birthday == "") || len(answers) > 24 {
		return errors.New("请填写需要保存的答案或生日")
	}
	if birthday != "" {
		s.Birthday = birthday
	}
	// Saving a birthday must not resume rejected or ambiguous question answers.
	if len(answers) == 0 {
		return p.save(s)
	}
	seen := map[int]bool{}
	changed := false
	for _, a := range answers {
		if seen[a.ID] || strings.TrimSpace(a.Answer) == "" || len(a.Answer) > 512 {
			return errors.New("安全问题答案不能为空或超过 512 字节")
		}
		seen[a.ID] = true
		found := false
		for i := range s.Entries {
			if s.Entries[i].Question.ID == a.ID {
				changed = changed || s.Entries[i].Answer != a.Answer
				s.Entries[i].Answer = a.Answer
				found = true
				break
			}
		}
		if !found {
			return errors.New("安全问题不属于当前账号，请先读取 Apple 返回的问题")
		}
	}
	if s.Blocked && s.BlockedReason == "rejected" && !changed {
		return errors.New("请修正被拒绝的答案后再启用自动验证")
	}
	s.Blocked = false
	s.BlockedReason = ""
	return p.save(s)
}
