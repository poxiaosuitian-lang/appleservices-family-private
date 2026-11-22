package main

import (
	"context"
	"errors"
)

type familyHelperRunner func(ctx context.Context, helper string, args, extraEnv []string) (string, error)

// ensureOrganizerFamilyReady checks the selected host account's live family
// state before adding a pool member. A standalone iCloud+ subscriber that has
// not created its family group yet will create the family (with purchase and
// location sharing disabled) and enable iCloud+ storage sharing automatically.
func ensureOrganizerFamilyReady(ctx context.Context, helper, organizerDir string, run familyHelperRunner) error {
	output, err := run(ctx, helper, []string{"-dir", organizerDir, "-safe-json"}, nil)
	if err != nil {
		return errors.New("母号家庭状态核验未完成：" + lastNonEmptyLine(output))
	}
	family, err := parseFamilyStatus(output)
	if err != nil {
		return errors.New("母号家庭状态回读无效")
	}
	if family.Member && !family.Organizer {
		return errors.New("所选账号已是其他家庭的成员，无法作为母号邀请")
	}
	if !family.Member {
		if !family.EligibilityKnown || !family.Eligible {
			return errors.New("所选母号当前不具备创建家庭资格")
		}
		output, err = run(ctx, helper, []string{"-dir", organizerDir, "-create-family", "-safe-json"}, nil)
		if err != nil {
			return errors.New("母号自动创建家庭未完成：" + lastNonEmptyLine(output))
		}
		family, err = parseFamilyStatus(output)
		if err != nil || !family.Member || !family.Organizer {
			return errors.New("母号创建家庭后未通过组织者回读")
		}
	}
	if !family.StorageEnabled {
		output, err = run(ctx, helper, []string{"-dir", organizerDir, "-share-icloud-storage", "-safe-json"}, nil)
		if err != nil {
			return errors.New("母号启用共享 iCloud+ 储存未完成：" + lastNonEmptyLine(output))
		}
		family, err = parseFamilyStatus(output)
		if err != nil || !family.StorageKnown || !family.StorageEnabled {
			return errors.New("母号共享 iCloud+ 储存尚未通过回读确认")
		}
	}
	return nil
}
