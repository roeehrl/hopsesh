package skill

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

var update = flag.Bool("update", false, "rewrite plugin/ from the templates, for the newest version in CHANGELOG.md")

// The plugin in plugin/ (installed from the repository with /plugin marketplace add
// roeehrl/hopsesh, codex plugin marketplace add or npx skills add) carries the skill of the
// newest release: the release that adds a CHANGELOG.md section rewrites it
// (scripts/update-plugin.sh), and between releases it stays as that release shipped it.
func TestPlugin(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	read := func(p string) []byte {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	m := regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\]`).FindSubmatch(read("CHANGELOG.md"))
	if m == nil {
		t.Fatal("CHANGELOG.md has no release section")
	}
	newest := string(m[1])
	manifests := []string{"plugin/.claude-plugin/plugin.json", "plugin/plugin.json"}
	if *update {
		files, err := Render(NewParams("hopsesh", newest, []string{"Claude Code", "Codex"}, []string{"claude", "codex"}))
		if err != nil {
			t.Fatal(err)
		}
		for name, b := range files {
			if err := os.WriteFile(filepath.Join(root, "plugin", "skills", "hopsesh", name), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		for _, p := range manifests {
			b := regexp.MustCompile(`"version": "[^"]*"`).ReplaceAll(read(p), []byte(`"version": "`+newest+`"`))
			if err := os.WriteFile(filepath.Join(root, p), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, p := range manifests {
		var v struct{ Name, Version string }
		if err := json.Unmarshal(read(p), &v); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if v.Name != "hopsesh" || v.Version != newest {
			t.Errorf("%s: name %q version %q, want hopsesh %s (run scripts/update-plugin.sh)", p, v.Name, v.Version, newest)
		}
	}
	skill := read("plugin/skills/hopsesh/SKILL.md")
	if !regexp.MustCompile(`(?m)^name: hopsesh$`).Match(skill) || !regexp.MustCompile(`(?m)^  hopsesh-version: "` + regexp.QuoteMeta(newest) + `"$`).Match(skill) {
		t.Errorf("plugin/skills/hopsesh/SKILL.md is not the %s skill (run scripts/update-plugin.sh)", newest)
	}
	if _, err := os.Stat(filepath.Join(root, "plugin", "skills", "hopsesh", "reference.md")); err != nil {
		t.Error(err)
	}
	for _, p := range []string{".claude-plugin/marketplace.json", ".agents/plugins/marketplace.json"} {
		if !json.Valid(read(p)) {
			t.Errorf("%s is not JSON", p)
		}
	}
}
