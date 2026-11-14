package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/Laky-64/appleservices/icloud"
	"github.com/Laky-64/appleservices/internal/control"
)

type hmeForwardClient interface {
	List(context.Context) (*icloud.HideMyEmailInfo, error)
	UpdateForwardTo(context.Context, string) error
}

func queueHMEForwardAfterPurchase(result control.Result, queue chan<- struct{}) {
	if !result.OK || result.Stage != "purchase-completed" || result.Purchase == nil || result.Purchase.State != "completed" || !result.Purchase.QuotaVerified {
		return
	}
	select {
	case queue <- struct{}{}:
	default:
	}
}

type hmeForwardState struct {
	AppleID string `json:"apple_id"`
	Target  string `json:"target"`
	Done    bool   `json:"done"`
}

func ownICloudForwardTarget(info *icloud.HideMyEmailInfo) string {
	if info == nil {
		return ""
	}
	targets := map[string]bool{}
	for _, email := range info.ForwardToEmails {
		email = strings.ToLower(strings.TrimSpace(email))
		if strings.Count(email, "@") != 1 || !strings.HasSuffix(email, "@icloud.com") || email == "@icloud.com" {
			continue
		}
		alias := false
		for _, item := range info.Aliases {
			if strings.EqualFold(item.Email, email) {
				alias = true
			}
		}
		if !alias {
			targets[email] = true
		}
	}
	if len(targets) == 1 {
		for target := range targets {
			return target
		}
	}
	return ""
}

func hmeForwardMatches(info *icloud.HideMyEmailInfo, target string) bool {
	if info == nil || !strings.EqualFold(info.SelectedForward, target) {
		return false
	}
	for _, item := range info.Aliases {
		if item.Active && !strings.EqualFold(item.ForwardToEmail, target) {
			return false
		}
	}
	return true
}

// Persist intent before updating. Restarts reconcile an uncertain result with
// a read, never replay it. A completed setup preserves later manual changes.
func ensureHMEICloudForward(ctx context.Context, dir, appleID string, client hmeForwardClient, info *icloud.HideMyEmailInfo) (*icloud.HideMyEmailInfo, error) {
	path := filepath.Join(dir, "hme-forward.json")
	var saved hmeForwardState
	data, err := os.ReadFile(path)
	if err == nil {
		if json.Unmarshal(data, &saved) != nil || !strings.EqualFold(saved.AppleID, appleID) || saved.Target == "" {
			return info, errors.New("转发任务账号绑定无效，请手动核对转发目标")
		}
		if saved.Done {
			return info, nil
		}
	} else if !os.IsNotExist(err) {
		return info, errors.New("无法读取转发任务")
	}
	// Apple has no forwarding identity until the first alias is reserved.
	if info == nil || len(info.Aliases) == 0 {
		return info, nil
	}
	target := ownICloudForwardTarget(info)
	if target == "" {
		return info, errors.New("无法唯一识别本账号的 iCloud 邮箱，请手动选择转发目标")
	}
	if saved.Target != "" && (!strings.EqualFold(saved.Target, target) || !hmeForwardMatches(info, target)) {
		return info, errors.New("上次转发设置尚未确认，请在转发设置中核对；不会自动重复提交")
	}
	saved = hmeForwardState{AppleID: appleID, Target: target}
	if !hmeForwardMatches(info, target) {
		if ctx.Err() != nil {
			return info, errors.New("转发检查已超时，请重新读取地址")
		}
		if err := writeJSONFile(path, saved); err != nil {
			return info, errors.New("无法保存转发任务，未修改转发目标")
		}
		_ = client.UpdateForwardTo(ctx, target)
		readback, err := client.List(ctx)
		if err != nil {
			return info, errors.New("转发结果尚未确认，请重新读取地址；不要重复创建")
		}
		info = readback
		if !hmeForwardMatches(info, target) {
			return info, errors.New("地址已保留，但转发尚未切换到 iCloud 邮箱，请手动核对")
		}
	}
	saved.Done = true
	if err := writeJSONFile(path, saved); err != nil {
		return info, errors.New("转发已确认，但任务状态保存失败")
	}
	return info, nil
}

func automaticHMEForwardResult(ctx context.Context, dir, appleID string, client hmeForwardClient, info *icloud.HideMyEmailInfo, message, stage string) control.Result {
	info, err := ensureHMEICloudForward(ctx, dir, appleID, client, info)
	if err != nil {
		message += "；" + err.Error()
		stage = "hme-forward-pending"
	}
	return control.Result{OK: true, Message: message, Stage: stage, HideMyEmail: info}
}
