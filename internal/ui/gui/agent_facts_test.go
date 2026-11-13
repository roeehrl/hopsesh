package gui

import (
	"reflect"
	"testing"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// The window learns each agent's facts from its module; for Claude Code and Codex they
// are what the window used to keep in tables of its own.
func TestAgentFactsFromModules(t *testing.T) {
	reg := all.Registry()
	facts := map[agent.ID]AgentFacts{}
	for _, m := range reg.All() {
		facts[m.Spec().ID] = agentFacts(m, "darwin")
	}
	c, x := facts["claude"], facts["codex"]
	if c.Short != "Claude" || x.Short != "Codex" || !c.Local || !x.Local || !c.Accounts || !x.Accounts {
		t.Fatalf("claude %+v codex %+v", c, x)
	}
	if got := []ToolDTO{{"claude-terminal", "Claude Code · terminal"}, {"claude-desktop", "Claude desktop"}}; !reflect.DeepEqual(c.Tools, got) {
		t.Fatalf("claude tools %+v", c.Tools)
	}
	if got := []ToolDTO{{"codex-terminal", "Codex · terminal"}, {"codex-app", "Codex app"}}; !reflect.DeepEqual(x.Tools, got) {
		t.Fatalf("codex tools %+v", x.Tools)
	}
	if c.AppName != "Claude" || x.AppName != "ChatGPT" || !c.Desktop.OwnSignIn || !x.Desktop.Shared || c.Desktop.Shared || x.Desktop.OwnSignIn {
		t.Fatalf("apps: claude %+v %+v codex %+v %+v", c.AppName, c.Desktop, x.AppName, x.Desktop)
	}
	if c.DefaultRoot != "~/.claude" || c.InheritsSignIn || !x.InheritsSignIn {
		t.Fatalf("folders: claude %+v codex %+v", c, x)
	}
	if c.HookReview || !x.HookReview || !c.ResumeAcrossAccounts || x.ResumeAcrossAccounts {
		t.Fatalf("hooks and accounts: claude %+v codex %+v", c, x)
	}
	if c.RemoteControlTyped != "/remote-control" || x.RemoteControlTyped != "" {
		t.Fatalf("remote control: %q %q", c.RemoteControlTyped, x.RemoteControlTyped)
	}
	// A cloud-only module has no columns and no local folder.
	if f := facts["copilot"]; f.Local || len(f.Tools) != 0 {
		t.Fatalf("copilot %+v", f)
	}
}

func TestPlanLabel(t *testing.T) {
	for _, c := range []struct{ plans, label, want string }{
		{"Claude", "max", "Claude Max"},
		{"Claude", "Claude Pro", "Claude Pro"},
		{"", "ChatGPT plus", "ChatGPT Plus"},
		{"Claude", "", ""},
	} {
		if got := planLabel(c.plans, c.label); got != c.want {
			t.Errorf("planLabel(%q, %q) = %q, want %q", c.plans, c.label, got, c.want)
		}
	}
}
