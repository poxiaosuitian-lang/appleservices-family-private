package appleservices

import (
	"context"
	"errors"
	"net/http"

	"github.com/Laky-64/appleservices/icloud"
)

// StoragePurchasePreparation contains only public quota and quote information.
// Preparation performs no purchase, terms acceptance, or payment mutation.
type StoragePurchasePreparation struct {
	Storage *StorageInfo
	Plans   *StoragePlanInfo
}

type storagePurchasePreparationSource interface {
	StorageInfo(context.Context) (*StorageInfo, error)
	StoragePlanInfo(context.Context) (*StoragePlanInfo, error)
	CheckSkillStoragePurchaseSession(context.Context, func() (string, error)) (*icloud.StoreAuthenticationInfo, error)
	StorePurchaseSessionReady() bool
	StorageSubscriptionPresent(context.Context) (bool, error)
}

func (l *Login) StorageSubscriptionPresent(ctx context.Context) (bool, error) {
	if !l.StorePurchaseSessionReady() {
		return false, &icloud.StorageError{Stage: "purchase-store-session"}
	}
	sub, err := l.storeCommerce.ReadSkillSubscription(ctx)
	if err != nil {
		return false, err
	}
	if sub == nil {
		return false, &icloud.StorageError{Stage: "purchase-skill-subscription-list"}
	}
	return sub.Present, nil
}

func (l *Login) PrepareSkillStoragePurchase(ctx context.Context, target PurchaseTarget, codeProvider func() (string, error)) (*StoragePurchasePreparation, error) {
	return prepareStoragePurchase(ctx, l, target, codeProvider)
}

func prepareStoragePurchase(ctx context.Context, source storagePurchasePreparationSource, target PurchaseTarget, codeProvider func() (string, error)) (*StoragePurchasePreparation, error) {
	quota, err := source.StorageInfo(ctx)
	if err != nil {
		return nil, err
	}
	if quota == nil {
		return nil, &icloud.StorageError{Stage: "purchase-quota-precheck"}
	}
	if quota.PaidBytes != 0 {
		return nil, &icloud.StorageError{Stage: "purchase-existing-subscription"}
	}
	plans, err := source.StoragePlanInfo(ctx)
	if err != nil {
		return nil, err
	}
	if err := icloud.ValidatePurchaseTarget(plans, target); err != nil {
		return nil, &icloud.StorageError{Stage: "purchase-quote-changed"}
	}
	hadStoreSession := source.StorePurchaseSessionReady()
	auth, err := source.CheckSkillStoragePurchaseSession(ctx, codeProvider)
	if err != nil {
		return nil, err
	}
	if auth == nil || !auth.Ready {
		return nil, &icloud.StorageError{Stage: "purchase-store-session"}
	}
	present, err := source.StorageSubscriptionPresent(ctx)
	var failure *icloud.StorageError
	if hadStoreSession && ctx.Err() == nil && errors.As(err, &failure) && failure.Stage == "purchase-skill-response" &&
		(failure.HTTPStatus == http.StatusUnauthorized || failure.HTTPStatus == http.StatusForbidden) &&
		!source.StorePurchaseSessionReady() {
		// The read rejected and invalidated a previously cached Store session.
		// Reauthenticate the same account once, then repeat only this read.
		// Purchase requests and ambiguous failures must never be replayed here.
		auth, err = source.CheckSkillStoragePurchaseSession(ctx, codeProvider)
		if err != nil {
			return nil, err
		}
		if auth == nil || !auth.Ready {
			return nil, &icloud.StorageError{Stage: "purchase-store-session"}
		}
		present, err = source.StorageSubscriptionPresent(ctx)
	}
	if err != nil {
		return nil, err
	}
	if present {
		return nil, &icloud.StorageError{Stage: "purchase-existing-subscription"}
	}
	return &StoragePurchasePreparation{Storage: quota, Plans: plans}, nil
}
