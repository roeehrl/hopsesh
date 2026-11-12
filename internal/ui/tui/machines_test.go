package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/roeehrl/hopsesh/internal/app"
)

// The header mark: ⌒ when hopsesh is there (its color says whether it receives), ↑ when
// it is older than the newest; nothing for a machine without hopsesh.
func TestHopMark(t *testing.T) {
	cases := []struct {
		installed, receiving, behind bool
		want                         string
	}{
		{true, true, false, "⌒"},
		{true, false, false, "⌒"},
		{false, false, false, ""},
		{true, false, true, "⌒↑"},
	}
	for _, c := range cases {
		if got := ansi.Strip(hopMark(c.installed, c.receiving, c.behind)); got != c.want {
			t.Errorf("%+v: %q", c, got)
		}
	}
	if hopMark(true, true, false) == hopMark(true, false, false) {
		t.Fatal("receiving and not receiving are styled apart")
	}
}

// The browse header shows each machine's mark from what the scan found.
func TestHeaderMarks(t *testing.T) {
	m := newModel(t)
	m.inv = &app.Inventory{Machines: []*app.Machine{
		{Name: "studio", Status: app.StatusOK, Hopsesh: "0.6.1", Presence: app.PresenceInstalled},
		{Name: "macbook", Status: app.StatusOK, Hopsesh: "0.5.0", Presence: app.PresenceInstalled},
		{Name: "nas", Status: app.StatusOK, Presence: app.PresenceSSH},
	}}
	m.mode, m.width, m.height = modeBrowse, 160, 40
	var b strings.Builder
	m.viewBrowse(&b)
	head := ansi.Strip(strings.SplitN(b.String(), "\n", 2)[0])
	for _, want := range []string{"studio ⌒ 0", "macbook ⌒↑ 0", "nas 0"} {
		if !strings.Contains(head, want) {
			t.Errorf("header %q lacks %q", head, want)
		}
	}
	if strings.Contains(head, "studio ⌒↑") || strings.Contains(head, "nas ⌒") {
		t.Errorf("header %q: only the older machine has ↑, only machines with hopsesh have ⌒", head)
	}
	if !strings.Contains(ansi.Strip(b.String()), "m machines") {
		t.Error("the help names the machines key")
	}
}

// The machines panel lists every machine and spells out the selected one's checks, each
// as a symbol and words, with the reason for what isn't right.
func TestMachinesPanel(t *testing.T) {
	ok := func(t string) app.Check { return app.Check{State: app.CheckOK, Text: t} }
	v := machinesView{cursor: 1, list: []app.MachineStatus{
		{Machine: "studio", Local: true, Version: "0.6.1", Reach: ok("this machine"), Hopsesh: ok("0.6.1"), Receiving: ok("on, listening"), Login: ok("in your login session"), CLI: ok("on PATH")},
		{Machine: "sam-macbook", Version: "0.5.0", Newest: "0.6.1", NewestOn: "studio", Todo: 2, Reach: ok("reached over SSH"),
			Hopsesh:   app.Check{State: app.CheckWarn, Text: "0.5.0", Reason: "0.6.1 is the newest, on studio", Fix: app.FixUpdate},
			Receiving: app.Check{State: app.CheckOff, Text: "not listening", Reason: "until hopsesh runs in a login session there"},
			Login:     app.Check{State: app.CheckOff, Text: "not running", Reason: "nobody is logged in there"},
			CLI:       ok("on PATH · ~/.local/bin/hopsesh"),
			Agents: []app.AgentSetup{
				{Agent: "claude", Name: "Claude Code", Hook: ok("installed"), Skill: app.Check{State: app.CheckOff, Text: "not installed", Fix: app.FixSkill}},
				{Agent: "claude", Name: "Claude Code", ProfileLabel: "work", Hook: app.Check{State: app.CheckError, Text: "broken", Reason: "it runs ~/old/hopsesh, which isn't there", Fix: app.FixHooks}, Skill: ok("installed")},
			},
			DetailsAt: time.Now().Add(-3 * time.Minute)},
		{Machine: "nas", Reach: ok("reached over SSH"), Hopsesh: app.Check{State: app.CheckOff, Text: "not installed", Fix: app.FixSetUp}},
	}}
	out := ansi.Strip(renderMachines(v, 120))
	for _, want := range []string{
		"studio (here)", "✓ 0.6.1 · all set",
		"▸ sam-macbook", "↑ 0.5.0 · 2 to do",
		"○ not installed",
		"↑ hopsesh      0.5.0 (newest 0.6.1, studio)",
		"○ receiving    not listening — until hopsesh runs in a login session there",
		"✓ command      on PATH",
		"○ skill        not installed  Claude Code",
		"✕ hook         broken  Claude Code · work — it runs ~/old/hopsesh, which isn't there",
		"checked 3m ago",
		"r check again · esc back",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("panel lacks %q:\n%s", want, out)
		}
	}
	v.loading = true
	if out := ansi.Strip(renderMachines(v, 120)); !strings.Contains(out, "checking…") || !strings.Contains(out, "sam-macbook") {
		t.Errorf("a new read keeps the last result shown:\n%s", out)
	}
	if out := ansi.Strip(renderMachines(machinesView{loading: true}, 80)); !strings.Contains(out, "reading every machine") {
		t.Errorf("first read:\n%s", out)
	}
}

// m opens the panel, r reads again, esc goes back; a stale answer is dropped.
func TestMachinesKeys(t *testing.T) {
	m := newModel(t)
	m.mode = modeMachines
	m.machines = machinesView{list: []app.MachineStatus{{Machine: "a"}, {Machine: "b"}}, gen: 3}
	m.machineKeys("down")
	m.machineKeys("down")
	if m.machines.cursor != 1 {
		t.Fatalf("cursor %d", m.machines.cursor)
	}
	m.Update(machinesDone{list: []app.MachineStatus{{Machine: "x"}}, gen: 2})
	if m.machines.list[0].Machine != "a" {
		t.Fatal("an older read's answer must not replace the newer one")
	}
	m.Update(machinesDone{list: []app.MachineStatus{{Machine: "x"}}, gen: 3})
	if len(m.machines.list) != 1 || m.machines.cursor != 0 {
		t.Fatalf("%+v", m.machines)
	}
	m.machineKeys("esc")
	if m.mode != modeBrowse {
		t.Fatal("esc goes back")
	}
}
