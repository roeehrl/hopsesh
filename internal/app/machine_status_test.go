package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/integrate"
	"github.com/roeehrl/hopsesh/internal/core/peer"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// The newest hopsesh on any machine is the baseline; an older one has an update, and one
// too old to talk to the others is an error. A development build is never a baseline.
func TestNewestIsTheBaseline(t *testing.T) {
	ss := []MachineStatus{
		{Machine: "studio", Version: "0.6.1", Hopsesh: Check{State: CheckOK, Text: "0.6.1"}},
		{Machine: "macbook", Version: "0.5.0", Hopsesh: Check{State: CheckOK, Text: "0.5.0"}},
		{Machine: "old", Version: "0.3.2", Hopsesh: Check{State: CheckOK, Text: "0.3.2"}},
		{Machine: "here", Version: "dev", Hopsesh: Check{State: CheckOK, Text: "dev"}},
		{Machine: "nas", Hopsesh: Check{State: CheckOff, Text: "not installed", Fix: FixSetUp}},
	}
	v, on := Newest(ss)
	if v != "0.6.1" || on != "studio" {
		t.Fatalf("newest: %s on %s", v, on)
	}
	want := map[string]string{"studio": CheckOK, "macbook": CheckWarn, "old": CheckError, "here": CheckOK, "nas": CheckOff}
	for _, s := range ss {
		s.Newest, s.NewestOn = v, on
		got := hopseshCheck(s, s.Hopsesh)
		if got.State != want[s.Machine] {
			t.Errorf("%s: %+v, want %s", s.Machine, got, want[s.Machine])
		}
		if got.State == CheckWarn && (got.Fix != FixUpdate || !strings.Contains(got.Reason, "0.6.1 is the newest, on studio")) {
			t.Errorf("%s: an older hopsesh is offered the update: %+v", s.Machine, got)
		}
	}
	if !atLeast("0.4.0", peer.FirstVersion) || atLeast("0.3.9", peer.FirstVersion) {
		t.Fatal("too old is below the first version that speaks the peer protocol")
	}
}

// A session moved from a machine blocks its original there only with a hopsesh that reads
// where it went and that agent folder's own hook running.
func TestCanBlock(t *testing.T) {
	ok := Check{State: CheckOK, Text: "installed"}
	base := MachineStatus{Version: "0.6.1", DetailsAt: time.Now(), Agents: []AgentSetup{
		{Agent: "claude", Name: "Claude Code", Profile: "p-default", Hook: ok},
		{Agent: "claude", Name: "Claude Code", Profile: "p-work", ProfileLabel: "work", Hook: Check{State: CheckOff, Text: "not installed", Fix: FixHooks}},
		{Agent: "codex", Name: "Codex", Profile: "c", Hook: Check{State: CheckWarn, Text: "approve in Codex", Fix: FixApprove}},
	}}
	cases := []struct {
		name    string
		s       MachineStatus
		id      agent.ID
		profile string
		can     bool
		why     string
	}{
		{"ready", base, "claude", "p-default", true, ""},
		{"that folder has no hook", base, "claude", "p-work", false, "isn't installed"},
		{"another folder's hook says nothing about this one", base, "claude", "elsewhere", false, "wasn't found"},
		{"a hook waiting for approval doesn't block yet", base, "codex", "x", false, "approve in Codex"},
		{"too old to read lineage/6", func() MachineStatus { s := base; s.Version = "0.5.0"; return s }(), "claude", "p-default", false, "needs " + MinBlockVersion},
		{"no hopsesh", MachineStatus{}, "claude", "", false, "isn't installed there"},
		{"hooks never read", MachineStatus{Version: "0.6.1"}, "claude", "", false, "haven't been checked"},
		{"hook unknown", MachineStatus{Version: "0.6.1", DetailsAt: time.Now(), Agents: []AgentSetup{{Agent: "claude", Hook: Check{State: CheckUnknown, Reason: "timed out"}}}}, "claude", "", false, "couldn't be checked: timed out"},
	}
	for _, c := range cases {
		can, why := c.s.CanBlock(c.id, c.profile)
		if can != c.can || c.why != "" && !strings.Contains(strings.Join(why, "; "), c.why) {
			t.Errorf("%s: %v %v", c.name, can, why)
		}
	}
	if MinBlockVersion != "0.6.0" {
		t.Fatalf("blocking needs the first hopsesh that reads lineage/6, not %s", MinBlockVersion)
	}
}

