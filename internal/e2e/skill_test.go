package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/integrate"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/internal/ui/skill"
)

func TestSkillInEveryAgentWithoutDrift(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude-config"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	for _, d := range []string{"claude-config", ".codex"} {
		os.MkdirAll(filepath.Join(home, d), 0o700)
	}
	reg, err := registry.New(claude.New(), codex.New())
	if err != nil {
		t.Fatal(err)
	}
	a := app.New(config.Defaults(), reg, t.TempDir(), nil)
	ctx := context.Background()
	files, _ := skill.Render(skill.NewParams("hopsesh", "0.3.0", []string{"Claude Code", "Codex"}, []string{"claude", "codex"}))

	if rep := a.Skill(ctx, files, "hopsesh"); rep.InSync || len(rep.Copies) != 2 {
		t.Fatalf("before install: %+v", rep)
	}
	rep, err := a.InstallSkill(ctx, files, "0.3.0", "hopsesh", false, true)
	if err != nil || !rep.InSync || len(rep.Rules) != 2 {
		t.Fatalf("install: %+v %v", rep, err)
	}
	cl, _ := os.ReadFile(filepath.Join(home, "claude-config", "skills", "hopsesh", "SKILL.md"))
	cx, _ := os.ReadFile(filepath.Join(home, ".agents", "skills", "hopsesh", "SKILL.md"))
	if len(cl) == 0 || string(cl) != string(cx) {
		t.Fatal("every agent gets byte-identical skill files")
	}
	settings, _ := os.ReadFile(filepath.Join(home, "claude-config", "settings.json"))
	rules, _ := os.ReadFile(filepath.Join(home, ".codex", "rules", "hopsesh.rules"))
	if !strings.Contains(string(settings), "Bash(hopsesh ls *)") || !strings.Contains(string(rules), `prefix_rule(pattern=["hopsesh", "pull"], decision="prompt")`) {
		t.Fatalf("rules:\n%s\n%s", settings, rules)
	}

	// Drift: one copy edited by the user is reported and protected.
	os.WriteFile(filepath.Join(home, ".agents", "skills", "hopsesh", "SKILL.md"), []byte("edited"), 0o644)
	rep = a.Skill(ctx, files, "hopsesh")
	if rep.InSync || rep.State != integrate.Modified {
		t.Fatalf("drift must be reported: %+v", rep)
	}
	if _, err := a.InstallSkill(ctx, files, "0.3.0", "hopsesh", false, false); err == nil {
		t.Fatal("an edited copy must stop the install without --force")
	}
	if rep, err = a.InstallSkill(ctx, files, "0.3.0", "hopsesh", true, false); err != nil || !rep.InSync {
		t.Fatalf("force: %+v %v", rep, err)
	}
	// A newer build's files make every copy stale, together.
	newer, _ := skill.Render(skill.NewParams("hopsesh", "0.3.1", []string{"Claude Code", "Codex"}, []string{"claude", "codex"}))
	if rep = a.Skill(ctx, newer, "hopsesh"); rep.State != integrate.Stale {
		t.Fatalf("stale: %+v", rep)
	}
	if err := a.RemoveSkill(ctx, files, "hopsesh", true); err != nil {
		t.Fatal(err)
	}
	if rep = a.Skill(ctx, files, "hopsesh"); rep.State != integrate.Absent {
		t.Fatalf("after remove: %+v", rep)
	}
	settings, _ = os.ReadFile(filepath.Join(home, "claude-config", "settings.json"))
	if strings.Contains(string(settings), "hopsesh") {
		t.Fatalf("rules must be removed: %s", settings)
	}
}
