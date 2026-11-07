package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/Laky-64/appleservices/icloud"
)

func TestSkillPurchaseOnlyReportsVerifiedCompletion(t *testing.T) {
	for _, state := range []string{"completed", "confirmation-required", "handoff", "verification-pending", "authentication-required", ""} {
		for _, verified := range []bool{false, true} {
			purchase := &icloud.PurchaseResult{State: state, QuotaVerified: verified, HTTPStatus: 200}
			result := skillStoragePurchaseResult(purchase, nil)
			if result.OK != (state == "completed" && verified) {
				t.Fatalf("incorrect success for state %q, verified %v", state, verified)
			}
			if !result.OK && strings.Contains(result.Message, "均已核验") {
				t.Fatal("unverified purchase reported as verified")
			}
		}
	}
	if skillStoragePurchaseResult(nil, nil).OK {
		t.Fatal("missing result reported as success")
	}
	if skillStoragePurchaseResult(&icloud.PurchaseResult{State: "completed", QuotaVerified: true}, errors.New("readback failed")).OK {
		t.Fatal("readback failure ignored")
	}
}

func TestSkillPurchaseExposesBusinessFailureWithoutClaimingMissingCard(t *testing.T) {
	err := &icloud.StorageError{Stage: "purchase-skill-buy", HTTPStatus: 200, AppleCode: "1234", Hint: "billing-confirmation"}
	result := skillStoragePurchaseResult(nil, err)
	if result.OK || result.AppleCode != "1234" || result.AppleHint != "billing-confirmation" || !strings.Contains(result.Message, "账单") {
		t.Fatal("billing failure was lost")
	}
	err.Hint = "unclassified-response"
	result = skillStoragePurchaseResult(nil, err)
	if strings.Contains(result.Message, "付款方式") || !strings.Contains(result.Message, "不代表订阅成功") {
		t.Fatal("unknown rejection was diagnosed as a missing payment method")
	}
}

func TestSkillPurchaseDistinguishesLocalSessionFromSubmittedPurchase(t *testing.T) {
	for _, stage := range []string{"purchase-store-session", "purchase-account-authentication-required"} {
		result := skillStoragePurchaseResult(nil, &icloud.StorageError{Stage: stage})
		if result.OK || result.Stage != stage || !strings.Contains(result.Message, "未提交购买") {
			t.Fatalf("local rejection lost: %s", stage)
		}
	}
	for _, stage := range []string{"purchase-existing-subscription", "purchase-subscription-verification", "purchase-quota-verification"} {
		result := skillStoragePurchaseResult(nil, &icloud.StorageError{Stage: stage})
		if result.OK || result.Stage != stage || strings.Contains(result.Message, "未提交购买") {
			t.Fatalf("subscription/readback state misreported: %s", stage)
		}
	}
}
