package gui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/pty"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Where an entry point opens: what the window asked, else the setting (its default the
// release gate's constant); the app never asks first (the command line's "ask" is the
// default place).
func TestRoute(t *testing.T) {
	for _, tc := range []struct {
		asked, setting string
		want           string
	}{
		{"", "", config.AppResumeDefault},
		{"", "terminal", "terminal"},
		{"", "ask", config.AppResumeDefault},
		{"here", "terminal", "here"},
		{"terminal", "here", "terminal"},
		{"elsewhere", "here", "here"},
	} {
		if got := route(tc.asked, tc.setting); got != tc.want {
			t.Errorf("route(%q, %q) = %q, want %q", tc.asked, tc.setting, got, tc.want)
		}
	}
	if config.AppResumeDefault != config.ResumeHere {
		t.Log("the release gate ships the user's terminal app as the default")
	}
}

// A command for people: the program's name, a long briefing as its length, quotes where
// an argument has spaces, and no control characters.
func TestDisplayCommand(t *testing.T) {
	got := displayCommand([]string{"/usr/local/bin/claude", "--cloud", strings.Repeat("brief ", 40), "a b", "x\x1b[31m"})
	if got != `claude --cloud ‹240 characters› "a b" x [31m` {
		t.Fatal(got)
	}
	if tabTitle("Fix the parser quoting bug in the long title", "claude") != "Fix the parser quoting bug… · claude" {
		t.Fatal(tabTitle("Fix the parser quoting bug in the long title", "claude"))
	}
}

// Notifications: hopsesh's own words with the title cleaned, at most one per pause, the
// rest told as one; a tab that stopped waiting meanwhile is left out.
func TestNotifier(t *testing.T) {
	var (
		mu   sync.Mutex
		sent []string
	)
	n := newNotifier(func(id, title, body string) {
		mu.Lock()
		sent = append(sent, id+"|"+title+"|"+body)
		mu.Unlock()
	}, 150*time.Millisecond)
	waitingIDs := map[string]bool{"a": true, "b": true, "c": false}
	n.still = func(id string) bool { return waitingIDs[id] }
	n.waiting("a", waitingText(TabMeta{Agent: "Claude Code"}, "Fix\x1b]0;evil\x07 the \u202eparser"))
	n.waiting("b", "two")
	n.waiting("c", "three (stopped waiting)")
	time.Sleep(400 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 2 {
		t.Fatalf("sent %q", sent)
	}
	if sent[0] != "a|hopsesh|Claude Code is waiting for you in “Fix ]0;evil the parser”" {
		t.Errorf("first: %q", sent[0])
	}
	if sent[1] != "b|hopsesh|two" { // c stopped waiting, so b alone
		t.Errorf("coalesced: %q", sent[1])
	}
	if got := waitingText(TabMeta{}, strings.Repeat("x", 60)); got != "A program is waiting for you in “"+strings.Repeat("x", 39)+"…”" {
		t.Errorf("long title: %q", got)
	}
	if got := endedText(TabMeta{Kind: TabStep, CloudTitle: "Claude Code cloud"}, pty.Info{Code: 0}); got != "The hand-off to Claude Code cloud finished" {
		t.Error(got)
	}
	if got := endedText(TabMeta{Kind: TabSignIn}, pty.Info{Title: "Sign in · Codex cloud", Code: 1}); got != "Sign in · Codex cloud ended with code 1" {
		t.Error(got)
	}
}

// A tab waits for the user on a bell or a notification, a step when it asks or goes
// quiet, and a bring-back until the copy is saved; a session going quiet is not waiting.
func TestAttention(t *testing.T) {
	for _, tc := range []struct {
		i    pty.Info
		m    TabMeta
		want bool
	}{
		{pty.Info{State: pty.Running}, TabMeta{Kind: TabSession}, false},
		{pty.Info{State: pty.Waiting, Reason: pty.ReasonIdle}, TabMeta{Kind: TabSession}, false},
		{pty.Info{State: pty.Waiting, Reason: pty.ReasonBell}, TabMeta{Kind: TabSession}, true},
		{pty.Info{State: pty.Waiting, Reason: pty.ReasonNotification}, TabMeta{Kind: TabShell}, true},
		{pty.Info{State: pty.Running}, TabMeta{Kind: TabStep}, false},
		{pty.Info{State: pty.Waiting, Reason: pty.ReasonIdle}, TabMeta{Kind: TabStep}, true},
		{pty.Info{State: pty.Running}, TabMeta{Kind: TabBring}, true},
		{pty.Info{State: pty.Running}, TabMeta{Kind: TabBring, Saved: "Fix it"}, false},
		{pty.Info{State: pty.Exited}, TabMeta{Kind: TabStep}, false},
	} {
		if got := attention(tc.i, tc.m); got != tc.want {
			t.Errorf("%+v %+v: %v", tc.i, tc.m, got)
		}
	}
}

// fakeClaude puts the stand-in claude (this test binary) first on PATH.
func fakeClaude(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in claude is linked in, which needs a POSIX system here")
	}
	bin := filepath.Join(t.TempDir(), "bin")
	os.MkdirAll(bin, 0o700)
	self, _ := os.Executable()
	if err := os.Symlink(self, filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+testPath())
	return bin
}

