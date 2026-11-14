package testkit

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/roeehrl/hopsesh/internal/testkit/fakeopencode"
)

// The demo home's OpenCode sessions are in the demo repository's project, so the stand-in
// lists them there as OpenCode would.
func TestDemoHomeOpenCode(t *testing.T) {
	h := t.TempDir()
	if err := DemoHome(h); err != nil {
		t.Fatal(err)
	}
	env := Env(h)
	var out, errOut bytes.Buffer
	code := fakeopencode.Main(fakeopencode.Proc{
		Args: []string{"session", "list", "--format", "json", "--standalone"}, Dir: filepath.Join(h, "git", "demo"),
		Getenv: func(k string) (string, bool) {
			if v, ok := env[k]; ok {
				return v, v != ""
			}
			return "", false
		},
		Stdout: &out, Stderr: &errOut,
	})
	if code != 0 {
		t.Fatalf("session list: %d %s", code, errOut.String())
	}
	var listed []struct {
		ID        string `json:"id"`
		Directory string `json:"directory"`
	}
	if err := json.Unmarshal(out.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range listed {
		ids = append(ids, s.ID)
		if s.Directory != filepath.Join(h, "git", "demo") { // as OpenCode resolved it: the OS's separators
			t.Errorf("%s is in %s", s.ID, s.Directory)
		}
	}
	slices.Sort(ids)
	want := slices.Sorted(slices.Values(DemoOpenCode))
	if !slices.Equal(ids, want) {
		t.Errorf("listed %v, want %v", ids, want)
	}
	if _, err := os.Stat(filepath.Join(h, ".local", "state", "opencode", "fake-service")); err == nil {
		t.Error("listing with --standalone started the background service")
	}
}

// Env's empty values are unset, never set empty.
func TestEnviron(t *testing.T) {
	got := Environ([]string{"OPENCODE_DB=/real/opencode.db", "KEEP=1", "HOME=/real"}, map[string]string{"OPENCODE_DB": "", "HOME": "/demo"})
	slices.Sort(got)
	if want := []string{"HOME=/demo", "KEEP=1"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	t.Setenv("OPENCODE_DB", "/real/opencode.db")
	Setenv(t, map[string]string{"OPENCODE_DB": ""})
	if _, ok := os.LookupEnv("OPENCODE_DB"); ok {
		t.Error("OPENCODE_DB is still set")
	}
}
