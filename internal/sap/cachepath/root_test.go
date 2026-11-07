package cachepath

import "testing"

func TestPortableCacheRoot(t *testing.T) {
	want := t.TempDir()
	t.Setenv("APPLESERVICES_CACHE_DIR", want)
	got, err := Root()
	if err != nil || got != want {
		t.Fatal("portable cache directory ignored")
	}
	t.Setenv("APPLESERVICES_CACHE_DIR", "relative-cache")
	if _, err := Root(); err == nil {
		t.Fatal("relative cache directory accepted")
	}
}
