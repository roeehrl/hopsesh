package gui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
		g := guardFor(core, e, nil)
		if !g.Checking || g.Effective || g.Fix != "" || g.Mode != "block" || g.State != guardUnknown {
			t.Fatalf("cache claims present/absent hooks: %+v", g)
		}
	}
	e.Cached = false
	set(nil)
	// Not checked yet and no check running: unknown, saying so, never "checking".
	if g := guardFor(core, e, nil); g.Checking || g.Effective || g.State != guardUnknown || g.Action != actionSettings || g.Problem == "" {
		t.Fatalf("unchecked hooks: %+v", g)
	}
	health.mu.Lock()
	health.reading++
	health.mu.Unlock()
	g := guardFor(core, e, nil)
	health.mu.Lock()
	health.reading--
	health.mu.Unlock()
	if !g.Checking || g.Effective || g.State != guardUnknown {
		t.Fatalf("hooks being checked: %+v", g)
	}
	set(&HookHealthDTO{Problems: []HookProblem{{State: "unknown", Message: "could not check hooks"}}})
	if g := guardFor(core, e, nil); g.Checking || g.Effective || g.State != guardUnknown || g.Action == actionSetUp || g.Problem != "could not check hooks" {
		t.Fatalf("failed health check treated as absent hooks: %+v", g)
	}
	set(&HookHealthDTO{Hooks: []app.MovementHookStatus{{Agent: "codex", Profile: "p1", Installed: true}}})
	if g := guardFor(core, e, nil); g.Checking || !g.Effective || g.State != guardProtected {
		t.Fatalf("fresh verified hooks remain pending: %+v", g)
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
	if g := guardFor(core, e, nil); g.Mode != "block" || g.Effective || g.Problem != "not trusted" || g.State != guardUnprotected || g.Action != actionApprove {
		t.Fatalf("untrusted hook shown as blocking: %+v", g)
	}
	set(&HookHealthDTO{Problems: []HookProblem{{Agent: "codex", Profile: "p1", State: "not-installed", Message: "not installed", Fix: "install"}}})
	if g := guardFor(core, e, nil); g.State != guardUnprotected || g.Action != actionSetUp || g.Machine != app.LocalName() {
		t.Fatalf("missing hook: %+v", g)
	}
	set(&HookHealthDTO{Hooks: []app.MovementHookStatus{{Agent: "codex", Profile: "p1", Installed: true}}})
	if g := guardFor(core, e, nil); !g.Effective || g.State != guardProtected {
		t.Fatalf("trusted hook not effective: %+v", g)
	}
	_ = core.ReleaseOriginal(e.Session.Key, "op")
	if g := guardFor(core, e, nil); g.Mode != "released" || g.State != "released" {
		t.Fatalf("released: %+v", g)
	}
	e.Departure.Status = "forked"
	if guardFor(core, e, nil) != nil {
		t.Fatal("a fork's original is never guarded")
	}
}

// remoteOriginal is the copy a move left on roee-macbook, in its "Claude Code default"
// folder, with what this machine last read of roee-macbook's hooks (agents; nil: never).
func remoteOriginal(t *testing.T, read time.Time, agents ...app.AgentSetup) (*app.App, *app.Inventory, app.Entry) {
	t.Helper()
	core := &app.App{Cfg: config.Defaults(), StateDir: t.TempDir()}
	if agents != nil {
		writeHooksRead(t, core, "roee-macbook", "0.6.4", read, agents)
	}
	m := &app.Machine{Name: "roee-macbook", Status: app.StatusOK, Hopsesh: "0.6.4", Presence: app.PresenceLogin}
	e := app.Entry{Machine: "roee-macbook", Agent: "claude", AgentName: "Claude Code", Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Profile: "9f5c", Session: "8de1"}},
		Departure: &app.MovementNotice{Operation: "op", Status: "continued", Machine: "tnt-studio", Agent: "claude", AgentName: "Claude Code"}}
	return core, &app.Inventory{Machines: []*app.Machine{m}, Entries: []app.Entry{e}}, e
}

