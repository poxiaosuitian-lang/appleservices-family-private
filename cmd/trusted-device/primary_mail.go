package main

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/Laky-64/appleservices/icloud"
	"github.com/Laky-64/appleservices/internal/control"
	"github.com/Laky-64/appleservices/internal/mailsetup"
)

type primaryMailbox interface {
	Check(context.Context) (*icloud.PrimaryMailInfo, error)
	Authenticate(context.Context, string, func(context.Context) (string, error)) error
	Ensure(context.Context, bool, func(string) error) (*icloud.PrimaryMailInfo, error)
}

func primaryMailAction(ctx context.Context, dir, appleID, password string, code func(context.Context) (string, error), checkOnly bool, client primaryMailbox) control.Result {
	s, err := mailsetup.Load(dir, appleID)
	if err != nil {
		if !os.IsNotExist(err) {
			return control.Result{Message: "邮箱任务无法读取，请检查本机账号目录", Stage: "primary-mail"}
		}
		s = &mailsetup.State{AppleID: appleID}
	}
	// Preserve the durable submission marker, including after a restart.
	if !checkOnly {
		s.AutoCreate = true
	}
	s.Phase, s.Message = "checking", "正在检查 iCloud 主邮箱"
	if err := s.Save(dir); err != nil {
		return control.Result{Message: "无法保存邮箱任务", Stage: "primary-mail"}
	}
	ctx, cancel := context.WithTimeout(ctx, 110*time.Second)
	defer cancel()
	info, err := client.Check(ctx)
	if errors.Is(err, icloud.ErrPrimaryMailAuthentication) {
		err = client.Authenticate(ctx, password, code)
		if err == nil {
			info, err = client.Check(ctx)
		}
	}
	if err == nil && !info.Exists && !checkOnly {
		info, err = client.Ensure(ctx, !s.Submitted, func(candidate string) error {
			s.Submitted, s.Candidate = true, candidate
			s.Phase, s.Message = "submitted", "已开始提交邮箱创建，正在等待 Apple 核验"
			return s.Save(dir)
		})
	}
	if err != nil {
		s.Phase, s.Message = "blocked", err.Error()
		if s.Submitted {
			s.Phase = "unconfirmed"
		}
	} else if info.Exists {
		s.Phase, s.Message = "ready", "iCloud 主邮箱已存在，无需重复创建"
		if info.Email != "" {
			s.Email = info.Email
			s.Message = "iCloud 主邮箱已创建并核验"
		}
	} else if s.Submitted {
		s.Phase, s.Message = "unconfirmed", icloud.ErrPrimaryMailUnconfirmed.Error()
		err = icloud.ErrPrimaryMailUnconfirmed
	} else {
		s.Phase, s.Message = "missing", "此账号尚未创建 iCloud 主邮箱"
	}
	if saveErr := s.Save(dir); saveErr != nil {
		return control.Result{Message: "邮箱操作已结束，但本机状态保存失败；请检查状态后再继续", Stage: "primary-mail"}
	}
	return control.Result{OK: err == nil, Message: s.Message, Stage: "primary-mail"}
}
