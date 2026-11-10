package appleservices

import (
	"context"
	"github.com/Laky-64/appleservices/icloud"
)

func (l *Login) PrepareGiftCard(ctx context.Context, codeProvider func() (string, error)) (*icloud.GiftCardInfo, error) {
	if _, err := l.CheckSkillStoragePurchaseSession(ctx, codeProvider); err != nil {
		return nil, err
	}
	return l.storeCommerce.PrepareGiftCard(ctx)
}
func (l *Login) RedeemGiftCard(ctx context.Context, confirmation, code string) (*icloud.GiftCardInfo, error) {
	if !l.StorePurchaseSessionReady() {
		return nil, &icloud.StorageError{Stage: "gift-session"}
	}
	return l.storeCommerce.RedeemGiftCard(ctx, confirmation, code)
}
