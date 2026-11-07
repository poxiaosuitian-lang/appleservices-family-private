package appleservices

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Laky-64/appleservices/icloud"
)

type fakePurchasePreparation struct {
	calls      []string
	paid       uint64
	price      int64
	authErr    error
	subscribed bool
}

func (f *fakePurchasePreparation) StorageInfo(context.Context) (*StorageInfo, error) {
	f.calls = append(f.calls, "quota")
	return &StorageInfo{PaidBytes: f.paid}, nil
}
func (f *fakePurchasePreparation) StoragePlanInfo(context.Context) (*StoragePlanInfo, error) {
	f.calls = append(f.calls, "quote")
	return &StoragePlanInfo{Plans: []icloud.StoragePlan{{Bytes: 50 * 1024 * 1024 * 1024, Price: f.price, Currency: "USD", Period: "month", Available: true}}}, nil
}
func (f *fakePurchasePreparation) CheckSkillStoragePurchaseSession(context.Context, func() (string, error)) (*icloud.StoreAuthenticationInfo, error) {
	f.calls = append(f.calls, "authentication")
	return &icloud.StoreAuthenticationInfo{Ready: f.authErr == nil}, f.authErr
}
func (f *fakePurchasePreparation) StorageSubscriptionPresent(context.Context) (bool, error) {
	f.calls = append(f.calls, "subscription")
	return f.subscribed, nil
}

func TestPurchasePreparationStopsAtFirstFailureAndNeverSubmits(t *testing.T) {
	target := PurchaseTarget{Bytes: 50 * 1024 * 1024 * 1024, PriceMinor: 99, Currency: "USD", Period: "month"}
	for _, test := range []struct {
		name   string
		source fakePurchasePreparation
		stage  string
		calls  []string
	}{
		{"ready", fakePurchasePreparation{price: 99}, "", []string{"quota", "quote", "authentication", "subscription"}},
		{"paid-quota", fakePurchasePreparation{paid: 1, price: 99}, "purchase-existing-subscription", []string{"quota"}},
		{"changed-price", fakePurchasePreparation{price: 199}, "purchase-quote-changed", []string{"quota", "quote"}},
		{"authentication-503", fakePurchasePreparation{price: 99, authErr: &icloud.StorageError{Stage: "purchase-store-authentication", HTTPStatus: 503}}, "purchase-store-authentication", []string{"quota", "quote", "authentication"}},
		{"existing-subscription", fakePurchasePreparation{price: 99, subscribed: true}, "purchase-existing-subscription", []string{"quota", "quote", "authentication", "subscription"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := prepareStoragePurchase(context.Background(), &test.source, target, nil)
			if !reflect.DeepEqual(test.source.calls, test.calls) {
				t.Fatalf("unexpected calls: %v", test.source.calls)
			}
			if test.stage == "" {
				if err != nil || result == nil {
					t.Fatalf("preparation failed: %v", err)
				}
				return
			}
			var failure *icloud.StorageError
			if result != nil || !errors.As(err, &failure) || failure.Stage != test.stage {
				t.Fatalf("failure lost: %v", err)
			}
		})
	}
}
