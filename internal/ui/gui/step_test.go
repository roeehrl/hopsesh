package gui

import (
	"context"
	"errors"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// A hand-off's terminal step from the window: hopsesh opens a terminal on its own
// `terminal-step` line, which runs the driver and leaves the outcome for the window; the
// window shows the wait, takes a pasted link instead, or stops waiting; and a terminal
// that cannot be opened is said.
func TestWindowTerminalStep(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in claude is linked in, which needs a POSIX system here")
	}
	home(t)
	bin := filepath.Join(t.TempDir(), "bin")
	os.MkdirAll(bin, 0o700)
	self, _ := os.Executable()
	if err := os.Symlink(self, filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+testPath())
	t.Setenv("FAKE_CLOUD_DIR", t.TempDir())
	a := NewApp(all.Registry())
	core := a.snapshot()
	folder := filepath.Join(core.StateDir, "handoff", "github.com", "example", "demo")
	os.MkdirAll(folder, 0o700)
	step := move.TermStep{Agent: "claude", Cloud: "claude-cloud", CloudTitle: "Claude Code cloud", Title: "Fix the parser", Folder: folder,
		Run: agent.Command{Argv: []string{filepath.Join(bin, "claude"), "--cloud", "[hopsesh] the briefing"}, Dir: folder, Unset: []string{"ANTHROPIC_API_KEY"}}}
	var lines []string
	defer SetTerminal(terminal)
	SetStepProgram(self)

	// The terminal runs the line: the stand-in claude, in a folder that is no checkout,
	// refuses, and the window gets the refusal.
	SetTerminal(func(line string) error {
		lines = append(lines, line)
		return osexec.Command("sh", "-c", line).Start()
	})
	_, err := a.runStep(context.Background(), step)
	if err == nil || errors.Is(err, agent.ErrNoSession) || !strings.Contains(err.Error(), "claude refused") {
		t.Fatalf("a refusal: %v", err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "terminal-step") || !strings.Contains(lines[0], folder) || strings.Contains(lines[0], "briefing") {
		t.Fatalf("the line: %v", lines)
	}
	if entries, _ := os.ReadDir(filepath.Join(core.StateDir, "steps")); len(entries) != 0 {
		t.Errorf("the step's files stay: %v", entries)
	}

	// Nothing in the terminal yet (the user has not answered): the window waits and shows
	// it, takes a pasted link, and refuses one of another cloud.
	SetTerminal(func(string) error { return nil })
	type result struct {
		res move.StepResult
		err error
	}
	got := make(chan result, 1)
	go func() {
		r, err := a.runStep(context.Background(), step)
		got <- result{r, err}
	}()
	st := waitStep(t, a, "waiting")
	if st.Folder != folder || st.Driver != "claude" || st.CloudTitle != "Claude Code cloud" {
		t.Fatalf("waiting: %+v", st)
	}
	if err := a.HandoffPasteLink("https://chatgpt.com/codex/tasks/task_e_1"); err == nil {
		t.Fatal("a link of another cloud is refused")
	}
	if err := a.HandoffPasteLink("https://claude.ai/code/session_01PastedAbc123?from=cli&m=0"); err != nil {
		t.Fatal(err)
	}
	r := <-got
	if r.err != nil || !r.res.Pasted || r.res.Session.Key.Session != "session_01PastedAbc123" || r.res.Session.URL != "https://claude.ai/code/session_01PastedAbc123" {
		t.Fatalf("pasted: %+v %v", r.res, r.err)
	}
	if a.HandoffStep() != nil {
		t.Fatal("the wait is over")
	}

	// Stopping the wait ends the hand-off without a session.
	go func() {
		r, err := a.runStep(context.Background(), step)
		got <- result{r, err}
	}()
	waitStep(t, a, "waiting")
	a.HandoffStopWaiting()
	if r := <-got; !errors.Is(r.err, agent.ErrNoSession) || !strings.Contains(r.err.Error(), "you stopped waiting") {
		t.Fatalf("stopped: %v", r.err)
	}

	// No terminal could be opened: the window says so, and still takes a link.
	SetTerminal(func(string) error { return errors.New("no terminal emulator found") })
	go func() {
		r, err := a.runStep(context.Background(), step)
		got <- result{r, err}
	}()
	if st := waitStep(t, a, "no-terminal"); !strings.Contains(st.Message, "no terminal emulator found") {
		t.Fatalf("no terminal: %+v", st)
	}
	a.HandoffStopWaiting()
	<-got
}

func waitStep(t *testing.T, a *App, state string) *HandoffStepDTO {
	t.Helper()
	end := time.Now().Add(10 * time.Second)
	for time.Now().Before(end) {
		if st := a.HandoffStep(); st != nil && st.State == state {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the step never was %s: %+v", state, a.HandoffStep())
	return nil
}
