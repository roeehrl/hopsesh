package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This machine's short name for a folder with a long name names the same folder (when the
// drive keeps short names at all).
func TestShortPathLocal(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a folder with a long name")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	s := shortPath(dir)
	if s == "" || strings.EqualFold(s, dir) {
		t.Skipf("no short names on this drive (%q)", s)
	}
	a, _ := os.Stat(dir)
	b, err := os.Stat(s)
	if err != nil || !os.SameFile(a, b) {
		t.Fatalf("%s is not %s: %v", s, dir, err)
	}
	m := &Machine{Local: true, Facts: Facts{OS: "windows"}}
	if got := m.ShortNames(t.Context(), []string{dir, filepath.Join(dir, "missing")}); got[dir] != s || len(got) != 1 {
		t.Fatalf("ShortNames: %v", got)
	}
}
