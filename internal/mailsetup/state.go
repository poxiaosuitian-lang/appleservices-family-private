// Package mailsetup persists the per-account mailbox provisioning intent.
package mailsetup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type State struct {
	AutoCreate bool      `json:"auto_create"`
	AppleID    string    `json:"apple_id"`
	Phase      string    `json:"phase"`
	Email      string    `json:"email,omitempty"`
	Candidate  string    `json:"candidate,omitempty"`
	Submitted  bool      `json:"submitted"`
	Message    string    `json:"message"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func Load(dir, appleID string) (*State, error) {
	data, err := os.ReadFile(filepath.Join(dir, "primary-mail-status.json"))
	if err != nil {
		return nil, err
	}
	var s State
	if json.Unmarshal(data, &s) != nil || !strings.EqualFold(strings.TrimSpace(appleID), s.AppleID) || s.Phase == "" {
		return nil, errors.New("邮箱任务账号绑定无效")
	}
	return &s, nil
}

func Queue(dir, appleID string) error {
	if _, err := Load(dir, appleID); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return (&State{AutoCreate: true, AppleID: strings.TrimSpace(appleID), Phase: "pending", Message: "登录成功后自动检查 iCloud 邮箱；已有邮箱直接复用"}).Save(dir)
}

func (s *State) Save(dir string) error {
	s.UpdatedAt = time.Now().UTC()
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "primary-mail-status.json")
	if err = os.WriteFile(path+".tmp", data, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}
