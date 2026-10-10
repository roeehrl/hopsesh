package gui

import (
	"testing"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestHookProblemsNameUntrustedCodexHooks(t *testing.T) {
	hooks := []app.MovementHookStatus{
		{Agent: "claude", Installed: true, Enabled: true, Supported: true},
		{Agent: "codex", Profile: "p1", Installed: true, Enabled: true, Supported: true, Trust: &agent.HookTrust{State: agent.HookNeedsReview, Fix: "Open Codex and run /hooks"}},
		{Agent: "amp", Enabled: true, Supported: true},
	}
	got := hookProblems(hooks, config.OriginalBlock)
	if len(got) != 2 || got[0].Agent != "codex" || got[0].State != agent.HookNeedsReview || got[0].Fix == "" || got[1].State != "not-installed" {
		t.Fatalf("problems: %+v", got)
	}
	if len(hookProblems(hooks, config.OriginalOff)) != 0 {
		t.Fatal("protection off reports hook problems")
	}
}

func TestGuardIsEffectiveOnlyWithATrustedHook(t *testing.T) {
	core := &app.App{Cfg: config.Defaults(), StateDir: t.TempDir()}
	e := app.Entry{Machine: app.LocalName(), Agent: "codex", Session: agent.Summary{Key: agent.SessionKey{Agent: "codex", Profile: "p1", Session: "s"}},
		Departure: &app.MovementNotice{Operation: "op", Status: "continued"}}
	set := func(d *HookHealthDTO) { health.mu.Lock(); health.dto = d; health.mu.Unlock() }
	t.Cleanup(func() { set(nil) })
	set(&HookHealthDTO{Hooks: []app.MovementHookStatus{{Agent: "codex", Profile: "p1", Installed: true}},
		Problems: []HookProblem{{Agent: "codex", Profile: "p1", State: agent.HookNeedsReview, Message: "not trusted", Fix: "approve"}}})
	if g := guardFor(core, e); g.Mode != "block" || g.Effective || g.Problem != "not trusted" {
		t.Fatalf("untrusted hook shown as blocking: %+v", g)
	}
	set(&HookHealthDTO{Hooks: []app.MovementHookStatus{{Agent: "codex", Profile: "p1", Installed: true}}})
	if g := guardFor(core, e); !g.Effective {
		t.Fatalf("trusted hook not effective: %+v", g)
	}
	_ = core.ReleaseOriginal(e.Session.Key, "op")
	if g := guardFor(core, e); g.Mode != "released" {
		t.Fatalf("released: %+v", g)
	}
	e.Departure.Status = "forked"
	if guardFor(core, e) != nil {
		t.Fatal("a fork's original is never guarded")
	}
}
