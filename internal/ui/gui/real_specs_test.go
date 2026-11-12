package gui

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The real-window specs (webtest/real) run only in CI, on the installed apps; a tab or
// heading they look for that the window no longer has fails there, after review. Each tab
// they open must be one of Settings' tabs, and each heading (but those made of a session's
// title) must still be written in the window's code.
func TestRealSpecsNameWhatTheWindowHas(t *testing.T) {
	var code strings.Builder
	_ = fs.WalkDir(Assets, "assets", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && (strings.HasSuffix(p, ".js") || strings.HasSuffix(p, ".html")) {
			b, _ := fs.ReadFile(Assets, p)
			code.Write(b)
		}
		return nil
	})
	specs, _ := filepath.Glob("../../devtools/webtest/real/*.spec.ts")
	if len(specs) == 0 {
		t.Fatal("no real-window specs found")
	}
	settings, _ := fs.ReadFile(Assets, "assets/settings.js")
	tabs := map[string]bool{}
	for _, m := range regexp.MustCompile(`\["[a-z-]+", "([^"]+)", [a-zA-Z]+\]`).FindAllStringSubmatch(string(settings), -1) {
		tabs[m[1]] = true
	}
	if !tabs["Machines"] {
		t.Fatalf("Settings' tabs not found: %v", tabs)
	}
	named := regexp.MustCompile(`getByRole\("(tab|heading)", \{ name: "([^"]+)"`)
	for _, f := range specs {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range named.FindAllStringSubmatch(string(b), -1) {
			switch {
			case m[1] == "tab":
				if !tabs[m[2]] {
					t.Errorf("%s opens Settings' %q tab, which Settings doesn't have", filepath.Base(f), m[2])
				}
				continue
			case strings.ContainsAny(m[2], "“”"):
				continue // a session's title
			}
			if !strings.Contains(code.String(), `"`+m[2]+`"`) && !strings.Contains(code.String(), "`"+m[2]+"`") {
				t.Errorf("%s looks for the %s %q, which the window doesn't have", filepath.Base(f), m[1], m[2])
			}
		}
	}
}
