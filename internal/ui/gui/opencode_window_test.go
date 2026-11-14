package gui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/pty"
	"github.com/roeehrl/hopsesh/internal/testkit"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// OpenCode counts a session's messages, not its bytes: the window gets the count as
// such, never as a size in kilobytes.
func TestOpenCodeSizesInMessages(t *testing.T) {
	a, _, _ := openCodeWindow(t, "")
	scan, err := a.Scan()
	if err != nil {
		t.Fatal(err)
	}
	oc := findEntry(t, scan, "opencode/"+testkit.DemoOpenCode[0])
	if oc.Messages <= 0 || oc.SizeKB != 0 {
		t.Fatalf("OpenCode's size: %d messages, %d KB", oc.Messages, oc.SizeKB)
	}
	cc := findEntry(t, scan, "claude/"+sid)
	if cc.Messages != 0 || cc.SizeKB <= 0 {
		t.Fatalf("Claude Code's size: %d messages, %d KB", cc.Messages, cc.SizeKB)
	}
}

// A session continued into OpenCode while its background service runs another version:
// the done screen says opening it restarts the service, the window asks, and the yes
// opens it once, in a tab running the stand-in.
func TestContinuedIntoOpenCodeAsksToOpen(t *testing.T) {
	const service = "2.2.5"
	a, bin, lines := openCodeWindow(t, service)
	scan, err := a.Scan()
	if err != nil {
		t.Fatal(err)
	}
	e := findEntry(t, scan, "claude/"+sid)
	if _, err := a.Plan(e.Machine, e.Key, "opencode", OptsDTO{Go: true}); err != nil {
		t.Fatal(err)
	}
	d, err := a.Apply()
	if err != nil {
		t.Fatal(err)
	}
	if !d.RestartsService || d.Command == "" {
		t.Fatalf("the done screen: %+v", d)
	}
	_, err = a.OpenResult(WhereHere)
	if err == nil || !strings.HasPrefix(err.Error(), restartAsk) || !errors.Is(err, agent.ErrRestartsService) {
		t.Fatalf("opened unasked: %v", err)
	}
	if err := a.AllowResultRestart(); err != nil {
		t.Fatal(err)
	}
	r, err := a.OpenResult(WhereHere)
	if err != nil || r.Where != WhereHere || len(*lines) != 0 {
		t.Fatalf("agreed: %+v %v %q", r, err, *lines)
	}
	x, ok := a.Terms.tabOf(r.Tab)
	if !ok || !strings.HasPrefix(x.spec.Argv[0], bin) {
		t.Fatalf("the tab runs another opencode than the stand-in: %+v", x)
	}
	// The plan's own start: its first prompt, not a bare resume.
	if i := slices.Index(x.spec.Argv, "--prompt"); i < 0 || i+1 >= len(x.spec.Argv) || x.spec.Argv[i+1] != "Continue." {
		t.Fatalf("the agreed open lost the plan's prompt: %q", x.spec.Argv)
	}
	tb := waitTab(t, a, func(d TermTab) bool { return d.ID == r.Tab && d.State == pty.Exited })
	if !strings.HasPrefix(tb.Command, "opencode ") {
		t.Fatalf("the tab: %+v", tb)
	}
	// The yes was used: it holds for no other opening.
	if a.resumeOptions(app.LocalName(), d.Key, agent.ResumeOptions{}).RestartService {
		t.Fatal("the yes held twice")
	}
}

// A tab names the agent's program by the agent's own name, also when the desktop app's
// copy of it runs (OpenCode's opencode-cli); another program by its file name.
func TestTabProgramNames(t *testing.T) {
	a, _, _ := openCodeWindow(t, "")
	for argv0, want := range map[string]string{
		"/Applications/OpenCode.app/Contents/Resources/opencode-cli":            "opencode",
		"/home/u/.local/share/ai.opencode.desktop/cli/2.2.7-arm64/opencode-cli": "opencode",
		"opencode":        "opencode",
		"/usr/bin/claude": "claude",
		"/bin/zsh":        "zsh",
		"claude-work":     "claude-work", // an account folder's command keeps its name
		"OPENCODE.EXE":    "opencode",    // Windows ignores case
		"tool.Exe":        "tool",
	} {
		if got := a.programName(argv0); got != want {
			t.Errorf("%s: %q, want %q", argv0, got, want)
		}
	}
	if m, _ := a.programOf("/bin/zsh"); m != nil {
		t.Errorf("zsh is %s's", m.Spec().ID)
	}
	if m, _ := a.programOf("OpenCode-CLI.EXE"); m == nil || len(a.terminalEnv("OPENCODE.EXE")) != len(m.Spec().TerminalEnv) {
		t.Errorf("an upper-case program is not OpenCode's: %v", m)
	}
	if got := displayCommand(append([]string{a.programName("/x/opencode-cli")}, "-s", "ses_1")); got != "opencode -s ses_1" {
		t.Errorf("the command: %q", got)
	}
}
