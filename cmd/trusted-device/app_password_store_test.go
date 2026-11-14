package main

import "testing"

func TestValidAppPassword(t *testing.T) {
	for _, password := range []string{"abcd-efgh-ijkl-mnop", "zzzz-aaaa-bbbb-cccc"} {
		if !validAppPassword(password) {
			t.Fatal("valid application-specific password rejected")
		}
	}
	for _, password := range []string{
		"", "abcdefghijklmnop", "ABCD-efgh-ijkl-mnop", "abc1-efgh-ijkl-mnop",
		"abcd_efgh-ijkl-mnop", "abcd-efgh-ijkl-mno", "abcd-efgh-ijkl-mnopp",
		" abcd-efgh-ijkl-mnop", "abcd-efgh-ijkl-mnop ", "abcd-efgh-ijkl-mnop\n",
		"abcé-efgh-ijkl-mno",
	} {
		if validAppPassword(password) {
			t.Fatal("invalid application-specific password accepted")
		}
	}
}
