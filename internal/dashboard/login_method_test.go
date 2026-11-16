package dashboard

import "testing"

func TestAutomaticRecoveryUsesLocalDeviceWithoutOverridingManualChoice(t *testing.T) {
	for _, tc := range []struct {
		selected string
		resume   bool
		want     string
	}{
		{"", true, "trusted-device"}, {"", false, "sms"}, {"sms", true, "sms"},
		{"sms-existing", true, "sms-existing"}, {"trusted-device", false, "trusted-device"},
	} {
		if got := loginVerificationMethod(tc.selected, tc.resume); got != tc.want {
			t.Fatalf("got %s want %s", got, tc.want)
		}
	}
}
