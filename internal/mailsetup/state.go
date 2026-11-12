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
	var checkpoint struct {
		Submitted *bool `json:"submitted"`
	}
	if json.Unmarshal(data, &s) != nil || json.Unmarshal(data, &checkpoint) != nil || checkpoint.Submitted == nil ||
		strings.TrimSpace(appleID) == "" || !strings.EqualFold(strings.TrimSpace(appleID), s.AppleID) {
		return nil, errors.New("邮箱任务账号绑定无效")
	}
	switch s.Phase {
	case "pending", "checking", "blocked", "missing", "ready":
	case "submitted", "unconfirmed":
		if !s.Submitted {
			return nil, errors.New("邮箱任务提交标记不完整，已停止重复创建")
		}
	default:
		return nil, errors.New("邮箱任务状态无效")
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
	file, err := os.CreateTemp(dir, ".primary-mail-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err == nil {
		// Activation must not start until its submission checkpoint has reached
		// storage. Closing a buffered file alone does not provide that guarantee.
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), path)
}
