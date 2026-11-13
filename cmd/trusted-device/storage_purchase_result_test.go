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

func TestSkillPurchaseExpiredTokenRequestsStoreAuthentication(t *testing.T) {
	result := skillStoragePurchaseResult(nil, &icloud.StorageError{Stage: "purchase-store-token-expired", HTTPStatus: 200, AppleCode: "2034"})
	if result.OK || result.AppleCode != "2034" || !strings.Contains(result.Message, "重新认证") || strings.Contains(result.Message, "付款方式") || strings.Contains(result.Message, "未提交购买") {
		t.Fatal("expired purchase token misreported as a payment failure or an unsent request")
	}
}

func TestSkillPurchasePublishesVerifiedQuotaForDashboardCache(t *testing.T) {
	quota := &icloud.StorageInfo{TotalBytes: 50 * 1024 * 1024 * 1024, PaidBytes: 50 * 1024 * 1024 * 1024}
	purchase := &icloud.PurchaseResult{State: "completed", QuotaVerified: true, VerifiedStorage: quota}
	result := skillStoragePurchaseResult(purchase, nil)
	if !result.OK || result.Storage != quota {
		t.Fatal("verified quota was not returned for the dashboard and its restart cache")
	}
	purchase.QuotaVerified = false
	if result := skillStoragePurchaseResult(purchase, nil); result.OK || result.Storage != nil {
		t.Fatal("unverified quota was published as a completed purchase")
	}
}
