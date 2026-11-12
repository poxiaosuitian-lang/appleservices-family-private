package mailsetup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRejectsLostSubmissionMarker(t *testing.T) {
	for _, body := range []string{
		`{"apple_id":"alice@example.test","phase":"checking"}`,
		`{"apple_id":"alice@example.test","phase":"checking","submitted":null}`,
		`{"apple_id":"alice@example.test","phase":"submitted","submitted":false}`,
		`{"apple_id":"alice@example.test","phase":"unconfirmed","submitted":false}`,
		`{"apple_id":"alice@example.test","phase":"unknown","submitted":false}`,
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, "primary-mail-status.json")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(dir, "alice@example.test"); err == nil {
			t.Fatal("incomplete checkpoint could allow activation replay")
		}
		if err := Queue(dir, "alice@example.test"); err == nil {
			t.Fatal("queue replaced an unreadable checkpoint")
		}
		unchanged, _ := os.ReadFile(path)
		if string(unchanged) != body {
			t.Fatal("unreadable checkpoint was changed")
		}
	}
}

func TestSaveReplacesCheckpointAndCleansTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	if err := Queue(dir, "alice@example.test"); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir, "ALICE@example.test")
	if err != nil {
		t.Fatal(err)
	}
	s.Submitted, s.Phase, s.Candidate = true, "submitted", "alice@icloud.com"
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	s, err = Load(dir, "alice@example.test")
	if err != nil || !s.Submitted || s.Candidate != "alice@icloud.com" {
		t.Fatal("submission checkpoint did not survive reload")
	}
	if _, err := Load(dir, "other@example.test"); err == nil {
		t.Fatal("checkpoint could be reused for another account")
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 || files[0].Name() != "primary-mail-status.json" {
		t.Fatal("temporary checkpoints were left behind")
	}
}
