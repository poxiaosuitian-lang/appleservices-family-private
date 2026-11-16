package dashboard

// Automatic recovery has a bound local device. Explicit user choices, including
// SMS, keep their original meaning.
func loginVerificationMethod(selected string, resume bool) string {
	if selected != "" {
		return selected
	}
	if resume {
		return "trusted-device"
	}
	return "sms"
}
