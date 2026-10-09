package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/testkit"
	"github.com/roeehrl/hopsesh/internal/testkit/fakeagent"
	"github.com/roeehrl/hopsesh/internal/testkit/fakecloud"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// The test binary is the stand-in claude when it runs under that name.
func TestMain(m *testing.M) {
	switch strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") {
	case "claude":
		os.Exit(fakeagent.Claude())
	case "codex":
		os.Exit(fakeagent.Codex())
	}
	os.Exit(m.Run())
}

// cloudModel is the TUI on a demo home whose repository's GitHub remote is a local bare
// repository, with the stand-in claude on PATH, Claude Code cloud allowed and a pasted
// link to a cloud session that pushed its work.
func cloudModel(t *testing.T) (*model, fakecloud.Session) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in origin's hook needs a POSIX shell")
	}
	h := t.TempDir()
	h, _ = filepath.EvalSymlinks(h)
	bin := filepath.Join(h, "bin")
	for k, v := range testkit.Env(h) {
		t.Setenv(k, v)
	}
	gitConfig := filepath.Join(h, "gitconfig")
	for k, v := range map[string]string{"PATH": bin + ":" + os.Getenv("PATH"), "FAKE_CLOUD_DIR": filepath.Join(h, "cloud"), "FAKE_CLOUD_FAIL": "", "FAKE_CLAUDE_SAYS": "ok",
		"GIT_CONFIG_GLOBAL": gitConfig, "GIT_CONFIG_NOSYSTEM": "1", "GIT_AUTHOR_NAME": "Sam Doe", "GIT_AUTHOR_EMAIL": "sam@example.com",
		"GIT_COMMITTER_NAME": "Sam Doe", "GIT_COMMITTER_EMAIL": "sam@example.com", "CLAUDE_CODE_CHILD_SESSION": "1"} {
		t.Setenv(k, v)
	}
	if err := testkit.DemoHome(h); err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(self, filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	o, err := fakecloud.NewOrigin(h, "https://github.com/example/demo.git")
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Redirect(gitConfig); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(h, "git", "demo")
	if out, err := exec.Command("git", "-C", repo, "push", "-q", "origin", "main").CombinedOutput(); err != nil {
		t.Fatalf("push: %v %s", err, out)
	}
	s, err := fakecloud.Open(filepath.Join(h, "cloud")).Seed(fakecloud.Session{Cloud: fakecloud.ClaudeCloud, Title: "Add rate limiting", Repo: "github.com/example/demo",
		CloneURL: o.FileURL(), Branch: "main", Code: "branch", Messages: []fakecloud.Message{{Role: "user", Text: "[hopsesh] rate limiting"}, {Role: "assistant", Text: "On it."}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := fakecloud.Work(fakecloud.Proc{Vars: map[string]string{}}, s.ID, false); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.SetCloudAllowed("claude-cloud", true)
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfg, all.Registry(), config.StateDir(), nil)
	t.Cleanup(func() { _ = a.Catalog.Close() })
	if _, err := a.Paste(context.Background(), s.ID, repo); err != nil {
		t.Fatal(err)
	}
	return &model{deps: Deps{App: a, Describe: func(app.Entry) string { return "" }}, mode: modeLoading, started: time.Now(), opts: a.DefaultOptions()}, s
}

// The header lists the cloud after the machines (half a dot: a partial listing), the line
// below says how to reach the rest, and the cloud session is a row of its own.
func TestBrowseShowsCloud(t *testing.T) {
	m, s := cloudModel(t)
	m.width, m.height = 160, 40
	m.Update(m.Init()())
	defer m.inv.Close()
	v := ansi.Strip(m.View().Content)
	for _, want := range []string{"◐ claude-cloud 1", "Remote Control mirrors", "f find in Claude Code · p paste a link", "claude-cloud", "Session " + s.ID, "state unknown"} {
		if !strings.Contains(v, want) {
			t.Errorf("the browse view lacks %q:\n%s", want, v)
		}
	}
}

// A cloud session is a row; enter plans bringing it here, y makes the worktree, and enter
// hands the terminal to claude --teleport (without the variables an agent's own session
// passes on). Once it ends, the TUI opens again on what came back: complete, then a
// partial copy that says so and links the known problem.
func TestBringFromCloud(t *testing.T) {
	m, s := cloudModel(t)
	run := func(fail string) *Exit {
		t.Helper()
		tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(140, 40))
		scr := watch(t, tm, 140, 40)
		scr.waitFor(t, "state unknown")
		for i := 0; i < 8 && !(m.cursor < len(m.rows) && m.rows[m.cursor].item != nil && m.rows[m.cursor].item.Entry.Cloud != nil); i++ {
			tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
			time.Sleep(50 * time.Millisecond)
		}
		scr.waitFor(t, "enter: bring it here")
		tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
		scr.waitFor(t, "here from Claude Code cloud")
		scr.waitFor(t, "Clean worktree")
		tm.Type("y")
		scr.waitFor(t, "The worktree is ready")
		scr.waitFor(t, "send a message in it")
		tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
		fm := tm.FinalModel(t, teatest.WithFinalTimeout(10*time.Second)).(*model)
		if fm.inv != nil {
			fm.inv.Close()
		}
		e := fm.exit
		if e == nil || strings.Join(e.RunArgv, " ") != "claude --teleport "+s.ID || e.Adopt == "" || len(e.Unset) == 0 {
			t.Fatalf("exit: %+v", e)
		}
		// The CLI's part: run it here, then adopt.
		c := exec.Command(e.RunArgv[0], e.RunArgv[1:]...)
		c.Dir, c.Env = e.RunDir, append(host.Without(os.Environ(), e.Unset), "FAKE_CLOUD_FAIL="+fail)
		if out, err := c.CombinedOutput(); err != nil || !strings.Contains(string(out), "Teleported") {
			t.Fatalf("teleport: %v %s", err, out)
		}
		if _, err := m.deps.App.Adopt(context.Background(), e.Adopt, true); err != nil {
			t.Fatal(err)
		}
		return e
	}
	e := run("")
	again := &model{deps: Deps{App: m.deps.App, Describe: m.deps.Describe, Adopted: e.Adopt}, mode: modeLoading, started: time.Now(), opts: m.opts}
	tm := teatest.NewTestModel(t, again, teatest.WithInitialTermSize(140, 40))
	scr := watch(t, tm, 140, 40)
	scr.waitFor(t, "Brought “")
	scr.waitFor(t, "3 messages restored. Claude Code gives no count to check them against.")
	scr.waitFor(t, "The code is here in full")
	tm.Type("q")
	if fm := tm.FinalModel(t, teatest.WithFinalTimeout(10*time.Second)).(*model); fm.inv != nil {
		fm.inv.Close()
	}

	// A session hopsesh handed off is checked against the briefing it sent.
	if err := move.SaveHandoff(m.deps.App.StateDir, &move.Handoff{Journal: "handed-off", Cloud: "claude-cloud", Repo: "github.com/example/demo",
		Session: agent.SessionKey{Agent: "claude", Session: agent.SessionID(s.ID)}, Brief: s.Messages[0].Text}); err != nil {
		t.Fatal(err)
	}
	m = &model{deps: Deps{App: m.deps.App, Describe: m.deps.Describe}, mode: modeLoading, started: time.Now(), opts: m.opts}
	e = run("partial")
	again = &model{deps: Deps{App: m.deps.App, Describe: m.deps.Describe, Adopted: e.Adopt}, mode: modeLoading, started: time.Now(), opts: m.opts}
	tm = teatest.NewTestModel(t, again, teatest.WithInitialTermSize(140, 40))
	scr = watch(t, tm, 140, 40)
	scr.waitFor(t, ": partial")
	scr.waitFor(t, "Claude Code restored 1 message, but it holds only the briefing hopsesh sent")
	scr.waitFor(t, "k keep the partial copy")
	tm.Type("k")
	scr.waitFor(t, "The partial copy stays as it is.")
	tm.Type("q")
	if fm := tm.FinalModel(t, teatest.WithFinalTimeout(10*time.Second)).(*model); fm.inv != nil {
		fm.inv.Close()
	}
}
