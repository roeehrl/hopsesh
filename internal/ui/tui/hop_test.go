package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/move"
)

// c on a cloud row offers the other clouds; the plan pane shows both legs; applying leaves
// the teleport for this terminal (enter hands it over), and once it ran and the hop went
// on, the UI opens on where it stands.
func TestHopFromCloudRow(t *testing.T) {
	m, s := cloudModel(t)
	self, _ := os.Executable()
	bin := filepath.SplitList(os.Getenv("PATH"))[0]
	if err := os.Symlink(self, filepath.Join(bin, "codex")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CODEX_ENVS", "env_api=acme-api")
	a := m.deps.App
	a.Cfg.SetCloudAllowed("codex-cloud", true)
	a.Cfg.SetCloudEnvironment("codex-cloud", "github.com/example/demo", "env_api")
	m.width, m.height = 160, 50
	m.Update(m.Init()())
	defer m.inv.Close()
	row := -1
	for i, r := range m.rows {
		if r.item != nil && r.item.Entry.Cloud != nil && string(r.item.Entry.Session.Key.Session) == s.ID {
			row = i
		}
	}
	if row < 0 {
		t.Fatal("no cloud row")
	}
	m.cursor = row
	m.key("c")
	if !m.ho.picking || !m.ho.hop {
		t.Fatalf("picker: %+v", m.ho)
	}
	v := ansi.Strip(m.View().Content)
	if !strings.Contains(v, "codex-cloud") || !strings.Contains(v, "Comes here from Claude Code cloud first") || strings.Contains(v, "claude-cloud   Comes") {
		t.Fatalf("picker view:\n%s", v)
	}
	for i, tg := range m.ho.targets {
		if tg.Cloud == "codex-cloud" {
			m.ho.at = i
		}
	}
	_, cmd := m.key("enter")
	m.Update(cmd())
	if m.mode != modePlan || m.plan.Kind != move.KindHop || len(m.plan.Blockers) > 0 {
		t.Fatalf("plan: %v %+v", m.mode, m.plan.Blockers)
	}
	v = ansi.Strip(m.View().Content)
	for _, want := range []string{"Hand on to Codex cloud", "1. Bring here", "2. Hand off", "claude --teleport " + s.ID, "send a message in it", "Environment  env_api"} {
		if !strings.Contains(v, want) {
			t.Errorf("the plan pane lacks %q:\n%s", want, v)
		}
	}
	_, cmd = m.key("y")
	m.Update(cmd())
	if m.mode != modeDone || m.result.Hop == nil || m.result.Hop.State != move.HopWaiting {
		t.Fatalf("applied: %v %+v", m.mode, m.result)
	}
	if v = ansi.Strip(m.View().Content); !strings.Contains(v, "It runs in this terminal") || !strings.Contains(v, "hopsesh clouds continue") {
		t.Fatalf("waiting view:\n%s", v)
	}
	m.key("enter")
	e := m.exit
	if e == nil || e.Hop != m.result.Journal || e.Adopt == "" || strings.Join(e.RunArgv, " ") != "claude --teleport "+s.ID {
		t.Fatalf("exit: %+v", e)
	}
	// The command line's part: run it here, adopt, take the hop on.
	c := exec.Command(e.RunArgv[0], e.RunArgv[1:]...)
	c.Dir, c.Env = e.RunDir, host.Without(os.Environ(), e.Unset)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("teleport: %v %s", err, out)
	}
	if _, err := a.Adopt(context.Background(), e.Adopt, true); err != nil {
		t.Fatal(err)
	}
	if res, err := a.ContinueHop(context.Background(), e.Hop, nil); err != nil || res.Hop.State != move.HopDone {
		t.Fatalf("continue: %+v %v", res, err)
	}
	again := &model{deps: Deps{App: a, Describe: func(app.Entry) string { return "" }, Hop: e.Hop}, mode: modeLoading, started: time.Now(), opts: m.opts, width: 160, height: 50}
	again.Update(again.Init()())
	defer again.inv.Close()
	v = ansi.Strip(again.View().Content)
	for _, want := range []string{"Handed on to Codex cloud", "came here from Claude Code cloud first", "Task task_e_", "u undo both legs"} {
		if !strings.Contains(v, want) {
			t.Errorf("the hop's view lacks %q:\n%s", want, v)
		}
	}
	_, cmd = again.key("u")
	again.Update(cmd())
	js, _ := a.Journals()
	for _, j := range js {
		if j.ID == e.Hop && !j.Undone {
			t.Fatal("u undoes the hop")
		}
	}
}