// waitTab waits for a tab that matches.
func waitTab(t *testing.T, a *App, ok func(TermTab) bool) TermTab {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		for _, d := range a.TerminalTabs() {
			if ok(d) {
				return d
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no such tab in %+v", a.TerminalTabs())
	return TermTab{}
}

// Resuming a session here opens it in a tab, with what it runs, the module's terminal
// variables and the session it is; asking again shows that tab; asking for the user's
// terminal app runs the launch there; with Ask nothing opens.
func TestResumeSessionRouting(t *testing.T) {
	home(t)
	fakeClaude(t)
	a := NewApp(all.Registry())
	defer a.Shutdown()
	a.Terms.AutoClose = func() bool { return false } // the test reads the ended tab
	var lines []string
	SetTerminal(func(line string) error { lines = append(lines, line); return nil })
	defer func() { testTerminal = nil }()
	SetStepProgram("/usr/bin/true")
	scan, err := a.Scan()
	if err != nil {
		t.Fatal(err)
	}
	e := findEntry(t, scan, "claude/"+sid)

	r, err := a.ResumeSession(e.Machine, e.Key, "")
	if err != nil || r.Where != WhereHere || r.Tab == "" {
		t.Fatalf("default: %+v %v", r, err)
	}
	tb := waitTab(t, a, func(d TermTab) bool { return d.ID == r.Tab })
	if tb.Kind != TabSession || tb.Key != e.Key || tb.Machine != e.Machine || !tb.External || !tb.Rerun ||
		!strings.HasPrefix(tb.Command, "claude --resume") || tb.Title != "Find the codeword · claude" {
		t.Fatalf("the tab: %+v", tb)
	}
	spec, _ := a.Terms.tabOf(r.Tab)
	if !slices.Contains(spec.spec.Env.Set, "CLAUDE_CODE_FORCE_SYNC_OUTPUT=1") || !filepath.IsAbs(spec.spec.Argv[0]) {
		t.Fatalf("the spec: %+v", spec.spec)
	}

	// The stand-in claude ends at once; while a tab runs, asking again shows it. A tab
	// that ended does not count.
	waitTab(t, a, func(d TermTab) bool { return d.ID == r.Tab && d.State == pty.Exited })
	if a.Terms.liveSessionTab(e.Machine, e.Key) != "" {
		t.Fatal("an ended tab counts as live")
	}

	r2, err := a.ResumeSession(e.Machine, e.Key, "terminal")
	if err != nil || r2.Where != WhereTerminal || len(lines) != 1 || !strings.Contains(lines[0], "terminal-open") {
		t.Fatalf("in my terminal: %+v %v %q", r2, err, lines)
	}
	a.mu.Lock()
	a.core.Cfg.Terminal.Resume = config.ResumeAsk
	a.mu.Unlock()
	if r3, err := a.ResumeSession(e.Machine, e.Key, ""); err != nil || r3.Where != config.AppResumeDefault {
		t.Fatalf("the command line's ask is the default place in the app: %+v %v", r3, err)
	}
}

// A running session's tab is shown, never a second copy: Resume, Show and the external
// path all find it.
func TestOneLiveTabPerSession(t *testing.T) {
	home(t)
	fakeClaude(t)
	a := NewApp(all.Registry())
	defer a.Shutdown()
	scan, err := a.Scan()
	if err != nil {
		t.Fatal(err)
	}
	e := findEntry(t, scan, "claude/"+sid)
	dir, _ := os.UserHomeDir()
	info, err := a.Terms.Open(pty.Spec{Argv: []string{"/bin/sh", "-c", "sleep 30"}, Dir: dir},
		TabSetup{Meta: TabMeta{Kind: TabSession, Machine: e.Machine, Key: e.Key}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := a.ResumeSession(e.Machine, e.Key, "terminal")
	if err != nil || r.Where != WhereShown || r.Tab != info.ID {
		t.Fatalf("resume: %+v %v", r, err)
	}
	if name, err := a.ShowEntry(e.Machine, e.Key); err != nil || name != TerminalTitle {
		t.Fatalf("show: %q %v", name, err)
	}
	if tab, err := a.EntryTab(e.Machine, e.Key); err != nil || tab == nil || tab.Terminal != TerminalTitle {
		t.Fatalf("entry tab: %+v %v", tab, err)
	}
	if err := a.ResumeEntry(e.Machine, e.Key, false); err == nil || !strings.Contains(err.Error(), "hopsesh Terminal window") {
		t.Fatalf("a second copy: %v", err)
	}
}

// The window's requests are a closed set about tabs it names: unknown ones, oversized
// ones and ones about tabs that are not there do nothing; Open in my terminal only where
// the tab offers it.
func TestWindowRequests(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	terms := NewTerminals("test")
	defer terms.CloseAll()
	dir, _ := os.UserHomeDir()
	moved := make(chan string, 1)
	info, err := terms.Open(pty.Spec{Argv: []string{shellProg(), "-c", "sleep 30"}, Dir: dir}, TabSetup{
		Meta:     TabMeta{Kind: TabSession, External: true},
		External: func(id string) error { moved <- id; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := terms.Open(pty.Spec{Argv: []string{shellProg(), "-c", "sleep 30"}, Dir: dir}, TabSetup{Meta: TabMeta{Kind: TabShell}})
	if err != nil {
		t.Fatal(err)
	}
	var shells []string
	terms.Shell = func(d string) error { shells = append(shells, d); return nil }
	terms.request([]byte(`{"op":"external","id":"` + plain.ID + `"}`))
	terms.request([]byte(`{"op":"external","id":"nope"}`))
	terms.request([]byte(`{"op":"rm -rf","id":"` + info.ID + `"}`))
	terms.request([]byte(`{"op":"external","id":"` + info.ID + `","pad":"` + strings.Repeat("x", 600) + `"}`))
	select {
	case id := <-moved:
		t.Fatalf("moved %s", id)
	case <-time.After(200 * time.Millisecond):
	}
	terms.request([]byte(`{"op":"external","id":"` + info.ID + `"}`))
	select {
	case id := <-moved:
		if id != info.ID {
			t.Fatal(id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("not moved")
	}
	terms.request([]byte(`{"op":"active","id":"` + plain.ID + `"}`))
	if terms.active != plain.ID {
		t.Fatal("active")
	}
	terms.request([]byte(`{"op":"rerun","id":"` + info.ID + `"}`)) // still running: nothing
	if len(terms.Tabs()) != 2 {
		t.Fatalf("tabs %+v", terms.Tabs())
	}
	terms.request([]byte(`{"op":"font","size":99}`)) // out of range: nothing (no SavePrefs set either)
}

func shellProg() string { return "/bin/sh" }

// A hand-off's step runs in a tab of the hopsesh Terminal window by default: the tab says
// what it is and that hopsesh reads it only for the link, and once it ends the module's
// reader decides (here the stand-in refuses: the folder is no checkout).
func TestStepInATab(t *testing.T) {
	home(t)
	fakeClaude(t)
	t.Setenv("FAKE_CLOUD_DIR", t.TempDir())
	a := NewApp(all.Registry())
	defer a.Shutdown()
	core := a.snapshot()
	folder := filepath.Join(core.StateDir, "handoff", "github.com", "example", "demo")
	os.MkdirAll(folder, 0o700)
	step := move.TermStep{Agent: "claude", Cloud: "claude-cloud", CloudTitle: "Claude Code cloud", Title: "Fix the parser", Folder: folder,
		Run: agent.Command{Argv: []string{"claude", "--cloud", "[hopsesh] the briefing"}, Dir: folder, Unset: []string{"ANTHROPIC_API_KEY"}}}
	SetTerminal(func(string) error { t.Fatal("the user's terminal app was opened"); return nil })
	defer func() { testTerminal = nil }()
	_, err := a.runStep(context.Background(), step)
	if err == nil || errors.Is(err, agent.ErrNoSession) || !strings.Contains(err.Error(), "claude refused") {
		t.Fatalf("a refusal: %v", err)
	}
	tabs := a.TerminalTabs()
	if len(tabs) != 1 {
		t.Fatalf("tabs %+v", tabs)
	}
	d := tabs[0]
	if d.Kind != TabStep || !d.Capture || d.CloudTitle != "Claude Code cloud" || d.Agent != "Claude Code" || d.Title != "Hand off · Fix the parser" ||
		!strings.Contains(d.Command, "claude --cloud") || d.State != pty.Exited {
		t.Fatalf("the step's tab: %+v", d)
	}
	tb, _ := a.Terms.tabOf(d.ID)
	if slices.Contains(tb.spec.Env.Set, "ANTHROPIC_API_KEY") || !slices.Contains(tb.spec.Env.Unset, "ANTHROPIC_API_KEY") {
		t.Fatalf("the step's environment: %+v", tb.spec.Env)
	}
}

// Quitting with programs in tabs waits for the window's confirmation; closing the window
// hides it while the setting keeps tabs, and asks otherwise.
func TestQuitAndClose(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	home(t)
	a := NewApp(all.Registry())
	defer a.Shutdown()
	var (
		evMu   sync.Mutex
		events []string
	)
	a.Emitter = func(name string, _ any) {
		evMu.Lock()
		events = append(events, name)
		evMu.Unlock()
	}
	saw := func(name string) bool {
		evMu.Lock()
		defer evMu.Unlock()
		return slices.Contains(events, name)
	}
	if !a.ShouldQuit() {
		t.Fatal("no tabs: quits")
	}
	if c, _ := a.MainClosing(); c {
		t.Fatal("no tabs: closes")
	}
	dir, _ := os.UserHomeDir()
	if _, err := a.Terms.Open(pty.Spec{Argv: []string{shellProg(), "-c", "sleep 30"}, Dir: dir}, TabSetup{Meta: TabMeta{Kind: TabShell}}); err != nil {
		t.Fatal(err)
	}
	if a.ShouldQuit() || !saw(QuitEvent) {
		t.Fatal("a running tab: quitting must ask")
	}
	if c, hide := a.MainClosing(); !c || !hide {
		t.Fatal("keep tabs: hide")
	}
	// An explicit desktop Quit choice wins over the older terminal keep-tabs preference.
	a.mu.Lock()
	a.core.Cfg.Desktop.Close = "quit"
	a.mu.Unlock()
	evMu.Lock()
	events = nil
	evMu.Unlock()
	if c, hide := a.MainClosing(); !c || hide || !saw(QuitEvent) {
		t.Fatal("explicit desktop quit must confirm running tabs")
	}
	off := false
	a.mu.Lock()
	a.core.Cfg.Desktop.Close = ""
	a.core.Cfg.Terminal.KeepTabs = &off
	a.mu.Unlock()
	evMu.Lock()
	events = nil
	evMu.Unlock()
	if c, hide := a.MainClosing(); !c || hide || !saw(QuitEvent) {
		t.Fatal("no keeping tabs: ask")
	}
	a.QuitAndEnd()
	if !a.ShouldQuit() || a.TerminalRunning() != 0 {
		t.Fatal("confirmed: quits, and the tabs ended")
	}
}

// Settings → Terminal round-trips, keeps defaults unwritten, and refuses what it cannot use.
func TestTerminalSettings(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	defer a.Shutdown()
	s := a.TerminalSettings()
	if s.Where != config.AppResumeDefault || s.FontSize != 13 || s.Scrollback != 5000 || !s.KeepTabs || !s.Notify {
		t.Fatalf("defaults: %+v", s)
	}
	in := TerminalSettingsInput{Where: "terminal", Font: "JetBrains Mono", FontSize: 15, Scrollback: 10000, KeepTabs: false, Notify: true, ScreenReader: "on"}
	if err := a.SetTerminalSettings(in); err != nil {
		t.Fatal(err)
	}
	s = a.TerminalSettings()
	if s.Where != "terminal" || s.Font != "JetBrains Mono" || s.FontSize != 15 || s.Scrollback != 10000 || s.KeepTabs || !s.Notify || s.ScreenReader != "on" {
		t.Fatalf("saved: %+v", s)
	}
	if p := a.termPrefs(); p.FontSize != 15 || p.Scrollback != 10000 || !p.ScreenReader || p.Font != "JetBrains Mono" {
		t.Fatalf("prefs: %+v", p)
	}
	b, _ := os.ReadFile(config.Path())
	if strings.Contains(string(b), "notify") {
		t.Fatalf("a default was written:\n%s", b)
	}
	in.FontSize = 40
	if err := a.SetTerminalSettings(in); err == nil {
		t.Fatal("a font size of 40 was taken")
	}
	a.saveTermPrefs(9, nil)
	if a.TerminalSettings().FontSize != 9 {
		t.Fatal("the window's font size")
	}
}
