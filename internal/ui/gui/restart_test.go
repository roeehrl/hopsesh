package gui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/core/pty"
	"github.com/roeehrl/hopsesh/internal/testkit"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Decision D2: a resume that would restart the agent's background service comes to the
// window marked, so it asks; the person's yes holds for that session's next resume only,
// and not for long.
func TestServiceRestartIsAsked(t *testing.T) {
	err := askRestart(fmt.Errorf("opening it: %w", agent.ErrRestartsService))
	if !strings.HasPrefix(err.Error(), restartAsk) || !errors.Is(err, agent.ErrRestartsService) {
		t.Fatalf("%v", err)
	}
	if other := errors.New("x"); askRestart(other) != other {
		t.Fatal("another error was marked")
	}
	a := &App{}
	if a.resumeOptions("m", "opencode/s", agent.ResumeOptions{}).RestartService {
		t.Fatal("agreed without asking")
	}
	a.AllowServiceRestart("m", "opencode/s")
	if a.resumeOptions("m", "opencode/other", agent.ResumeOptions{}).RestartService {
		t.Fatal("the yes reached another session")
	}
	if o := a.resumeOptions("m", "opencode/s", agent.ResumeOptions{Prompt: "p"}); !o.RestartService || o.Prompt != "p" {
		t.Fatalf("the yes was lost: %+v", o)
	}
	if a.resumeOptions("m", "opencode/s", agent.ResumeOptions{}).RestartService {
		t.Fatal("the yes held twice")
	}
	a.AllowServiceRestart("m", "opencode/s")
	a.restartOK["m\x00opencode/s"] = time.Now().Add(-restartWindow - time.Second)
	if a.resumeOptions("m", "opencode/s", agent.ResumeOptions{}).RestartService {
		t.Fatal("an old yes held")
	}
}

