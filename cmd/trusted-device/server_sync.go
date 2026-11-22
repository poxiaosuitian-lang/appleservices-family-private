package main

import (
	"context"
	"errors"

	"github.com/Laky-64/appleservices/icloud"
)

type syncHMEClient interface {
	List(context.Context) (*icloud.HideMyEmailInfo, error)
	Authenticate(context.Context, string, func(context.Context) (string, error)) error
}

type syncHMERepairClient interface {
	AcceptPendingTerms(context.Context) error
	AuthenticateAndAcceptTerms(context.Context, string, func(context.Context) (string, error)) error
}

// Sync verifies actual Web access without requiring paid storage or a family
// cache. A successful empty alias list is valid; Apple errors still stop export.
func loadSyncHMESession(ctx context.Context, client syncHMEClient, password string, code func(context.Context) (string, error)) (*icloud.HideMyEmailInfo, error) {
	info, err := client.List(ctx)
	if errors.Is(err, icloud.ErrHMEAuthenticationRequired) {
		err = client.Authenticate(ctx, password, code)
		if err == nil {
			info, err = client.List(ctx)
		}
	}
	if errors.Is(err, icloud.ErrHMEWebRepairRequired) {
		if repair, ok := client.(syncHMERepairClient); ok {
			repairErr := repair.AcceptPendingTerms(ctx)
			if errors.Is(repairErr, icloud.ErrHMEWebRepairSessionUnavailable) {
				repairErr = repair.AuthenticateAndAcceptTerms(ctx, password, code)
			}
			if repairErr != nil {
				return nil, repairErr
			}
			return client.List(ctx)
		}
	}
	return info, err
}

func serverSyncMailbox(info *icloud.HideMyEmailInfo) (string, error) {
	mailbox := ownICloudForwardTarget(info)
	if mailbox == "" {
		return "", errors.New("无法唯一确定本账号的 iCloud 邮箱，请先检查邮箱状态")
	}
	// Accounts without active aliases have no forwarding to configure. Never
	// require creating a paid alias just to import their existing mailbox.
	for _, alias := range info.Aliases {
		if alias.Active {
			if !hmeForwardMatches(info, mailbox) {
				return "", errors.New("请先将隐私邮箱转发到本账号的 iCloud 邮箱")
			}
			break
		}
	}
	return mailbox, nil
}
