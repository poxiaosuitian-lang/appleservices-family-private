package icloud

import (
	"encoding/hex"
	"testing"
)

func TestWebPasswordKeyIndependentVectors(t *testing.T) {
	// Python hashlib.pbkdf2_hmac reference, using synthetic credentials only.
	salt, _ := hex.DecodeString("00112233445566778899aabbccddeeff")
	for protocol, want := range map[string]string{
		"s2k":    "bf9b39ead7ab6a13064321b8e9cfd4c9d9e0570b6aa3a3d6b9a90d9f167a0f2d",
		"s2k_fo": "0826e8ddbbb0a8cde3f17a9d1350c11bd82f6edcc5341b29fe8d6efac16a1887",
	} {
		t.Run(protocol, func(t *testing.T) {
			key, err := deriveWebPasswordKey("synthetic-password", protocol, salt, 1000)
			if err != nil || hex.EncodeToString(key) != want {
				t.Fatal("password key differs from independent reference")
			}
		})
	}
}

func TestWebPasswordKeyRejectsInvalidChallenge(t *testing.T) {
	for _, tc := range []struct {
		protocol   string
		iterations int
		salt       []byte
	}{
		{"", 1000, []byte("salt")}, {"unknown", 1000, []byte("salt")},
		{"s2k", 0, []byte("salt")}, {"s2k_fo", -1, []byte("salt")},
		{"s2k", 10000001, []byte("salt")}, {"s2k", 1000, nil},
	} {
		if key, err := deriveWebPasswordKey("synthetic-password", tc.protocol, tc.salt, tc.iterations); err == nil || key != nil {
			t.Fatal("invalid challenge accepted")
		}
	}
}
