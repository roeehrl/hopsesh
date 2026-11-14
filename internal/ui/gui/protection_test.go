package gui

import (
	"reflect"
	"testing"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// What a machine's protection still needs: hooks not installed or awaiting approval, and
// a skill that is not current; an agent that cannot take a hook asks nothing.
func TestNeedsOfNamesWhatIsLeft(t *testing.T) {
	names := (&App{core: app.New(config.Defaults(), all.Registry(), t.TempDir(), nil)}).agentName
	p := &app.Protection{Command: true, Skill: app.SkillReport{State: "stale"}, Hooks: []app.MovementHookStatus{
		{Agent: "claude", Supported: true, Installed: true},
		{Agent: "claude", Supported: true, Installed: false},
		{Agent: "codex", Supported: true, Installed: true, Trust: &agent.HookTrust{State: agent.HookNeedsReview}},
		{Agent: "codex", Supported: false},
	}}
	if got, want := needsOf(p, names), []string{"Claude Code hook", "Codex hook approval", "skill"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("%v, want %v", got, want)
	}
	if got := needsOf(&app.Protection{Command: true, Skill: app.SkillReport{State: "current"}}, names); len(got) != 0 {
		t.Fatalf("all set, yet %v", got)
	}
}
