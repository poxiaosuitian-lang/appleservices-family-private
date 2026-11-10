package appleservices

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	ready      bool
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
	f.ready = f.authErr == nil
	return &icloud.StoreAuthenticationInfo{Ready: f.authErr == nil}, f.authErr
}
func (f *fakePurchasePreparation) StorePurchaseSessionReady() bool { return f.ready }
func (f *fakePurchasePreparation) StorageSubscriptionPresent(context.Context) (bool, error) {
	f.calls = append(f.calls, "subscription")
	return f.subscribed, nil
}

type recoveringPurchasePreparation struct {
	fakePurchasePreparation
	reads               int
	authentications     int
	readFailures        []error
	preserveSession     bool
	reauthenticationErr error
	reauthentication    *icloud.StoreAuthenticationInfo
	onReadFailure       func()
}

func (f *recoveringPurchasePreparation) CheckSkillStoragePurchaseSession(ctx context.Context, provider func() (string, error)) (*icloud.StoreAuthenticationInfo, error) {
	f.authentications++
	if f.authentications == 1 {
		return f.fakePurchasePreparation.CheckSkillStoragePurchaseSession(ctx, provider)
	}
	f.calls = append(f.calls, "authentication")
	f.ready = f.reauthenticationErr == nil && f.reauthentication != nil && f.reauthentication.Ready
	return f.reauthentication, f.reauthenticationErr
}

func (f *recoveringPurchasePreparation) StorageSubscriptionPresent(context.Context) (bool, error) {
	f.calls = append(f.calls, "subscription")
	f.reads++
	if f.reads <= len(f.readFailures) && f.readFailures[f.reads-1] != nil {
		if !f.preserveSession {
			f.ready = false
		}
		if f.onReadFailure != nil {
			f.onReadFailure()
		}
		return false, f.readFailures[f.reads-1]
	}
	return f.subscribed, nil
}

func TestPurchasePreparationReauthenticatesInvalidatedStoreSessionOnce(t *testing.T) {
	target := PurchaseTarget{Bytes: 50 * 1024 * 1024 * 1024, PriceMinor: 99, Currency: "USD", Period: "month"}
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			source := &recoveringPurchasePreparation{
				fakePurchasePreparation: fakePurchasePreparation{price: 99, ready: true},
				readFailures: []error{fmt.Errorf("subscription read: %w", &icloud.StorageError{
					Stage: "purchase-skill-response", HTTPStatus: status,
				})},
				reauthentication: &icloud.StoreAuthenticationInfo{Ready: true},
			}
			result, err := prepareStoragePurchase(context.Background(), source, target, nil)
			want := []string{"quota", "quote", "authentication", "subscription", "authentication", "subscription"}
			if err != nil || result == nil || !source.ready || !reflect.DeepEqual(source.calls, want) {
				t.Fatalf("failed to recover invalidated session: calls=%v, err=%v", source.calls, err)
			}
		})
	}
}

func TestPurchasePreparationStopsAfterRecoveryFailure(t *testing.T) {
	target := PurchaseTarget{Bytes: 50 * 1024 * 1024 * 1024, PriceMinor: 99, Currency: "USD", Period: "month"}
	expired := &icloud.StorageError{Stage: "purchase-skill-response", HTTPStatus: http.StatusUnauthorized}
	accountBinding := errors.New("Store account binding failed")
	for _, test := range []struct {
		name        string
		reauth      *icloud.StoreAuthenticationInfo
		reauthErr   error
		secondError error
		subscribed  bool
		wantErr     error
		wantStage   string
		wantReads   int
	}{
		{name: "rejected-again", reauth: &icloud.StoreAuthenticationInfo{Ready: true}, secondError: expired, wantErr: expired, wantReads: 2},
		{name: "different-account", reauthErr: accountBinding, wantErr: accountBinding, wantReads: 1},
		{name: "missing-auth-result", wantStage: "purchase-store-session", wantReads: 1},
		{name: "auth-not-ready", reauth: &icloud.StoreAuthenticationInfo{}, wantStage: "purchase-store-session", wantReads: 1},
		{name: "subscription-now-present", reauth: &icloud.StoreAuthenticationInfo{Ready: true}, subscribed: true, wantStage: "purchase-existing-subscription", wantReads: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := &recoveringPurchasePreparation{
				fakePurchasePreparation: fakePurchasePreparation{price: 99, subscribed: test.subscribed, ready: true},
				readFailures:            []error{expired, test.secondError}, reauthentication: test.reauth, reauthenticationErr: test.reauthErr,
			}
			result, err := prepareStoragePurchase(context.Background(), source, target, nil)
			if result != nil || err == nil || source.authentications != 2 || source.reads != test.wantReads {
				t.Fatalf("unexpected recovery sequence: calls=%v, err=%v", source.calls, err)
			}
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("recovery error lost: %v", err)
			}
			var failure *icloud.StorageError
			if test.wantStage != "" && (!errors.As(err, &failure) || failure.Stage != test.wantStage) {
				t.Fatalf("wrong recovery stage: %v", err)
			}
		})
	}
}

func TestPurchasePreparationDoesNotRetryUnprovenSessionExpiry(t *testing.T) {
	target := PurchaseTarget{Bytes: 50 * 1024 * 1024 * 1024, PriceMinor: 99, Currency: "USD", Period: "month"}
	for _, test := range []struct {
		name       string
		failure    error
		stillReady bool
		cancel     bool
		fresh      bool
	}{
		{"network", errors.New("connection reset"), false, false, false},
		{"unavailable", &icloud.StorageError{Stage: "purchase-skill-response", HTTPStatus: 503}, false, false, false},
		{"application-rejection", &icloud.StorageError{Stage: "purchase-skill-response", HTTPStatus: 200, AppleCode: "2034"}, false, false, false},
		{"wrong-stage", &icloud.StorageError{Stage: "purchase-store-authentication", HTTPStatus: 401}, false, false, false},
		{"not-invalidated", &icloud.StorageError{Stage: "purchase-skill-response", HTTPStatus: 401}, true, false, false},
		{"cancelled", &icloud.StorageError{Stage: "purchase-skill-response", HTTPStatus: 403}, false, true, false},
		{"newly-authenticated", &icloud.StorageError{Stage: "purchase-skill-response", HTTPStatus: 401}, false, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			source := &recoveringPurchasePreparation{
				fakePurchasePreparation: fakePurchasePreparation{price: 99, ready: !test.fresh},
				readFailures:            []error{test.failure}, preserveSession: test.stillReady,
			}
			if test.cancel {
				source.onReadFailure = cancel
			}
			result, err := prepareStoragePurchase(ctx, source, target, nil)
			if result != nil || !errors.Is(err, test.failure) || source.authentications != 1 || source.reads != 1 {
				t.Fatalf("unexpected authentication retry: calls=%v, err=%v", source.calls, err)
			}
		})
	}
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
