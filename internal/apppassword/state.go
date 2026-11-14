// Package apppassword stores only public provisioning progress, never credentials.
package apppassword

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Status struct {
	State     string    `json:"state"`
	Message   string    `json:"message"`
	Label     string    `json:"label,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type checkpoint struct {
	AppleID string `json:"apple_id"`
	Status  Status `json:"status"`
}

func Load(dir, appleID string) (*Status, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "app-password-status.json"))
	if err != nil {
		return nil, err
	}
	var saved checkpoint
	if json.Unmarshal(raw, &saved) != nil || strings.TrimSpace(appleID) == "" || !strings.EqualFold(saved.AppleID, strings.TrimSpace(appleID)) {
		return nil, errors.New("应用专用密码状态账号不匹配")
	}
	switch saved.Status.State {
	case "pending", "ready", "verification_required", "unknown", "failed", "not_subscribed":
	default:
		return nil, errors.New("应用专用密码状态无效")
	}
	return &saved.Status, nil
}

func Save(dir, appleID string, status *Status) error {
	if strings.TrimSpace(appleID) == "" || status == nil {
		return errors.New("应用专用密码账号绑定缺失")
	}
	status.UpdatedAt = time.Now().UTC()
	raw, err := json.Marshal(checkpoint{AppleID: strings.TrimSpace(appleID), Status: *status})
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".app-password-status-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), filepath.Join(dir, "app-password-status.json"))
}
