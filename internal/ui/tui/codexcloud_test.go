package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/testkit/fakecloud"
)

// Codex cloud in the terminal: Hand off to ▸ says what hopsesh cannot reach there, the plan
// pane waits for an environment (e picks one of the ones recent tasks used), the done view
// names the task; when it is done, enter on its row brings it back as a commit and a Codex
// thread, shown at once.
func TestHandoffToCodexCloudInTheTerminal(t *testing.T) {
	m, _ := cloudModel(t)
	self, _ := os.Executable()
	if err := os.Symlink(self, filepath.Join(os.Getenv("HOME"), "bin", "codex")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CODEX_ENVS", "env_api=acme-api")
	store := fakecloud.Open(os.Getenv("FAKE_CLOUD_DIR"))
	if _, err := store.Seed(fakecloud.Session{Cloud: fakecloud.CodexCloud, Title: "An earlier task", Env: "env_api", EnvLabel: "acme-api", State: fakecloud.StateDone}); err != nil {
		t.Fatal(err)
	}
	a := m.deps.App
	a.Cfg.SetCloudAllowed("codex-cloud", true)
	m.width, m.height = 200, 70
	m.Update(m.Init()())
	defer m.inv.Close()
	for i, r := range m.rows {
		if r.item != nil && r.item.Entry.Session.Title == "Find the codeword" {
			m.cursor = i
		}
	}
	m.key("c")
	for m.ho.at < len(m.ho.targets) && m.ho.targets[m.ho.at].Cloud != "codex-cloud" {
		m.key("down")
	}
	v := ansi.Strip(m.View().Content)
	if !strings.Contains(v, "Gets a briefing and the code on a branch · Codex cloud (legacy) tasks only: the new Codex Cloud has no command line yet") {
		t.Errorf("the picker:\n%s", v)
	}
	_, cmd := m.key("enter")
	m.Update(cmd())
	if m.mode != modePlan || m.plan.Handoff == nil || m.plan.Handoff.Cloud != "codex-cloud" {
		t.Fatalf("plan: %v %v", m.mode, m.err)
	}
	v = ansi.Strip(m.View().Content)
	for _, want := range []string{"Hand off to Codex cloud", "Environment  none picked  [e] pick: acme-api", "✗ Pick a Codex cloud environment for github.com/example/demo",
		"Cloud tasks use your plan's allowance.", "e environment"} {
		if !strings.Contains(v, want) {
			t.Errorf("the plan pane lacks %q:\n%s", want, v)
		}
	}
	_, cmd = m.key("e")
	m.Update(cmd())
	if v = ansi.Strip(m.View().Content); !strings.Contains(v, "Environment  acme-api") || len(m.plan.Blockers) > 0 {
		t.Fatalf("e picks the environment: %v\n%s", m.plan.Blockers, v)
	}
	_, cmd = m.key("enter")
	m.Update(cmd())
	if m.mode != modeDone || m.result == nil || m.result.Handoff == nil || m.result.Handoff.Failed != "" {
		t.Fatalf("done: %v %v %+v", m.mode, m.err, m.result)
	}
	r := m.result.Handoff
	v = ansi.Strip(m.View().Content)
	for _, want := range []string{"✓ Handed off to Codex cloud", "Task " + shortID(r.Session) + " is running", "env     acme-api", "id      " + r.Session,
		"the task stays in your Codex cloud list"} {
		if !strings.Contains(v, want) {
			t.Errorf("the done view lacks %q:\n%s", want, v)
		}
	}
	if a.Cfg.CloudSettings("codex-cloud").Environments["github.com/example/demo"] != "env_api" {
		t.Error("the environment is remembered")
	}

	if err := fakecloud.Work(fakecloud.Proc{Vars: map[string]string{}}, r.Session, false); err != nil {
		t.Fatal(err)
	}
	_, cmd = m.key("enter")
	m.Update(cmd())
	for i, row := range m.rows {
		if row.item != nil && string(row.item.Entry.Session.Key.Session) == r.Session {
			m.cursor = i
		}
	}
	_, cmd = m.key("enter")
	if cmd == nil {
		t.Fatal("enter plans bringing the task here")
	}
	m.Update(cmd())
	if m.mode != modePlan || m.plan.Fetch == nil || len(m.plan.Blockers) > 0 {
		t.Fatalf("bring-back plan: %v %v %+v", m.mode, m.err, m.plan)
	}
	v = ansi.Strip(m.View().Content)
	for _, want := range []string{"Only the task title, summary and code come back. The steps stay in the cloud.", "code  the task's patch (+1 −0 · 1 file), committed on hopsesh/from/codex-cloud/" + r.Session,
		"writes  a new Codex session (2 messages) in the worktree"} {
		if !strings.Contains(v, want) {
			t.Errorf("the bring-back plan lacks %q:\n%s", want, v)
		}
	}
	_, cmd = m.key("y")
	m.Update(cmd())
	if m.mode != modeBrought || m.brought == nil || !m.brought.Written || m.brought.Outcome != move.FetchComplete {
		t.Fatalf("brought: %v %v %+v", m.mode, m.err, m.brought)
	}
	v = ansi.Strip(m.View().Content)
	for _, want := range []string{"Brought “", "from Codex cloud", "is here in Codex", "The code is here:", "The cloud task is untouched.", "r resume it now", "codex resume"} {
		if !strings.Contains(v, want) {
			t.Errorf("the brought view lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "i continue in") {
		t.Error("a written copy is already in the agent it went to")
	}
	_, cmd = m.key("u")
	if msg := cmd(); msg.(undoDone).err != nil {
		t.Fatalf("undo: %v", msg.(undoDone).err)
	}
}
