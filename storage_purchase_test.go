package appleservices

import (
	"context"
	"errors"
	"testing"

	"github.com/Laky-64/appleservices/icloud"
)

func TestSkillPurchaseStopsBeforeNetworkWhenSessionIsMissing(t *testing.T) {
	for _, test := range []struct {
		name  string
		login *Login
		stage string
	}{
		{"not-logged-in", &Login{}, "purchase-account-authentication-required"},
		{"verification-pending", &Login{needsTwoFactor: true, client: &Client{DSID: "test"}}, "purchase-account-authentication-required"},
		{"renewed-without-store-session", &Login{client: &Client{DSID: "test"}}, "purchase-store-session"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// No transport or anisette is configured: a failed precondition must
			// return before accessing either or submitting any purchase.
			result, err := test.login.SubmitSkillStoragePurchase(context.Background(), PurchaseTarget{}, "")
			var failure *icloud.StorageError
			if result != nil || !errors.As(err, &failure) || failure.Stage != test.stage || failure.HTTPStatus != 0 {
				t.Fatalf("local precondition was not reported correctly: %v", err)
			}
		})
	}
}
