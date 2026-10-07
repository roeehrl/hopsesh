package launch

import (
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestStartPromptCoversMoveAndChecks(t *testing.T) {
	p := StartPrompt(Context{
		AgentName: "Claude Code", SourceLocation: "studio", SourceOS: "macOS", SourceVersion: "2.1.284", SourceCWD: "/Users/a/git/p",
		TargetLocation: "laptop", TargetOS: "macOS", TargetCWD: "/Users/b/git/p/.claude/worktrees/feat",
		Branch: "feat", WorktreeNote: "A matching worktree was created here.", Unpushed: 2, Dirty: 7,
		SecretsFound: 1, Redacted: true,
	})
	for _, want := range []string{"moved here from another machine", "studio", "laptop", "Claude Code 2.1.284", "branch feat",
		"worktree", "2 unpushed", "7 uncommitted", "git status", "environment variables", "redacted",
		"wait for my go-ahead"} {
		if !strings.Contains(p, want) {
			t.Errorf("start prompt lacks %q:\n%s", want, p)
		}
	}
}

func TestShellQuoting(t *testing.T) {
	c := agent.Command{Dir: "/Users/b/My Repo", Argv: []string{"claude", "--resume", "abc", "--fork-session", "it's moved"}}
	if sh := Shell(c, "", "posix"); sh != `cd '/Users/b/My Repo' && claude --resume abc --fork-session 'it'"'"'s moved'` {
		t.Errorf("posix: %s", sh)
	}
	if ps := Shell(c, "", "powershell"); ps != `Set-Location '/Users/b/My Repo'; & 'claude' '--resume' 'abc' '--fork-session' 'it''s moved'` {
		t.Errorf("powershell: %s", ps)
	}
	f := agent.Command{Dir: "/d", Argv: []string{"claude", "--resume", "abc", "long prompt"}}
	if got := Shell(f, "/s/p.md", "posix"); got != `cd /d && claude --resume abc "$(cat /s/p.md)"` {
		t.Errorf("posix with file: %s", got)
	}
	if got := Shell(f, "/s/p.md", "powershell"); got != `Set-Location '/d'; & 'claude' '--resume' 'abc' (Get-Content -Raw '/s/p.md')` {
		t.Errorf("powershell with file: %s", got)
	}
}

// A cloud driver's command runs without the variables its cloud must not inherit.
func TestShellUnset(t *testing.T) {
	c := agent.Command{Dir: "/w", Argv: []string{"claude", "--teleport", "session_01x"}, Unset: []string{"CLAUDE_CODE_CHILD_SESSION", "ANTHROPIC_API_KEY"}}
	if sh := Shell(c, "", "posix"); sh != `cd /w && env -u CLAUDE_CODE_CHILD_SESSION -u ANTHROPIC_API_KEY claude --teleport session_01x` {
		t.Errorf("posix: %s", sh)
	}
	want := `Set-Location '/w'; Remove-Item -ErrorAction SilentlyContinue 'Env:CLAUDE_CODE_CHILD_SESSION'; Remove-Item -ErrorAction SilentlyContinue 'Env:ANTHROPIC_API_KEY'; & 'claude' '--teleport' 'session_01x'`
	if ps := Shell(c, "", "powershell"); ps != want {
		t.Errorf("powershell: %s", ps)
	}
}

func TestSessionName(t *testing.T) {
	if got := SessionName("Fix the flaky test!", "Laptop.local"); got != "fix-the-flaky-test@laptop" {
		t.Errorf("got %q", got)
	}
	if got := SessionName("", "box"); got != "session@box" {
		t.Errorf("got %q", got)
	}
}
