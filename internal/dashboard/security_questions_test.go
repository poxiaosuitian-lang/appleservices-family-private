package dashboard

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSecurityQuestionViewIsRedactedAndAccountBound(t *testing.T) {
	a := &Account{AppleID: "owner@example.test", Dir: t.TempDir()}
	data := []byte(`{"apple_id":"owner@example.test","status":{"questions":[{"id":136,"question":"Question?","saved":true,"answer":"synthetic-private"}],"blocked":false}}`)
	if err := os.WriteFile(filepath.Join(a.Dir, "security-questions-status.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	s := readSecurityQuestionsStatus(a)
	if s == nil || len(s.Questions) != 1 || !s.Questions[0].Saved {
		t.Fatal("status missing")
	}
	out, _ := json.Marshal(s)
	if bytes.Contains(out, []byte("synthetic-private")) || bytes.Contains(out, []byte(`"answer"`)) {
		t.Fatal("view leaked answer")
	}
	a.AppleID = "other@example.test"
	if readSecurityQuestionsStatus(a) != nil {
		t.Fatal("cross-account status leaked")
	}
}
