package app

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/term"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/termapp"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// terminalRole plays the parts of the terminal tests when the test binary runs as one:
// "verb", hopsesh's `terminal-open <ticket>` in a tab (TERMAPP_ROLE=verb); and the agent it
// starts, a stand-in claude that says what it got and waits for Return
// (TERMAPP_AGENT=wait) or ends at once (exit) with code 3.
func terminalRole() (int, bool) {
	if strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") == "claude" && os.Getenv("TERMAPP_AGENT") != "" {
		fmt.Printf("agent running: tty=%v args=%s config=%s\r\n", term.IsTerminal(int(os.Stdin.Fd())), strings.Join(os.Args[1:], " "), os.Getenv("CLAUDE_CONFIG_DIR"))
		if os.Getenv("TERMAPP_AGENT") == "wait" {
			_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		}
		return 3, true
	}
	if os.Getenv("TERMAPP_ROLE") != "verb" {
		return 0, false
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Println(err)
		return 1, true
	}
	a := New(cfg, all.Registry(), config.StateDir(), nil)
	defer a.Catalog.Close()
	tio := TerminalIO{In: os.Stdin, Out: os.Stdout, Err: os.Stdout, Labels: term.IsTerminal(int(os.Stdout.Fd())), Hold: os.Getenv("TERMAPP_HOLD") == "1"}
	if err := a.RunTicket(os.Getenv("TERMAPP_TICKET"), tio); err != nil {
		fmt.Printf("verb error: %v\r\n", err)
		return 1, true
	}
	return 0, true
}

// scripts is a terminal app's AppleScript, recorded and answered; nothing reaches
// osascript.
type scripts struct {
	got    []string
	answer func(string) (string, error)
}

func (s *scripts) run(_ context.Context, script string) (string, error) {
	s.got = append(s.got, script)
	return s.answer(script)
}

type procTable struct {
	alive map[int]bool
	tty   map[int]string
}

func (p procTable) Alive(pid int) bool                    { return p.alive[pid] }
func (p procTable) TTY(_ context.Context, pid int) string { return p.tty[pid] }

var denied = func(string) (string, error) {
	return "", fmt.Errorf("%w (System Settings › Privacy & Security › Automation)", termapp.ErrDenied)
}

func yes() bool { return true }

// A launch from the app: the ticket holds the command, the terminal app gets only
// hopsesh's path, the verb and the ticket's id. iTerm2 denied: it opens in Terminal and
// says so; Terminal denied too: nothing opens, the ticket is dropped, and the error says
// to copy the command.
func TestOpenInTerminal(t *testing.T) {
	cloudEnv(t, "claude")
	a := cloudApp(t, all.Registry())
	it := &scripts{answer: denied}
	ta := &scripts{answer: func(string) (string, error) { return "/dev/ttys002", nil }}
	a.Terminals = termapp.NewSet(termapp.ITerm2Using(it.run, yes), termapp.TerminalAppUsing(ta.run, yes))
	dir := t.TempDir()
	l := Launch{Kind: termapp.KindSession, Key: agent.SessionKey{Agent: "claude", Session: "sess-1"},
		Run:    agent.Command{Argv: []string{"claude", "--resume", "sess-1", "[hopsesh] the start prompt"}, Dir: dir},
		Labels: termapp.Labels{Title: "Fix \"the\" parser", Agent: "Claude Code", Machine: "here"}}
	prog := "/opt/hopsesh/bin/hopsesh"
	if runtime.GOOS == "windows" {
		prog = "C:" + prog // absolute there
	}
	o, err := a.OpenInTerminal(context.Background(), prog, nil, l)
	if err != nil || !o.FellBack || o.Terminal != "Terminal" {
		t.Fatalf("%+v %v", o, err)
	}
	if len(it.got) != 1 || len(ta.got) != 1 {
		t.Fatalf("scripts: %d %d", len(it.got), len(ta.got))
	}
	for _, s := range append(it.got, ta.got...) {
		if strings.Contains(s, "parser") || strings.Contains(s, "prompt") || strings.Contains(s, "--resume") || !strings.Contains(s, prog+" terminal-open ") {
			t.Fatalf("the script carries more than hopsesh's verb:\n%s", s)
		}
	}
	tickets, _ := os.ReadDir(filepath.Join(a.StateDir, "terminal", "tickets"))
	if len(tickets) != 1 {
		t.Fatalf("tickets: %v", tickets)
	}
	id := strings.TrimSuffix(tickets[0].Name(), ".json")
	if !strings.Contains(ta.got[0], "terminal-open "+id) {
		t.Fatal(ta.got[0])
	}
	tk, err := a.termStore().Take(id, ticketAge)
	if err != nil || !filepath.IsAbs(tk.Argv[0]) || tk.Argv[3] != "[hopsesh] the start prompt" || tk.Key != "claude/sess-1" {
		t.Fatalf("%+v %v", tk, err)
	}
	ta.answer = denied
	_, err = a.OpenInTerminal(context.Background(), prog, nil, l)
	if !errors.Is(err, termapp.ErrNoTerminal) {
		t.Fatal(err)
	}
	if tickets, _ := os.ReadDir(filepath.Join(a.StateDir, "terminal", "tickets")); len(tickets) != 0 {
		t.Fatalf("a ticket stays: %v", tickets)
	}
}