// writeHooksRead keeps a reading of a machine's hooks, as MachineStatuses does.
func writeHooksRead(t *testing.T, core *app.App, machine, version string, at time.Time, agents []app.AgentSetup) {
	t.Helper()
	all := map[string]any{}
	path := filepath.Join(core.StateDir, "machine-status.json")
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &all)
	}
	all[machine] = map[string]any{"version": version, "agents": agents, "at": at}
	b, _ := json.Marshal(all)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// The folders on roee-macbook as read on 26 Oct: the default folder's hook is installed,
// the copy's own "Claude Code default" folder has none.
func roeeFolders() []app.AgentSetup {
	off := app.Check{State: app.CheckOff, Text: "not installed", Reason: "originals moved from here aren't blocked or warned", Fix: app.FixHooks}
	return []app.AgentSetup{
		{Agent: "claude", Name: "Claude Code", Profile: "0d5e", Hook: app.Check{State: app.CheckOK, Text: "installed"}},
		{Agent: "claude", Name: "Claude Code", Profile: "9ed8", ProfileLabel: "claude code personal", Hook: off},
		{Agent: "claude", Name: "Claude Code", Profile: "9f5c", ProfileLabel: "Claude Code default", Hook: off},
		{Agent: "codex", Name: "Codex", Profile: "7ae2", Hook: app.Check{State: app.CheckOK, Text: "installed · trust checked at login"}},
	}
}

// noReads keeps guardFor from reading any machine's hooks in a test, or records them.
func noReads(t *testing.T, read func(ctx context.Context, core *app.App, m *app.Machine)) {
	t.Helper()
	old := readGuardStatus
	guardReads.mu.Lock()
	window := guardReads.window
	guardReads.running, guardReads.started, guardReads.window = nil, nil, nil
	guardReads.mu.Unlock()
	if read == nil {
		read = func(context.Context, *app.App, *app.Machine) { t.Error("read a machine's hooks") }
	}
	readGuardStatus = read
	t.Cleanup(func() {
		readGuardStatus = old
		guardReads.mu.Lock()
		guardReads.running, guardReads.started, guardReads.window = nil, nil, window
		guardReads.mu.Unlock()
		forgetStatuses()
	})
}

// The copy left on another machine is protected by that machine's hook for its own agent
// folder, as hopsesh last read it: the case behind "Checking…" forever was a folder with
// no hook while the default folder had one. Each answer is definite, or unknown with why.
func TestRemoteOriginalProtectionIsDefinite(t *testing.T) {
	noReads(t, nil)
	now := time.Now().UTC().Truncate(time.Second)
	core, inv, e := remoteOriginal(t, now, roeeFolders()...)
	g := guardFor(core, e, inv)
	if g.Checking || g.Effective || g.State != guardUnprotected || g.Action != actionSetUp || g.Machine != "roee-macbook" || !g.CheckedAt.Equal(now) ||
		!strings.Contains(g.Problem, "The Claude Code hook isn't set up on roee-macbook for “Claude Code default”, so nothing stops new prompts in this copy.") {
		t.Fatalf("hook off there: %+v", g)
	}
	inv.Machines[0].Presence = app.PresenceInstalled // hopsesh there can't install it from here
	if g := guardFor(core, e, inv); g.Action != actionMachines || !strings.Contains(g.Fix, "Start hopsesh at login on roee-macbook") {
		t.Fatalf("hook off, hopsesh not in the login session: %+v", g)
	}
	inv.Machines[0].Presence = app.PresenceLogin
	e.Session.Key.Profile = "0d5e"
	if g := guardFor(core, e, inv); !g.Effective || g.State != guardProtected || g.Checking || g.Problem != "" {
		t.Fatalf("hook there: %+v", g)
	}
	e.Session.Key.Profile = "beef"
	if g := guardFor(core, e, inv); g.Checking || g.State != guardUnknown || !strings.Contains(g.Problem, "wasn't found") || g.Action != actionMachines {
		t.Fatalf("a folder not there is not the default folder's: %+v", g)
	}
	codex := e
	codex.Agent, codex.AgentName, codex.Session.Key = "codex", "Codex", agent.SessionKey{Agent: "codex", Profile: "7ae2", Session: "c1"}
	if g := guardFor(core, codex, inv); g.Checking || g.Effective || g.State != guardUnknown || !strings.Contains(g.Problem, "over SSH") {
		t.Fatalf("Codex approval read over SSH: %+v", g)
	}
	inv.Machines[0].Hopsesh = "0.5.0"
	writeHooksRead(t, core, "roee-macbook", "0.5.0", now, roeeFolders())
	if g := guardFor(core, e, inv); g.State != guardUnprotected || g.Action != actionUpdate || g.NoHopsesh || g.Checking {
		t.Fatalf("too old to block: %+v", g)
	}
	inv.Machines[0].Hopsesh, inv.Machines[0].Presence = "", app.PresenceSSH
	if g := guardFor(core, e, inv); !g.NoHopsesh || g.State != guardUnprotected || g.Checking || !strings.Contains(g.Problem, "roee-macbook has no hopsesh") {
		t.Fatalf("no hopsesh: %+v", g)
	}
	if g := guardFor(core, e, &app.Inventory{}); g.Checking || g.State != guardUnknown || !strings.Contains(g.Problem, "wasn't scanned") {
		t.Fatalf("not scanned: %+v", g)
	}
}