// Whether a copy left on a machine is protected is that agent folder's own hook, as last
// read: the default folder's hook never vouches for another folder, a reading too old or
// never made is unknown (not a yes), and Codex's approval can't be told over SSH.
func TestOriginalProtection(t *testing.T) {
	a := &App{}
	m := &Machine{Name: "roee-macbook", Status: StatusOK, Hopsesh: "0.6.4", Presence: PresenceLogin}
	read := statusDetails{Version: "0.6.4", At: time.Now(), Agents: []AgentSetup{
		{Agent: "claude", Name: "Claude Code", Profile: "0d5e", Hook: Check{State: CheckOK, Text: "installed"}},
		{Agent: "claude", Name: "Claude Code", Profile: "9ed8", ProfileLabel: "claude code personal", Hook: Check{State: CheckOff, Text: "not installed", Fix: FixHooks}},
		{Agent: "claude", Name: "Claude Code", Profile: "9f5c", ProfileLabel: "Claude Code default", Hook: Check{State: CheckOff, Text: "not installed", Fix: FixHooks}},
		{Agent: "codex", Name: "Codex", Profile: "7ae2", Hook: Check{State: CheckOK, Text: "installed · trust checked at login"}},
	}}
	s := a.machineStatus(m, read, true)
	cases := []struct {
		name, profile    string
		id               agent.ID
		verdict, fix, in string
	}{
		{"the copy's folder has no hook, though the default one has", "9f5c", "claude", ProtectedNo, FixHooks, "isn't installed"},
		{"the default folder's own copy", "0d5e", "claude", ProtectedYes, "", ""},
		{"a folder that isn't there", "beef", "claude", ProtectedUnknown, "", "wasn't found"},
		{"Codex's trust over SSH", "7ae2", "codex", ProtectedUnknown, FixServe, "over SSH"},
	}
	for _, c := range cases {
		v, why, fix := s.OriginalProtection(c.id, c.profile)
		if v != c.verdict || fix != c.fix || !strings.Contains(why, c.in) {
			t.Errorf("%s: %s %q %s", c.name, v, why, fix)
		}
	}
	// Stale: an installed hook last seen long ago is unknown; a missing one stays missing.
	read.At = time.Now().Add(-2 * statusStale)
	s = a.machineStatus(m, read, true)
	if v, why, _ := s.OriginalProtection("claude", "0d5e"); v != ProtectedUnknown || !strings.Contains(why, "last seen like this") {
		t.Errorf("stale installed hook: %s %q", v, why)
	}
	if v, _, _ := s.OriginalProtection("claude", "9f5c"); v != ProtectedNo {
		t.Errorf("stale missing hook: %s", v)
	}
	// Never read: unknown, whatever the folders.
	s = a.machineStatus(m, statusDetails{}, false)
	if v, why, _ := s.OriginalProtection("claude", "9f5c"); v != ProtectedUnknown || !strings.Contains(why, "haven't been checked") {
		t.Errorf("never read: %s %q", v, why)
	}
	// Not reached, hopsesh not known there: unknown, not "no hopsesh".
	s = a.machineStatus(&Machine{Name: "away", Status: StatusError}, statusDetails{}, false)
	if v, _, fix := s.OriginalProtection("claude", ""); v != ProtectedUnknown || fix != "" {
		t.Errorf("not reached: %s %s", v, fix)
	}
}

// A hook turned off reads as turned off, whether the agent says why or protection of
// originals is off in hopsesh there (it gives no reason).
func TestHookTurnedOffIsNotOK(t *testing.T) {
	for _, h := range []MovementHookStatus{
		{Agent: "claude", Supported: true, Installed: true},
		{Agent: "claude", Supported: true, Installed: true, Reason: "disableAllHooks is set"},
	} {
		if c := hookCheck(h); c.State != CheckWarn || c.Reason == "" {
			t.Errorf("%+v: %+v", h, c)
		}
	}
	if c := hookCheck(MovementHookStatus{Agent: "claude", Supported: true, Installed: true, Enabled: true}); c.State != CheckOK {
		t.Fatalf("enabled: %+v", c)
	}
}

// Receiving is green only when hopsesh runs in the login session there with receiving on.
func TestLoginAndReceiving(t *testing.T) {
	a := &App{Cfg: config.Config{}}
	off, on := false, true
	cases := []struct {
		m                Machine
		login, receiving string
		fix              string
	}{
		{Machine{Presence: PresenceLogin, receive: &on}, CheckOK, CheckOK, ""},
		{Machine{Presence: PresenceLogin, receive: &off}, CheckOK, CheckOff, FixReceive},
		{Machine{Presence: PresenceInstalled, PresenceWhy: "hopsesh on x is not in a login session"}, CheckOff, CheckOff, ""},
		{Machine{Presence: PresenceOlder, Hopsesh: "0.3.0"}, CheckError, CheckError, FixUpdate},
		{Machine{Presence: PresenceSSH}, CheckOff, CheckOff, ""},
		{Machine{Local: true, listening: true}, CheckOK, CheckOff, FixReceive}, // receiving is off in this config
	}
	for i, c := range cases {
		l, r := c.m.loginChecks(a)
		if l.State != c.login || r.State != c.receiving || r.Fix != c.fix {
			t.Errorf("%d (%s): login %+v receiving %+v", i, c.m.Presence, l, r)
		}
	}
	a.Cfg.Peer.Receive = true
	if _, r := (&Machine{Local: true, listening: true}).loginChecks(a); r.State != CheckOK {
		t.Fatalf("here, receiving on and serve running: %+v", r)
	}
	if l, r := (&Machine{Local: true}).loginChecks(a); r.State != CheckOff || r.Text != "switched on, not receiving yet" || l.Fix != FixServe {
		t.Fatalf("here, receiving on without serve: login %+v receiving %+v", l, r)
	}
}

