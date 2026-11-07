package main

import (
	"errors"

	"github.com/Laky-64/appleservices/icloud"
	"github.com/Laky-64/appleservices/internal/control"
)

func skillStoragePurchaseResult(purchase *icloud.PurchaseResult, err error) control.Result {
	result := control.Result{Purchase: purchase, Message: "购买结果尚未确认，请先核对订阅和容量；不要重复提交", Stage: "purchase-verification"}
	if err != nil {
		var failure *icloud.StorageError
		if errors.As(err, &failure) {
			result.Stage, result.HTTPStatus, result.AppleCode, result.AppleHint = failure.Stage, failure.HTTPStatus, failure.AppleCode, failure.Hint
			switch failure.Stage {
			case "purchase-store-timeout":
				result.Message = "购买会话认证超时；未提交购买，请稍后重新进行预检查"
			case "purchase-store-authentication", "purchase-store-rejected", "purchase-store-transport":
				result.Message = "Apple Store 认证未通过，购买链路停在认证步骤；未提交购买"
			case "purchase-quote-changed":
				result.Message = "Apple 实时报价与待确认方案不一致；未提交购买，请重新读取方案"
			case "purchase-account-authentication-required":
				result.Message = "请先完成账户登录与验证；未提交购买"
			case "purchase-store-session":
				result.Message = "购买会话不可用或已随登录续期重置，请重新验证购买会话；未提交购买"
			case "purchase-existing-subscription":
				result.Message = "Apple 已返回现有 iCloud+ 订阅，已阻止重复购买；请核对订阅和容量"
			case "purchase-subscription-verification", "purchase-quota-verification":
				result.Message = "购买已返回完成结果，但订阅或付费容量尚未核实；请核对 Apple 订阅，不要重复购买"
			}
			if failure.Stage == "purchase-skill-buy" {
				result.Message = "Apple 未接受此次购买，请查看业务错误码；HTTP 200 不代表订阅成功"
			}
			if failure.Hint == "payment-confirmation" || failure.Hint == "billing-confirmation" {
				result.Message = "Apple 返回付款或账单相关提示，请在 Apple 官方账户页面检查付款方式"
			}
		}
		return result
	}
	if purchase == nil {
		return result
	}
	if purchase.State == "completed" && purchase.QuotaVerified {
		result.OK, result.Message, result.Stage = true, "50GB 订阅和付费配额均已核验", "purchase-completed"
		return result
	}
	switch purchase.State {
	case "confirmation-required", "handoff":
		result.Message = "Apple 要求进一步确认，请在官方客户端处理并核对订阅；当前尚未确认开通"
		result.Stage = "purchase-confirmation-required"
	case "authentication-required":
		result.Message, result.Stage = "Apple 要求重新验证购买会话", "purchase-authentication-required"
	}
	return result
}