// Hooks never read, or read too long ago, are read in the background once: the copy
// shows "checking" only while that runs, then the answer, and is not read again soon
// after, so a machine that can't be read says why instead of checking for ever.
func TestRemoteOriginalReadsMissingHooksOnce(t *testing.T) {
	started, finish := make(chan struct{}, 4), make(chan struct{})
	done := make(chan struct{}, 4)
	var reads int
	noReads(t, func(ctx context.Context, core *app.App, m *app.Machine) {
		reads++
		started <- struct{}{}
		<-finish
		folders := roeeFolders()
		folders[2].Hook = app.Check{State: app.CheckOK, Text: "installed"}
		writeHooksRead(t, core, m.Name, m.Hopsesh, time.Now(), folders)
		done <- struct{}{}
	})
	core, inv, e := remoteOriginal(t, time.Time{})
	g := guardFor(core, e, inv)
	if !g.Checking || g.State != guardUnknown || g.Effective {
		t.Fatalf("never read: %+v", g)
	}
	<-started
	if g := guardFor(core, e, inv); !g.Checking {
		t.Fatalf("while reading: %+v", g)
	}
	close(finish)
	<-done
	waitFor(t, func() bool { return !guardFor(core, e, inv).Checking })
	if g := guardFor(core, e, inv); g.State != guardProtected || !g.Effective {
		t.Fatalf("read: %+v", g)
	}
	if reads != 1 {
		t.Fatalf("read %d times", reads)
	}
	// Read long ago and not again since (recently tried): unknown, saying when.
	writeHooksRead(t, core, "roee-macbook", "0.6.4", time.Now().Add(-2*time.Hour), roeeFolders())
	e.Session.Key.Profile = "0d5e"
	if g := guardFor(core, e, inv); g.Checking || g.State != guardUnknown || !strings.Contains(g.Problem, "were last read") || g.CheckedAt.IsZero() {
		t.Fatalf("stale: %+v", g)
	}
	// A machine the scan didn't reach is not read: unknown, saying so.
	core, inv, e = remoteOriginal(t, time.Time{})
	inv.Machines[0].Status, inv.Machines[0].Hopsesh = app.StatusError, ""
	if g := guardFor(core, e, inv); g.Checking || g.State != guardUnknown || !strings.Contains(g.Problem, "wasn't reached") {
		t.Fatalf("not reached: %+v", g)
	}
	if reads != 1 {
		t.Fatalf("read %d times", reads)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for range 200 {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out")
}
