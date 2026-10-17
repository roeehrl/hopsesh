package gui

import (
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestCachedGuardDoesNotDeclareCurrentHooksMissingOrEffective(t *testing.T) {
	core := &app.App{Cfg: config.Defaults(), StateDir: t.TempDir()}
	e := app.Entry{Cached: true, Machine: app.LocalName(), Agent: "codex", Session: agent.Summary{Key: agent.SessionKey{Agent: "codex", Profile: "p1", Session: "s"}}, Departure: &app.MovementNotice{Operation: "op", Status: "prepared"}}
	set := func(d *HookHealthDTO) { health.mu.Lock(); health.dto = d; health.mu.Unlock() }
	t.Cleanup(func() { set(nil) })
	for _, h := range []*HookHealthDTO{nil, {Checked: time.Now()}, {Hooks: []app.MovementHookStatus{{Agent: "codex", Profile: "p1", Installed: true}}}} {
		set(h)
		g := guardFor(core, e, "")
		if !g.Checking || g.Effective || g.Fix != "" || g.Mode != "block" {
			t.Fatalf("cache claims present/absent hooks: %+v", g)
		}
	}
	e.Cached = false
	set(nil)
	if g := guardFor(core, e, ""); !g.Checking || g.Effective {
		t.Fatal("unchecked hooks treated as missing")
	}
	set(&HookHealthDTO{Problems: []HookProblem{{State: "unknown", Message: "could not check hooks"}}})
	if g := guardFor(core, e, ""); !g.Checking || g.Effective || g.Fix != "" {
		t.Fatal("failed health check treated as absent hooks")
	}
	set(&HookHealthDTO{Hooks: []app.MovementHookStatus{{Agent: "codex", Profile: "p1", Installed: true}}})
	if g := guardFor(core, e, ""); g.Checking || !g.Effective {
		t.Fatal("fresh verified hooks remain pending")
	}
}

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
	if g := guardFor(core, e, ""); g.Mode != "block" || g.Effective || g.Problem != "not trusted" {
		t.Fatalf("untrusted hook shown as blocking: %+v", g)
	}
	set(&HookHealthDTO{Hooks: []app.MovementHookStatus{{Agent: "codex", Profile: "p1", Installed: true}}})
	if g := guardFor(core, e, ""); !g.Effective {
		t.Fatalf("trusted hook not effective: %+v", g)
	}
	_ = core.ReleaseOriginal(e.Session.Key, "op")
	if g := guardFor(core, e, ""); g.Mode != "released" {
		t.Fatalf("released: %+v", g)
	}
	e.Departure.Status = "forked"
	if guardFor(core, e, "") != nil {
		t.Fatal("a fork's original is never guarded")
	}
}

// An original on a machine without hopsesh is said to be unprotected, never "checking".
func TestOriginalOnMachineWithoutHopseshIsNotProtected(t *testing.T) {
	core := &app.App{Cfg: config.Defaults(), StateDir: t.TempDir()}
	e := app.Entry{Machine: "lurus", Agent: "claude", Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Session: "s"}}, Departure: &app.MovementNotice{Operation: "op", Status: "prepared"}}
	g := guardFor(core, e, "")
	if g == nil || g.Checking || g.Effective || !g.NoHopsesh || !strings.Contains(g.Problem, "lurus has no hopsesh") {
		t.Fatalf("%+v", g)
	}
	if g := guardFor(core, e, "0.5.0"); g.NoHopsesh || !g.Checking {
		t.Fatalf("with hopsesh there: %+v", g)
	}
}
