package fsys

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppendKeepTime(t *testing.T) {
	f := filepath.Join(t.TempDir(), "s.jsonl")
	os.WriteFile(f, []byte(`{"a":1}`), 0o600) // no trailing newline
	old := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	os.Chtimes(f, old, old)
	if err := (Local{}).AppendKeepTime(f, []byte("{\"b\":2}\n")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(f)
	if string(b) != "{\"a\":1}\n{\"b\":2}\n" {
		t.Fatalf("got %q", b)
	}
	fi, _ := os.Stat(f)
	if !fi.ModTime().Equal(old) {
		t.Fatalf("mtime changed: %v", fi.ModTime())
	}
}