// The window's paths that open an OpenCode session or start one, with the running service
// of another version than every opencode here (the stand-in's): each refuses, marked so
// the window asks; the person's yes opens it once; "Run again" and "Open in my terminal"
// make the command again and never reuse that yes; reopening at start and the copied
// command say how to agree, without the window's marker.
func TestServiceRestartInTheWindow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in opencode is linked in, which needs a POSIX system here")
	}
	// The service's version must be one no OpenCode desktop app here bundles: the module
	// would pick that app's CLI, which is not the stand-in.
	const service = "2.2.5"
	for _, app := range []string{"/Applications/OpenCode.app"} {
		if b, err := os.ReadFile(filepath.Join(app, "Contents", "Resources", "opencode-cli.version")); err == nil && strings.TrimSpace(string(b)) == service {
			t.Skipf("%s bundles opencode %s", app, service)
		}
	}
	h, _ := filepath.EvalSymlinks(t.TempDir())
	testkit.UnsetAgentEnv(t)
	for k, v := range map[string]string{"HOME": h, "USERPROFILE": h, "HOPSESH_CONFIG_DIR": filepath.Join(h, "config"), "HOPSESH_STATE_DIR": filepath.Join(h, "state"),
		"CLAUDE_CONFIG_DIR": "", "CODEX_HOME": "", "HOPSESH_APP_RUNTIMES": "off", "FAKE_AGENT_LOG": filepath.Join(h, "agents.log"),
		"FAKE_OPENCODE_SERVICE": "running", "FAKE_OPENCODE_SERVICE_VERSION": service} {
		t.Setenv(k, v)
	}
	if err := testkit.DemoHome(h); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(h, "bin")
	os.MkdirAll(bin, 0o700)
	self, _ := os.Executable()
	if err := os.Symlink(self, filepath.Join(bin, "opencode")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+testPath())
	var lines []string
	SetTerminal(func(line string) error { lines = append(lines, line); return nil })
	defer func() { testTerminal = nil }()
	a := NewApp(all.Registry())
	defer a.Shutdown()
	a.Terms.AutoClose = func() bool { return false }
	scan, err := a.Scan()
	if err != nil {
		t.Fatal(err)
	}
	e := findEntry(t, scan, "opencode/"+testkit.DemoOpenCode[0])
	marked := func(what string, err error) {
		t.Helper()
		if err == nil || !strings.HasPrefix(err.Error(), restartAsk) || !errors.Is(err, agent.ErrRestartsService) {
			t.Fatalf("%s: %v", what, err)
		}
	}

	_, err = a.ResumeSession(e.Machine, e.Key, WhereHere)
	marked("in a tab", err)
	marked("in my terminal", a.ResumeEntry(e.Machine, e.Key, false))
	if len(lines) != 0 {
		t.Fatalf("opened unasked: %q", lines)
	}
	a.AllowServiceRestart(e.Machine, e.Key)
	if err := a.ResumeEntry(e.Machine, e.Key, false); err != nil || len(lines) != 1 {
		t.Fatalf("agreed, in my terminal: %v %q", err, lines)
	}
	a.AllowServiceRestart(e.Machine, e.Key)
	r, err := a.ResumeSession(e.Machine, e.Key, WhereHere)
	if err != nil || r.Where != WhereHere {
		t.Fatalf("agreed, in a tab: %+v %v", r, err)
	}
	if x, ok := a.Terms.tabOf(r.Tab); !ok || !strings.Contains(strings.Join(x.spec.Argv, " "), filepath.Join(bin, "opencode")) {
		t.Fatalf("the tab runs another opencode than the stand-in: %+v", x)
	}
	tb := waitTab(t, a, func(d TermTab) bool { return d.ID == r.Tab && d.State == pty.Exited })
	if !strings.HasPrefix(tb.Command, "opencode -s "+testkit.DemoOpenCode[0]) {
		t.Fatalf("the tab: %+v", tb)
	}
	// Run again: made again, without the yes, so refused; the ended tab stays.
	x, ok := a.Terms.tabOf(r.Tab)
	if !ok || x.respec == nil || x.external == nil {
		t.Fatal("the tab cannot make its command again")
	}
	if _, err := x.respec(); !errors.Is(err, agent.ErrRestartsService) || !strings.Contains(err.Error(), "hopsesh's window, which asks") {
		t.Fatalf("run again: %v", err)
	}
	a.Terms.rerun(r.Tab, x)
	if tabs := a.TerminalTabs(); len(tabs) != 1 || tabs[0].ID != r.Tab {
		t.Fatalf("ran again unasked: %+v", tabs)
	}
	if err := x.external(r.Tab); !errors.Is(err, agent.ErrRestartsService) || len(lines) != 1 {
		t.Fatalf("open in my terminal: %v %q", err, lines)
	}
	// The copied command and a reopened tab: refused, saying how to agree, unmarked.
	if _, err := a.ResumeCommand(e.Machine, e.Key); !errors.Is(err, agent.ErrRestartsService) || strings.HasPrefix(err.Error(), restartAsk) {
		t.Fatalf("copied: %v", err)
	}
	if err := a.reopenTab(ReopenTab{Kind: TabSession, Machine: e.Machine, Key: e.Key}); !errors.Is(err, agent.ErrRestartsService) || strings.HasPrefix(err.Error(), restartAsk) || !strings.Contains(err.Error(), "which asks") {
		t.Fatalf("reopened: %v", err)
	}

	// A new session in the default folder: asked the same way, and its tab too.
	_, err = a.NewSessionOn("", "opencode", "", "")
	marked("a new session", err)
	a.AllowNewSessionRestart("", "opencode", "")
	n, err := a.NewSessionOn("", "opencode", "", "")
	if err != nil || n.Where != WhereHere {
		t.Fatalf("agreed, a new session: %+v %v", n, err)
	}
	if y, ok := a.Terms.tabOf(n.Tab); !ok || !strings.Contains(strings.Join(y.spec.Argv, " "), filepath.Join(bin, "opencode")) {
		t.Fatalf("the new session runs another opencode than the stand-in: %+v", y)
	}
	if y, ok := a.Terms.tabOf(n.Tab); !ok || y.respec == nil {
		t.Fatal("the new session's tab cannot make its command again")
	} else if _, err := y.respec(); !errors.Is(err, agent.ErrRestartsService) {
		t.Fatalf("a new session, run again: %v", err)
	}
	// With the service of the program's version nothing is asked.
	t.Setenv("FAKE_OPENCODE_SERVICE_VERSION", "")
	if c, err := x.respec(); err != nil || !strings.HasSuffix(c.Argv[0], "opencode") {
		t.Fatalf("same version, run again: %+v %v", c, err)
	}
}
