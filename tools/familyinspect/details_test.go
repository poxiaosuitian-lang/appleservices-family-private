package main

import "testing"

func TestFamilyDetailsCompatibilityRequiresExplicitBoundMembership(t *testing.T) {
	ageOnly := []byte(`{"dsid":123,"status":0,"status-message":"Success","age-category":"ADULT"}`)
	for _, test := range []struct {
		name, response string
		success        bool
	}{
		{"no-family", `{"dsid":123,"is-member-of-family":false}`, true},
		{"family", `{"dsid":123,"is-member-of-family":true,"family-members":[{"is-me":true,"member-is-organizer":true}]}`, true},
		{"wrong-account", `{"dsid":456,"is-member-of-family":false}`, false},
		{"still-missing", `{"dsid":123,"status":0}`, false},
		{"inconsistent", `{"dsid":123,"is-member-of-family":true}`, false},
		{"error", `{"dsid":123,"is-member-of-family":false,"error":true}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			_, legacy, err := compatibleFamilyDetails(ageOnly, "123", func() ([]byte, error) { calls++; return []byte(test.response), nil })
			if (err == nil) != test.success || calls != 1 || legacy != test.success {
				t.Fatal("invalid compatibility result", err)
			}
		})
	}
}

func TestFamilyDetailsDoesNotFallbackOnErrorOrAccountMismatch(t *testing.T) {
	for _, primary := range []string{
		`{"dsid":456,"status":0}`, `{"dsid":123,"status":1}`, `{"dsid":123,"status":0,"error":true}`,
		`{"dsid":123,"status":0,"is-member-of-family":"false"}`, `{}`,
	} {
		_, _, err := compatibleFamilyDetails([]byte(primary), "123", func() ([]byte, error) { t.Fatal("unexpected fallback"); return nil, nil })
		if err == nil {
			t.Fatal("invalid primary accepted")
		}
	}
	_, legacy, err := compatibleFamilyDetails([]byte(`{"dsid":123,"is-member-of-family":false}`), "123", func() ([]byte, error) { t.Fatal("unneeded fallback"); return nil, nil })
	if err != nil || legacy {
		t.Fatal("valid primary rejected")
	}
}
