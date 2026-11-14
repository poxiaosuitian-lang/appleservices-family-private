package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Laky-64/appleservices/icloud"
)

type fakeGiftRedeemer struct {
	calls                 []string
	prepared              *icloud.GiftCardInfo
	prepareErr, redeemErr error
	confirmation, code    string
	cancel                context.CancelFunc
}

func (f *fakeGiftRedeemer) PrepareGiftCard(context.Context, func() (string, error)) (*icloud.GiftCardInfo, error) {
	f.calls = append(f.calls, "prepare")
	if f.cancel != nil {
		f.cancel()
	}
	return f.prepared, f.prepareErr
}

func (f *fakeGiftRedeemer) RedeemGiftCard(_ context.Context, confirmation, code string) (*icloud.GiftCardInfo, error) {
	f.calls = append(f.calls, "redeem")
	f.confirmation, f.code = confirmation, code
	if f.redeemErr != nil {
		return &icloud.GiftCardInfo{State: "unknown"}, f.redeemErr
	}
	return &icloud.GiftCardInfo{State: "completed"}, nil
}

func TestGiftRedeemPreparesAndSubmitsOnce(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		f := &fakeGiftRedeemer{prepared: &icloud.GiftCardInfo{Ready: true, State: "prepared", PreparationID: "bound-confirmation"}}
		confirmation := ""
		want := []string{"prepare", "redeem"}
		if legacy {
			confirmation = "bound-confirmation"
			want = []string{"redeem"}
		}
		result, err := redeemPreparedGiftCard(context.Background(), f, confirmation, " x000000000000000 ", nil)
		if err != nil || result.State != "completed" || !reflect.DeepEqual(f.calls, want) || f.confirmation != "bound-confirmation" || f.code != "X000000000000000" {
			t.Fatalf("unexpected redemption: calls=%v result=%+v err=%v", f.calls, result, err)
		}
	}
}

func TestGiftRedeemStopsBeforeSubmission(t *testing.T) {
	for _, mode := range []string{"format", "prepare-error", "nil", "not-ready", "missing-id", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &fakeGiftRedeemer{prepared: &icloud.GiftCardInfo{Ready: true, State: "prepared", PreparationID: "bound-confirmation"}}
			code := "X000000000000000"
			switch mode {
			case "format":
				code = "invalid"
			case "prepare-error":
				f.prepareErr = errors.New("session unavailable")
			case "nil":
				f.prepared = nil
			case "not-ready":
				f.prepared.Ready = false
			case "missing-id":
				f.prepared.PreparationID = ""
			case "cancelled":
				f.cancel = cancel
			}
			result, err := redeemPreparedGiftCard(ctx, f, "", code, nil)
			if err == nil || result.State != "not-submitted" || f.code != "" {
				t.Fatalf("must not submit: %+v %v", result, err)
			}
			if mode == "format" && len(f.calls) != 0 {
				t.Fatal("invalid input must not authenticate")
			}
		})
	}
}

func TestGiftRedeemDoesNotReplayUnknownResult(t *testing.T) {
	f := &fakeGiftRedeemer{prepared: &icloud.GiftCardInfo{Ready: true, State: "prepared", PreparationID: "bound-confirmation"}, redeemErr: errors.New("connection lost")}
	result, err := redeemPreparedGiftCard(context.Background(), f, "", "X000000000000000", nil)
	if err == nil || result.State != "unknown" || !reflect.DeepEqual(f.calls, []string{"prepare", "redeem"}) {
		t.Fatalf("must preserve uncertain outcome without retry: %+v %v", result, f.calls)
	}
}
