package skill

import (
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	files, err := Render(NewParams("/opt/hopsesh", "0.3.0", []string{"Claude Code", "Codex"}, []string{"claude", "codex"}))
	if err != nil {
		t.Fatal(err)
	}
	s := string(files["SKILL.md"])
	for _, want := range []string{"name: hopsesh", "Claude Code and Codex", "--in <claude|codex>", "`/opt/hopsesh plan <ref> --json`", `hopsesh-version: "0.3.0"`} {
		if !strings.Contains(s, want) {
			t.Errorf("SKILL.md lacks %q", want)
		}
	}
	if !strings.HasPrefix(s, "---\nname: hopsesh\n") {
		t.Error("frontmatter first")
	}
	again, _ := Render(NewParams("/opt/hopsesh", "0.3.0", []string{"Claude Code", "Codex"}, []string{"claude", "codex"}))
	if string(again["SKILL.md"]) != s || string(again["reference.md"]) != string(files["reference.md"]) {
		t.Error("rendering must be byte-identical every time (no drift between agents)")
	}
}
