package pty_test

import (
	"context"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/pty"
	"github.com/roeehrl/hopsesh/internal/core/pty/ptytest"
)

const wait = 20 * time.Second

// tabs is a Manager for a test, closed at its end.
func tabs(t *testing.T, o pty.Options) *pty.Manager {
	t.Helper()
	if o.Version == "" {
		o.Version = "0.0.0-test"
	}
	// The tabs' folders go after the tabs: on Windows a running program's folder cannot be
	// removed (cleanups run last first).
	_ = t.TempDir()
	if o.ConptyDir == "" {
		o.ConptyDir = os.Getenv("HOPSESH_CONPTY_DIR") // CI's Windows test job fetches the pair
	}
	m := pty.NewManager(o)
	t.Cleanup(m.CloseAll)
	return m
}

// role is a Spec running this test binary as the child role (see TestMain).
func role(t *testing.T, name string, env ...string) pty.Spec {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return pty.Spec{Argv: []string{self}, Dir: t.TempDir(), Env: pty.Env{Set: append([]string{"PTY_TEST_ROLE=" + name}, env...)}}
}

func start(t *testing.T, m *pty.Manager, spec pty.Spec) *pty.Session {
	t.Helper()
	s, err := m.Start(spec)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// find serves m's tabs to any window (the app checks the window first).
func find(m *pty.Manager) func(string) (*pty.Session, error) {
	return func(id string) (*pty.Session, error) {
		if s, ok := m.Get(id); ok {
			return s, nil
		}
		return nil, os.ErrNotExist
	}
}

func open(t *testing.T, m *pty.Manager, s *pty.Session, o ptytest.Options) *ptytest.Window {
	t.Helper()
	w := ptytest.Open(find(m), nil, s.ID(), o)
	t.Cleanup(w.Detach)
	return w
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// A tab runs its program in a terminal of the window's size; the window's emulator
// answers the program's device-attributes query (DA1) through the stream, the user's keys
// reach it raw, a resize reaches it, and its exit code comes back as the tab's state.
func TestTabRoundTrip(t *testing.T) {
	m := tabs(t, pty.Options{})
	// The child can print its initial size before the window attaches. Start it at
	// the intended size, then independently verify the later resize below.
	spec := role(t, "da1")
	spec.Cols, spec.Rows = 90, 25
	s := start(t, m, spec)
	w := open(t, m, s, ptytest.Options{Cols: 90, Rows: 25})
	must(t, w.WaitFor("ready size=", wait))
	must(t, w.WaitFor("da1=", wait))
	screen := w.Screen()
	if runtime.GOOS == "windows" {
		// Through a pseudoconsole the query may be answered by the console host rather
		// than reach the emulator: either way the program gets an answer.
		t.Logf("%s; the emulator answered %d queries; the screen:\n%s", s.Info().Backend, w.Answered(), screen)
		if !strings.Contains(screen, `da1="\x1b[?`) {
			t.Errorf("the program got no device attributes:\n%s", screen)
		}
	} else if !strings.Contains(screen, `da1="\x1b[?62;1;6;22c"`) || !strings.Contains(screen, "ready size=90x25") {
		t.Fatalf("the program's query was not answered by the window's emulator, or the size is wrong:\n%s", screen)
	}
	w.Type("a")
	must(t, w.WaitFor("got:61", wait))
	w.Type("\r")
	must(t, w.WaitFor("got:0d", wait))
	w.Resize(120, 40)
	must(t, w.WaitFor("resized 120x40", wait))
	if i := s.Info(); i.Cols != 120 || i.Rows != 40 {
		t.Errorf("info has %dx%d", i.Cols, i.Rows)
	}
	w.Type("q")
	i, err := w.WaitState(pty.Exited, wait)
	must(t, err)
	if i.Code != 3 {
		t.Fatalf("exit code %d, want 3", i.Code)
	}
	if code, err := s.Wait(context.Background()); err != nil || code != 3 {
		t.Fatalf("Wait: %d %v", code, err)
	}
}

// Exit codes come back as they are.
func TestExitCodes(t *testing.T) {
	m := tabs(t, pty.Options{})
	for _, code := range []string{"0", "7"} {
		s := start(t, m, role(t, "exit", "PTY_TEST_CODE="+code))
		ctx, cancel := context.WithTimeout(context.Background(), wait)
		got, err := s.Wait(ctx)
		cancel()
		must(t, err)
		if want := map[string]int{"0": 0, "7": 7}[code]; got != want {
			t.Errorf("exit %s: got %d", code, got)
		}
		if i := s.Info(); i.State != pty.Exited || i.Code != got {
			t.Errorf("info %+v", i)
		}
	}
}

// Ctrl-C typed in the tab reaches the program as an interrupt.
func TestCtrlC(t *testing.T) {
	m := tabs(t, pty.Options{})
	s := start(t, m, role(t, "sleep"))
	w := open(t, m, s, ptytest.Options{})
	must(t, w.WaitFor("sleeping", wait))
	w.Type("\x03")
	i, err := w.WaitState(pty.Exited, wait)
	must(t, err)
	if runtime.GOOS != "windows" && i.Code != 130 {
		t.Fatalf("code %d, want 130 (SIGINT)", i.Code)
	}
	if i.Code == 0 {
		t.Fatal("the interrupted program reports success")
	}
}

// Closing a tab ends its program (it does not wait for it to finish) and forgets the tab.
func TestCloseEndsTheProgram(t *testing.T) {
	m := tabs(t, pty.Options{})
	s := start(t, m, role(t, "sleep"))
	w := open(t, m, s, ptytest.Options{})
	must(t, w.WaitFor("sleeping", wait))
	if m.Running() != 1 {
		t.Fatalf("running %d", m.Running())
	}
	began := time.Now()
	w.CloseTab()
	select {
	case <-s.Done():
	case <-time.After(wait):
		t.Fatal("the program outlived its tab")
	}
	if d := time.Since(began); d > 10*time.Second {
		t.Errorf("closing took %s", d)
	}
	if err := w.Until(func() bool { _, ok := m.Get(s.ID()); return !ok }, wait, func() string { return "the tab is still listed" }); err != nil {
		t.Fatal(err)
	}
	if i := s.Info(); i.State != pty.Exited || i.Code == 0 {
		t.Fatalf("info %+v", i)
	}
	if m.Running() != 0 || len(m.List()) != 0 {
		t.Fatalf("still listed: %+v", m.List())
	}
	select {
	case <-w.Served:
	case <-time.After(wait):
		t.Fatal("the window's stream outlived its tab")
	}
}

// The tab's state: waiting after a bell or a notification (until the user types), and
// after output followed by quiet; never from what a sign-in tab prints.
func TestStates(t *testing.T) {
	var mu sync.Mutex
	var changes []pty.Info
	m := tabs(t, pty.Options{IdleAfter: 400 * time.Millisecond, OnChange: func(i pty.Info) {
		mu.Lock()
		changes = append(changes, i)
		mu.Unlock()
	}})
	t.Run("bell", func(t *testing.T) {
		s := start(t, m, role(t, "bell"))
		w := open(t, m, s, ptytest.Options{})
		i, err := w.WaitState(pty.Waiting, wait)
		must(t, err)
		if i.Reason != pty.ReasonBell {
			t.Fatalf("reason %q", i.Reason)
		}
		w.Type("x")
		_, err = w.WaitState(pty.Exited, wait)
		must(t, err)
		saw := false
		for _, st := range w.States() {
			saw = saw || st.State == pty.Running && st.Reason == ""
		}
		if !saw {
			t.Errorf("typing did not make it running again: %+v", w.States())
		}
	})
	t.Run("notification", func(t *testing.T) {
		s := start(t, m, role(t, "notify"))
		w := open(t, m, s, ptytest.Options{})
		i, err := w.WaitState(pty.Waiting, wait)
		must(t, err)
		if i.Reason != pty.ReasonNotification {
			t.Fatalf("reason %q", i.Reason)
		}
		w.Type("x")
		_, err = w.WaitState(pty.Exited, wait)
		must(t, err)
	})
	t.Run("idle", func(t *testing.T) {
		s := start(t, m, role(t, "quiet"))
		w := open(t, m, s, ptytest.Options{})
		must(t, w.WaitFor("hello", wait))
		i, err := w.WaitState(pty.Waiting, wait)
		must(t, err)
		if i.Reason != pty.ReasonIdle {
			t.Fatalf("reason %q", i.Reason)
		}
		must(t, m.Close(s.ID()))
	})
	t.Run("sign-in reads nothing", func(t *testing.T) {
		spec := role(t, "bell")
		spec.Private = true
		s := start(t, m, spec)
		w := open(t, m, s, ptytest.Options{})
		must(t, w.WaitFor("working", wait))
		time.Sleep(300 * time.Millisecond) // the bell rang
		w.Type("x")
		_, err := w.WaitState(pty.Exited, wait)
		must(t, err)
		for _, st := range w.States() {
			if st.Reason == pty.ReasonBell || st.Reason == pty.ReasonNotification {
				t.Fatalf("a sign-in tab's output was read: %+v", w.States())
			}
		}
		if !s.Info().Private {
			t.Error("not marked private")
		}
	})
	mu.Lock()
	defer mu.Unlock()
	if len(changes) == 0 {
		t.Error("OnChange never called")
	}
}

// A step's tab keeps the end of its output for the module's reader, once; other tabs keep
// none, and a sign-in tab cannot.
func TestCapture(t *testing.T) {
	m := tabs(t, pty.Options{})
	link := "Created cloud session\r\nView: https://claude.ai/code/session_01ABCDEF\r\n"
	spec := role(t, "print", "PTY_TEST_TEXT="+link)
	spec.Capture = pty.CaptureStep
	s := start(t, m, spec)
	if _, ok := s.StepOutput(); ok {
		t.Error("a step's output before it ended")
	}
	<-s.Done()
	out, ok := s.StepOutput()
	if !ok || !strings.Contains(out.Text, "View: https://claude.ai/code/session_01ABCDEF") || out.Code != 0 || out.Width != 100 {
		t.Fatalf("step output %v %+v", ok, out)
	}
	if strings.Contains(out.Text, "\x1b") || strings.Contains(out.Text, "\r") {
		t.Errorf("not plain text: %q", out.Text)
	}
	if _, ok := s.StepOutput(); ok {
		t.Error("read twice")
	}

	plain := start(t, m, role(t, "print", "PTY_TEST_TEXT="+link))
	<-plain.Done()
	if _, ok := plain.StepOutput(); ok {
		t.Error("a tab without capture kept output for hopsesh")
	}

	spec.Private = true
	if _, err := m.Start(spec); err == nil {
		t.Error("a sign-in tab started with capture")
	}
}

// What a tab printed is drawn again for a window that comes back, from memory, at most
// Options.Scrollback bytes of it.
func TestScrollbackReplay(t *testing.T) {
	m := tabs(t, pty.Options{Scrollback: 4096})
	text := ""
	for i := range 200 {
		text += "line " + strings.Repeat("=", 10) + " " + string(rune('A'+i%26)) + "\r\n"
	}
	text += "the end\r\n"
	s := start(t, m, role(t, "print", "PTY_TEST_TEXT="+text))
	<-s.Done()
	w := open(t, m, s, ptytest.Options{Cols: 80, Rows: 10})
	must(t, w.WaitFor("the end", wait))
	if d := w.Drawn(); d > 4096 {
		t.Fatalf("replayed %d bytes, more than the scrollback", d)
	}
	if w.Last().State != pty.Exited {
		t.Errorf("state %+v", w.Last())
	}
	// A second window replaces the first, whose stream ends.
	w2 := open(t, m, s, ptytest.Options{Cols: 80, Rows: 10})
	must(t, w2.WaitFor("the end", wait))
	select {
	case <-w.Served:
	case <-time.After(wait):
		t.Fatal("the first window kept its stream")
	}
}

// The window's acks pace the program: hopsesh stops reading it while more than 512 KiB
// are not drawn, the program waits on its own output, and it goes on once the window
// catches up. Output frames are at most 64 KiB.
func TestFlowControl(t *testing.T) {
	const total = 4 << 20
	m := tabs(t, pty.Options{})
	s := start(t, m, role(t, "flood", "PTY_TEST_BYTES=4194304"))
	w := open(t, m, s, ptytest.Options{NoAck: true, Depth: 4})
	stable := func() int {
		last, since := -1, time.Now()
		for time.Since(since) < 700*time.Millisecond {
			if d := w.Drawn(); d != last {
				last, since = d, time.Now()
			}
			time.Sleep(20 * time.Millisecond)
		}
		return last
	}
	held := stable()
	if held < 256<<10 || held > 512<<10+128<<10 {
		t.Fatalf("drew %d bytes without acking; want about 512 KiB", held)
	}
	if s.Info().State == pty.Exited {
		t.Fatal("the program finished while the window was behind")
	}
	w.AckAll()
	if _, err := w.WaitState(pty.Exited, 60*time.Second); err != nil {
		t.Fatal(err)
	}
	if d := w.Drawn(); d < total {
		t.Fatalf("drew %d of %d bytes", d, total)
	}
	if i := s.Info(); i.Code != 0 {
		t.Fatalf("code %d", i.Code)
	}
}

// A window that names no tab, or one it may not show, gets nothing.
func TestAttachRefused(t *testing.T) {
	m := tabs(t, pty.Options{})
	w := ptytest.Open(find(m), nil, "0123456789abcdef", ptytest.Options{})
	defer w.Detach()
	select {
	case err := <-w.Served:
		if err == nil {
			t.Fatal("served an unknown tab")
		}
	case <-time.After(wait):
		t.Fatal("still serving")
	}
}

// Links open only as http(s), through the app's (confirming) opener.
func TestLinks(t *testing.T) {
	for raw, want := range map[string]string{
		"https://example.com/a?b=c#d":  "https://example.com/a?b=c#d",
		" HTTP://Example.com/x ":       "http://Example.com/x",
		"javascript:alert(1)":          "",
		"file:///etc/passwd":           "",
		"vscode://file/x":              "",
		"https://":                     "",
		"https://exa\x1bmple.com/":     "",
		"https://example.com/\u0085x":  "",
		"ms-settings:privacy":          "",
		"//example.com/no-scheme":      "",
		"https://user@example.com/a b": "https://user@example.com/a%20b",
	} {
		got, ok := pty.LinkTarget(raw)
		if ok != (want != "") || got != want {
			t.Errorf("LinkTarget(%q) = %q %v, want %q", raw, got, ok, want)
		}
	}
	m := tabs(t, pty.Options{})
	s := start(t, m, role(t, "sleep"))
	var mu sync.Mutex
	var opened []string
	w := ptytest.Open(find(m), func(u string) { mu.Lock(); opened = append(opened, u); mu.Unlock() }, s.ID(), ptytest.Options{})
	defer w.Detach()
	must(t, w.WaitFor("sleeping", wait))
	w.Link("javascript:alert(1)")
	w.Link("https://example.com/docs")
	must(t, w.Until(func() bool { mu.Lock(); defer mu.Unlock(); return len(opened) == 1 }, wait, func() string { return "no link opened" }))
	must(t, w.Until(func() bool { return len(w.Errors()) == 1 }, wait, func() string { return "no refusal" }))
	if opened[0] != "https://example.com/docs" || !strings.Contains(w.Errors()[0], "only web links") {
		t.Fatalf("opened %v, errors %v", opened, w.Errors())
	}
}

// A tab needs a program and a folder that is there; there are at most MaxTabs; once the
// tabs are closed for good, no more start.
func TestStartRefusals(t *testing.T) {
	m := tabs(t, pty.Options{MaxTabs: 1})
	for _, spec := range []pty.Spec{{}, {Argv: []string{""}}, {Argv: []string{"x\x00y"}}, {Argv: []string{"x"}, Dir: "relative"}, {Argv: []string{"x"}, Dir: "/no/such/folder/here"}} {
		if _, err := m.Start(spec); err == nil {
			t.Errorf("started %+v", spec)
		}
	}
	if _, err := m.Start(pty.Spec{Argv: []string{"hopsesh-no-such-program"}, Dir: t.TempDir()}); err == nil {
		t.Error("started a program that is not there")
	}
	if len(m.List()) != 0 {
		t.Fatalf("a failed start left a tab: %+v", m.List())
	}
	s := start(t, m, role(t, "sleep"))
	if _, err := m.Start(role(t, "sleep")); err == nil {
		t.Error("more tabs than MaxTabs")
	}
	m.CloseAll()
	<-s.Done()
	if _, err := m.Start(role(t, "sleep")); err == nil {
		t.Error("started after CloseAll")
	}
}

// The program's environment: the user's, less other terminals' and multiplexers'
// variables, with hopsesh's terminal identity; a step's unset names removed.
func TestEnvironmentReachesTheProgram(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1/default,1,0")
	t.Setenv("VSCODE_PID", "123")
	t.Setenv("HOPSESH_TEST_KEEP", "kept")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-not-a-real-key")
	m := tabs(t, pty.Options{})
	spec := role(t, "env", "PTY_TEST_VARS=TMUX,VSCODE_PID,HOPSESH_TEST_KEEP,TERM,COLORTERM,TERM_PROGRAM,ANTHROPIC_API_KEY")
	spec.Env.Unset = []string{"ANTHROPIC_API_KEY"}
	s := start(t, m, spec)
	w := open(t, m, s, ptytest.Options{Cols: 120})
	if err := w.WaitFor("env-done", wait); err != nil {
		t.Fatalf("%v\n%+v drawn=%d errs=%v", err, s.Info(), w.Drawn(), w.Errors())
	}
	screen := w.Screen()
	for _, want := range []string{"ENV TMUX= set=false", "ENV VSCODE_PID= set=false", "ENV HOPSESH_TEST_KEEP=kept set=true",
		"ENV TERM=xterm-256color set=true", "ENV COLORTERM=truecolor set=true", "ENV TERM_PROGRAM=hopsesh set=true", "ENV ANTHROPIC_API_KEY= set=false"} {
		if !strings.Contains(screen, want) {
			t.Errorf("missing %q in:\n%s", want, screen)
		}
	}
}

func TestEnvBuild(t *testing.T) {
	base := []string{"PATH=/bin", "HOME=/home/alice", "LANG=en_US.UTF-8", "LC_ALL=C", "TMUX=x", "TMUX_PANE=%1", "ZELLIJ_SESSION_NAME=z",
		"WT_SESSION=w", "VSCODE_IPC_HOOK=v", "TERM_PROGRAM=iTerm.app", "ITERM_SESSION_ID=i", "LC_TERMINAL=iTerm2", "TERM=screen",
		"GITHUB_TOKEN=ghp_notreal", "ANTHROPIC_API_KEY=sk-ant-x", "CLAUDE_CONFIG_DIR=/home/alice/.claude", "HTTPS_PROXY=http://proxy:3128", "noequals"}
	has := func(env []string, kv string) bool { return slices.Contains(env, kv) }
	names := func(env []string) map[string]bool {
		o := map[string]bool{}
		for _, kv := range env {
			k, _, _ := strings.Cut(kv, "=")
			o[k] = true
		}
		return o
	}

	inherit := pty.Env{Base: base}.Build("1.2.3")
	n := names(inherit)
	for _, gone := range []string{"TMUX", "TMUX_PANE", "ZELLIJ_SESSION_NAME", "WT_SESSION", "VSCODE_IPC_HOOK", "ITERM_SESSION_ID", "LC_TERMINAL"} {
		if n[gone] {
			t.Errorf("inherit keeps %s", gone)
		}
	}
	for _, kept := range []string{"PATH=/bin", "GITHUB_TOKEN=ghp_notreal", "ANTHROPIC_API_KEY=sk-ant-x", "TERM=xterm-256color", "COLORTERM=truecolor",
		"TERM_PROGRAM=hopsesh", "TERM_PROGRAM_VERSION=1.2.3", "LC_ALL=C"} {
		if !has(inherit, kept) {
			t.Errorf("inherit lacks %s: %v", kept, inherit)
		}
	}

	strict := pty.Env{Base: base, Strict: true, Keep: []string{"CLAUDE_CONFIG_DIR"}, Unset: []string{"LANG"}, Set: []string{"CLAUDE_CODE_FORCE_SYNC_OUTPUT=1", "COLORTERM=24bit"}}.Build("")
	n = names(strict)
	for _, gone := range []string{"GITHUB_TOKEN", "ANTHROPIC_API_KEY", "TMUX", "LANG", "noequals"} {
		if n[gone] {
			t.Errorf("strict keeps %s", gone)
		}
	}
	for _, kept := range []string{"PATH=/bin", "HOME=/home/alice", "LC_ALL=C", "CLAUDE_CONFIG_DIR=/home/alice/.claude", "HTTPS_PROXY=http://proxy:3128",
		"CLAUDE_CODE_FORCE_SYNC_OUTPUT=1", "COLORTERM=24bit", "TERM_PROGRAM_VERSION=dev"} {
		if !has(strict, kept) {
			t.Errorf("strict lacks %s: %v", kept, strict)
		}
	}
	if has(strict, "COLORTERM=truecolor") {
		t.Error("Set does not win")
	}
}