// The verb runs only an enabled agent's own program, in a folder that is there, once.
func TestRunTicketRefuses(t *testing.T) {
	cloudEnv(t, "claude")
	a := cloudApp(t, all.Registry())
	dir := t.TempDir()
	for _, run := range []agent.Command{
		{Argv: []string{"/bin/sh", "-c", "touch " + filepath.Join(dir, "ran")}, Dir: dir},
		{Argv: []string{"claude", "--resume", "x"}, Dir: filepath.Join(dir, "gone")},
	} {
		id, err := a.termStore().Save(termapp.Ticket{Kind: termapp.KindSession, Argv: run.Argv, Dir: run.Dir})
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		err = a.RunTicket(id, TerminalIO{In: strings.NewReader(""), Out: &out, Err: &out})
		if err == nil || !strings.Contains(err.Error(), "hopsesh could not open this") {
			t.Fatalf("%v: %v %q", run.Argv, err, out.String())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "ran")); err == nil {
		t.Fatal("a ticket ran another program")
	}
	if err := a.RunTicket("0123456789abcdef", TerminalIO{In: strings.NewReader(""), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}); err == nil {
		t.Fatal("ran a ticket that is not there")
	}
}

// A launch's exit code for a watcher comes from hopsesh's own records only: a ticket's
// exit (terminal-open) or a step's outcome (terminal-step); a refused ticket ends with -1.
func TestLaunchExit(t *testing.T) {
	cloudEnv(t, "claude")
	a := cloudApp(t, all.Registry())
	step := "00112233445566aa"
	if _, ok := a.launchExit(termapp.Handle{Verb: "terminal-step", Ticket: step}); ok {
		t.Fatal("a step's exit before its outcome")
	}
	if err := os.MkdirAll(filepath.Join(a.StateDir, "steps"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := a.writeOutcome(step, StepOutcome{Code: 5}); err != nil {
		t.Fatal(err)
	}
	if code, ok := a.launchExit(termapp.Handle{Verb: "terminal-step", Ticket: step}); !ok || code != 5 {
		t.Fatalf("step exit %d %v", code, ok)
	}
	if _, ok := a.launchExit(termapp.Handle{Verb: "something-else", Ticket: step}); ok {
		t.Fatal("an exit for an unknown verb")
	}
	id, err := a.termStore().Save(termapp.Ticket{Kind: termapp.KindSession, Argv: []string{"/bin/sh"}, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	_ = a.RunTicket(id, TerminalIO{In: strings.NewReader(""), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}})
	if code, ok := a.launchExit(termapp.Handle{Verb: "terminal-open", Ticket: id}); !ok || code != -1 {
		t.Fatalf("refused ticket exit %d %v", code, ok)
	}
	// The default terminals (none injected) read exits through the app; a launch in a
	// terminal that cannot watch says so.
	if _, err := a.WatchLaunch(context.Background(), termapp.Handle{Terminal: "nope"}); err == nil {
		t.Fatal("watched a launch in an unknown terminal")
	}
}

// Rule 14: a session's tab is found from the agent's process id (Claude's registry) or
// hopsesh's own record (Codex has no process id in its files), never from what a tab
// shows; a session open elsewhere is shown, not opened twice.
func TestSessionTab(t *testing.T) {
	cloudEnv(t, "claude")
	a := cloudApp(t, all.Registry())
	a.Procs = procTable{alive: map[int]bool{100: true, 200: true, 300: true}, tty: map[int]string{100: "/dev/ttys004", 200: "/dev/ttys007", 300: "/dev/ttys008"}}
	it := &scripts{answer: func(s string) (string, error) {
		for ref, tty := range map[string]string{"SESSION-A": "/dev/ttys004", "SESSION-B": "/dev/ttys007"} {
			if strings.Contains(s, "select s") && strings.Contains(s, `unique ID of s is "`+ref+`"`) && strings.Contains(s, `tty of s is "`+tty+`"`) {
				return "ok", nil
			}
			if strings.Contains(s, `if tty of s is "`+tty+`"`) {
				return ref + "\t" + tty, nil
			}
		}
		return "", nil
	}}
	ta := &scripts{answer: func(string) (string, error) { return "", nil }}
	a.Terminals = termapp.NewSet(termapp.ITerm2Using(it.run, yes), termapp.TerminalAppUsing(ta.run, yes))
	ctx := context.Background()
	claude := agent.SessionKey{Agent: "claude", Session: "c1"}
	codex := agent.SessionKey{Agent: "codex", Session: "t1"}

	f, ok, err := a.SessionTab(ctx, claude, 100)
	if err != nil || !ok || f.Handle.Ref != "SESSION-A" || f.Name() != "iTerm2" {
		t.Fatalf("%+v %v %v", f, ok, err)
	}
	if _, ok, _ := a.SessionTab(ctx, codex, 0); ok {
		t.Fatal("found a Codex session nobody started")
	}
	if err := a.termStore().Record(termapp.Record{Ticket: "00000000000000b1", Key: codex.String(), TTY: "/dev/ttys007", PID: 200}); err != nil {
		t.Fatal(err)
	}
	if f, ok, _ := a.SessionTab(ctx, codex, 0); !ok || f.Handle.Ref != "SESSION-B" || !a.Running(codex) {
		t.Fatalf("%+v", f)
	}
	if err := a.OpenElsewhere(ctx, codex, agent.LiveInfo{}); !errors.Is(err, ErrOpenElsewhere) || !strings.Contains(err.Error(), "in iTerm2") {
		t.Fatal(err)
	}
	// Live in a terminal hopsesh does not have: said, with nothing to show.
	if err := a.OpenElsewhere(ctx, claude, agent.LiveInfo{State: agent.Live, PID: 300}); !errors.Is(err, ErrOpenElsewhere) || !strings.Contains(err.Error(), "another terminal") {
		t.Fatal(err)
	}
	if err := a.OpenElsewhere(ctx, agent.SessionKey{Agent: "claude", Session: "c9"}, agent.LiveInfo{State: agent.Ended}); err != nil {
		t.Fatal(err)
	}
	// A tab is shown only while its process is still on it.
	if err := a.ShowTab(ctx, f); err != nil {
		t.Fatal(err)
	}
	a.Procs = procTable{alive: map[int]bool{100: true}, tty: map[int]string{100: "/dev/ttys009"}}
	if err := a.ShowTab(ctx, f); !errors.Is(err, termapp.ErrGone) {
		t.Fatal(err)
	}
	for _, s := range append(it.got, ta.got...) {
		if err := termapp.VetScript(s); err != nil {
			t.Fatal(err)
		}
	}
}

// A ticket for an agent npm installed on Windows names its claude.cmd: that is still
// Claude Code's own program (runLaunch then runs the program behind the shim).
func TestCheckTicketTakesNpmShims(t *testing.T) {
	cloudEnv(t, "claude")
	a := cloudApp(t, all.Registry())
	dir := t.TempDir()
	for _, prog := range []string{`C:\Users\alice\AppData\Roaming\npm\claude.cmd`, `C:\Users\alice\.local\bin\claude.exe`, "/usr/local/bin/claude", `C:\npm\CLAUDE.CMD`} {
		if err := a.checkTicket(termapp.Ticket{Kind: termapp.KindSession, Argv: []string{prog, "--resume", "x"}, Dir: dir}); err != nil {
			t.Errorf("%s: %v", prog, err)
		}
	}
	for _, prog := range []string{`C:\Windows\System32\cmd.exe`, `C:\npm\claude.ps1`, `C:\npm\claude-evil.cmd`} {
		if err := a.checkTicket(termapp.Ticket{Kind: termapp.KindSession, Argv: []string{prog}, Dir: dir}); err == nil {
			t.Errorf("%s passed for an agent's program", prog)
		}
	}
}
