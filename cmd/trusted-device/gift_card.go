package main

import (
	"context"
	"regexp"
	"strings"

	"github.com/Laky-64/appleservices/icloud"
)

type giftCardRedeemer interface {
	PrepareGiftCard(context.Context, func() (string, error)) (*icloud.GiftCardInfo, error)
	RedeemGiftCard(context.Context, string, string) (*icloud.GiftCardInfo, error)
}

// A single user confirmation prepares the account-bound session and submits once.
// Keep explicit confirmation IDs working for older clients.
func redeemPreparedGiftCard(ctx context.Context, redeemer giftCardRedeemer, confirmation, code string, provider func() (string, error)) (*icloud.GiftCardInfo, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if !regexp.MustCompile(`^X[A-Z0-9]{15}$`).MatchString(code) {
		return &icloud.GiftCardInfo{State: "not-submitted"}, &icloud.StorageError{Stage: "gift-code-format"}
	}
	if confirmation == "" {
		prepared, err := redeemer.PrepareGiftCard(ctx, provider)
		if err != nil {
			return &icloud.GiftCardInfo{State: "not-submitted"}, err
		}
		if prepared == nil || !prepared.Ready || prepared.State != "prepared" || prepared.PreparationID == "" {
			return &icloud.GiftCardInfo{State: "not-submitted"}, &icloud.StorageError{Stage: "gift-prepare"}
		}
		confirmation = prepared.PreparationID
	}
	if err := ctx.Err(); err != nil {
		return &icloud.GiftCardInfo{State: "not-submitted"}, err
	}
	return redeemer.RedeemGiftCard(ctx, confirmation, code)
}