// Over SSH, a hook is the hopsesh notice hook for that agent running a program that is
// there; a skill copy is current when the hopsesh there would not write a newer one.
func TestHooksAndSkillReadOverSSH(t *testing.T) {
	dir := t.TempDir()
	prog := filepath.Join(dir, "bin", "hopsesh")
	os.MkdirAll(filepath.Dir(prog), 0o700)
	os.WriteFile(prog, []byte("#!/bin/sh\n"), 0o700)
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, []byte(body), 0o600)
		return p
	}
	fsys := host.LocalFS()
	// JSON escapes nothing in a single-quoted shell command; some writers escape quotes.
	settings := write("claude/settings.json", `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"'`+prog+`' notice-hook --agent 'claude' --profile '' '--config-dir' '/x'"}]}]}}`)
	if c := remoteHook(fsys, settings, "claude"); c.State != CheckOK {
		t.Fatalf("installed: %+v", c)
	}
	if c := remoteHook(fsys, settings, "codex"); c.State != CheckOff || c.Fix != FixHooks {
		t.Fatalf("another agent's hook is not this one's: %+v", c)
	}
	gone := write("claude2/settings.json", `{"command":"'`+filepath.Join(dir, "moved", "hopsesh")+`' notice-hook --agent 'claude' --profile ''"}`)
	if c := remoteHook(fsys, gone, "claude"); c.State != CheckError || !strings.Contains(c.Reason, "isn't there") {
		t.Fatalf("a hook whose program is gone is broken: %+v", c)
	}
	codex := write("codex/hooks.json", `{"hooks":[{"command":"'`+prog+`' notice-hook --agent 'codex' --profile 'p1'"}]}`)
	if c := remoteHook(fsys, codex, "codex"); c.State != CheckOK || !strings.Contains(c.Text, "trust") {
		t.Fatalf("codex: %+v", c)
	}
	if c := remoteHook(fsys, filepath.Join(dir, "none.json"), "claude"); c.State != CheckOff {
		t.Fatalf("no file: %+v", c)
	}

	skill := filepath.Join(dir, "skills", integrate.SkillName)
	if c := remoteSkill(fsys, skill, "0.6.1"); c.State != CheckOff || c.Fix != FixSkill {
		t.Fatalf("absent: %+v", c)
	}
	write("skills/hopsesh/README", "mine")
	if c := remoteSkill(fsys, skill, "0.6.1"); c.Text != "not hopsesh's" {
		t.Fatalf("foreign: %+v", c)
	}
	write("skills/hopsesh/"+integrate.RecordFile, `{"hopseshVersion":"0.6.0","files":{}}`)
	if c := remoteSkill(fsys, skill, "0.6.1"); c.State != CheckError {
		t.Fatalf("no SKILL.md: %+v", c)
	}
	write("skills/hopsesh/SKILL.md", "# hopsesh")
	if c := remoteSkill(fsys, skill, "0.6.1"); c.State != CheckWarn || c.Text != "outdated" {
		t.Fatalf("written by an older hopsesh: %+v", c)
	}
	if c := remoteSkill(fsys, skill, "0.6.0"); c.State != CheckOK {
		t.Fatalf("current: %+v", c)
	}
}

