package claudeskill

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderIsValidSkill(t *testing.T) {
	files, err := Render(Params{Bin: "/Users/alice/.local/bin/hopsesh", Version: "0.2.0"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(files["SKILL.md"])
	if !strings.HasPrefix(s, "---\nname: hopsesh\n") {
		t.Fatalf("frontmatter must start with the name:\n%s", s[:80])
	}
	front := s[4 : strings.Index(s[4:], "\n---\n")+4]
	if !strings.Contains(front, `hopsesh-version: "0.2.0"`) || !strings.Contains(front, "Bash(/Users/alice/.local/bin/hopsesh plan *)") {
		t.Fatalf("frontmatter:\n%s", front)
	}
	for _, bad := range []string{"pull *)", "undo", "--yes)"} {
		if strings.Contains(front, bad) {
			t.Fatalf("allowed-tools must never pre-approve %q", bad)
		}
	}
	// The description is the part Claude Code matches on; keep it within the limit.
	desc := front[strings.Index(front, "description:"):strings.Index(front, "license:")]
	if len(desc) > 1024 {
		t.Fatalf("description too long: %d", len(desc))
	}
	if strings.Contains(s, "{{") || strings.Contains(string(files["reference.md"]), "{{") {
		t.Fatal("unrendered template")
	}
}

func TestLifecycle(t *testing.T) {
	cfg := t.TempDir()
	v1 := Params{Bin: "hopsesh", Version: "0.1.0"}
	v2 := Params{Bin: "hopsesh", Version: "0.2.0"}
	state := func(p Params) string {
		st, err := Check(cfg, p)
		if err != nil {
			t.Fatal(err)
		}
		return st.State
	}
	if state(v1) != Absent {
		t.Fatal("absent")
	}
	st, err := Install(cfg, v1, false)
	if err != nil || st.State != Current || !st.NewSkills {
		t.Fatalf("install: %+v %v", st, err)
	}
	if state(v2) != Stale {
		t.Fatal("a newer hopsesh sees the skill as stale")
	}
	if st, err := Install(cfg, v2, false); err != nil || st.State != Current {
		t.Fatalf("update: %+v %v", st, err)
	}
	// The user edits it: never overwritten without force.
	skill := filepath.Join(Dir(cfg), "SKILL.md")
	os.WriteFile(skill, []byte("my own notes"), 0o644)
	if st, _ := Check(cfg, v2); st.State != Modified || len(st.Changed) != 1 {
		t.Fatalf("modified: %+v", st)
	}
	if _, err := Install(cfg, v2, false); !errors.Is(err, ErrModified) {
		t.Fatalf("must refuse: %v", err)
	}
	if err := Remove(cfg, v2, false); err == nil {
		t.Fatal("remove must refuse an edited skill")
	}
	if st, err := Install(cfg, v2, true); err != nil || st.State != Current {
		t.Fatalf("force: %+v %v", st, err)
	}
	if b, _ := os.ReadFile(filepath.Join(Dir(cfg)+".bak-1", "SKILL.md")); string(b) != "my own notes" {
		t.Fatal("the edited skill is kept as a backup")
	}
	if err := Remove(cfg, v2, false); err != nil || state(v2) != Absent {
		t.Fatalf("remove: %v", err)
	}
	// A skill hopsesh did not write.
	os.MkdirAll(Dir(cfg), 0o755)
	os.WriteFile(skill, []byte("---\nname: hopsesh\n---\nsomeone else's"), 0o644)
	if state(v2) != Foreign {
		t.Fatal("foreign")
	}
	if _, err := Install(cfg, v2, false); !errors.Is(err, ErrForeign) {
		t.Fatalf("foreign install: %v", err)
	}
	os.Remove(skill)
	os.WriteFile(filepath.Join(Dir(cfg), recordFile), []byte(`{"format":"1","files":{"SKILL.md":"x"}}`), 0o644)
	if state(v2) != Broken {
		t.Fatal("broken")
	}
}

func TestAddRules(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(p, []byte(`{"theme":"dark","permissions":{"allow":["Bash(ls *)"]}}`), 0o600)
	added, err := AddRules(p, "hopsesh")
	if err != nil || len(added) != 9 {
		t.Fatalf("added %v %v", added, err)
	}
	var doc map[string]any
	b, _ := os.ReadFile(p)
	json.Unmarshal(b, &doc)
	if doc["theme"] != "dark" || len(doc["permissions"].(map[string]any)["allow"].([]any)) != 7 {
		t.Fatalf("settings must keep everything else: %s", b)
	}
	if !HasRules(p, "hopsesh") {
		t.Fatal("HasRules")
	}
	if again, _ := AddRules(p, "hopsesh"); len(again) != 0 {
		t.Fatal("adding twice must be a no-op")
	}
	if _, err := os.Stat(p + ".hopsesh-backup"); err != nil {
		t.Fatal("a backup of the previous settings is kept")
	}
	os.WriteFile(p, []byte(`{not json`), 0o600)
	if _, err := AddRules(p, "hopsesh"); err == nil {
		t.Fatal("invalid settings must not be overwritten")
	}
}
