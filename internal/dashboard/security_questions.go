package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/Laky-64/appleservices/icloud"
)

// This projection never opens the DPAPI file and cannot return answer values.
func readSecurityQuestionsStatus(account *Account) *icloud.SecurityQuestionsStatus {
	data, err := os.ReadFile(filepath.Join(account.Dir, "security-questions-status.json"))
	if err != nil || len(data) > 64<<10 {
		return nil
	}
	var saved struct {
		AppleID string                         `json:"apple_id"`
		Status  icloud.SecurityQuestionsStatus `json:"status"`
	}
	if json.Unmarshal(data, &saved) != nil || !strings.EqualFold(saved.AppleID, account.AppleID) {
		return nil
	}
	return &saved.Status
}