// Details too old to trust, on a machine that could not be read again, turn unknown
// instead of staying green; a fix stays a fix.
func TestStaleDetailsTurnUnknown(t *testing.T) {
	a := &App{}
	m := &Machine{Name: "macbook", Status: StatusError, Error: "timed out", Hopsesh: "0.6.1"}
	d := statusDetails{Version: "0.6.1", At: time.Now().Add(-statusDetailsTTL - time.Minute), CLI: Check{State: CheckOK, Text: "on PATH"},
		Agents: []AgentSetup{{Agent: "claude", Hook: Check{State: CheckOK, Text: "installed"}, Skill: Check{State: CheckOff, Text: "not installed", Fix: FixSkill}}}}
	s := a.machineStatus(m, d, true)
	if !s.Stale || s.CLI.State != CheckUnknown || s.Agents[0].Hook.State != CheckUnknown || s.Agents[0].Skill.Fix != FixSkill {
		t.Fatalf("stale: %+v", s)
	}
	if s.Reach.State != CheckError || s.Login.State != CheckUnknown {
		t.Fatalf("unreached: %+v", s)
	}
	m.Status = StatusOK
	d.At = time.Now()
	if s := a.machineStatus(m, d, true); s.Stale || s.CLI.State != CheckOK {
		t.Fatalf("fresh: %+v", s)
	}
	s.count()
	if s.Todo != 1 {
		t.Fatalf("todo counts the checks with a fix: %d", s.Todo)
	}
}

// The status of what hopsesh there reports: one line per agent folder, its skill matched
// by the names the report gives each copy.
func TestProtectionSetups(t *testing.T) {
	reg, err := registry.New(claude.New(), codex.New())
	if err != nil {
		t.Fatal(err)
	}
	a := &App{Reg: reg}
	p := Protection{Hooks: []MovementHookStatus{
		{Agent: "claude", Profile: "d", Supported: true, Installed: true, Enabled: true},
		{Agent: "claude", Profile: "w", ProfileLabel: "work", Supported: true},
		{Agent: "codex", Profile: "c", Supported: true, Installed: true, Enabled: true, Trust: &agent.HookTrust{State: agent.HookNeedsReview, Fix: "open /hooks in Codex"}},
	}, Skill: SkillReport{Copies: []SkillCopy{
		{Agents: []string{"Claude Code", "Codex"}, Status: integrate.SkillStatus{State: integrate.Current}},
		{Agents: []string{"Claude Code (work)"}, Status: integrate.SkillStatus{State: integrate.Stale, Version: "0.5.0"}},
	}}}
	got := a.protectionSetups(p, &Machine{})
	if len(got) != 3 {
		t.Fatalf("%+v", got)
	}
	if got[0].Hook.State != CheckOK || got[0].Skill.State != CheckOK {
		t.Errorf("default: %+v", got[0])
	}
	if got[1].Hook.Fix != FixHooks || got[1].Skill.State != CheckWarn {
		t.Errorf("work: %+v", got[1])
	}
	if got[2].Hook.Fix != FixApprove || got[2].Hook.Reason != "open /hooks in Codex" {
		t.Errorf("codex: %+v", got[2])
	}
}

// The planner's guard reads only the hook of the session's agent folder there, and the
// version the scan found: a hook that runs a program that is there makes the original
// protectable; a missing hook, or a hopsesh too old for lineage/6, says why not.
func TestGuardReadsTheHook(t *testing.T) {
	reg, err := registry.New(claude.New())
	if err != nil {
		t.Fatal(err)
	}
	a := &App{Reg: reg}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prog := filepath.Join(root, "hopsesh")
	os.WriteFile(prog, []byte("#!/bin/sh\n"), 0o700)
	in := agent.Install{Agent: "claude", Version: "2.1.300", Roots: map[string]string{"home": filepath.Join(root, ".claude")}, Present: true}
	os.MkdirAll(in.Root("home"), 0o700)
	hm := &host.Machine{Name: "box", Local: true, Facts: host.Facts{OS: runtime.GOOS, Home: root, Env: map[string]string{}, Binaries: map[string]agent.BinaryFact{}}}
	m := &Machine{Name: "box", Status: StatusOK, Hopsesh: "0.6.1", host: hm, Agents: []AgentState{{Agent: "claude", Name: "Claude Code", Install: in}}}
	m.Local = false // a remote machine as the scan saw it (the version is its own)
	key := agent.SessionKey{Agent: "claude"}
	ctx := context.Background()

	if g := a.Guard(ctx, m, key); g == nil || g.Ready || !strings.Contains(strings.Join(g.Why, " "), "hook isn't installed") {
		t.Fatalf("no hook: %+v", g)
	}
	settings := `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"'` + prog + `' notice-hook --agent 'claude' --profile ''"}]}]}}`
	os.WriteFile(filepath.Join(in.Root("home"), "settings.json"), []byte(settings), 0o600)
	if g := a.Guard(ctx, m, key); g == nil || !g.Ready {
		t.Fatalf("hook there: %+v", g)
	}
	m.Hopsesh = "0.5.0"
	if g := a.Guard(ctx, m, key); g == nil || g.Ready || !strings.Contains(g.Why[0], "needs "+MinBlockVersion) {
		t.Fatalf("too old: %+v", g)
	}
	m.Hopsesh = ""
	if g := a.Guard(ctx, m, key); g != nil {
		t.Fatalf("no hopsesh there: the planner decides alone: %+v", g)
	}
}
