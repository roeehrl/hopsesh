// Package scenario runs hopsesh's command line through scripted scenarios (testdata/*.txtar)
// with stand-in claude and codex programs on the PATH, on every OS, with no network.
package scenario

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/ui/cli"
)

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"hopsesh": func() { os.Exit(hopsesh()) },
		"claude":  func() { os.Exit(fakeClaude()) },
		"codex":   func() { os.Exit(fakeCodex()) },
	})
}

func hopsesh() int {
	if err := cli.NewRoot(os.Stdout, all.Registry()).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func TestScenarios(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir: "testdata",
		Setup: func(env *testscript.Env) error {
			home := filepath.Join(env.WorkDir, "home")
			for _, d := range []string{home, filepath.Join(home, ".claude"), filepath.Join(home, ".codex", "sessions")} {
				if err := os.MkdirAll(d, 0o700); err != nil {
					return err
				}
			}
			env.Setenv("HOME", home)
			env.Setenv("USERPROFILE", home)
			env.Setenv("HOPSESH_CONFIG_DIR", filepath.Join(home, "hopsesh-config"))
			env.Setenv("HOPSESH_STATE_DIR", filepath.Join(home, "hopsesh-state"))
			env.Setenv("HOPSESH_MACHINE", "here")
			env.Setenv("HOPSESH_TAILSCALE", "off")
			env.Setenv("FAKE_AGENT_LOG", filepath.Join(env.WorkDir, "agents.log"))
			env.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
			env.Setenv("GIT_AUTHOR_NAME", "Sam Doe")
			env.Setenv("GIT_AUTHOR_EMAIL", "sam@example.com")
			env.Setenv("GIT_COMMITTER_NAME", "Sam Doe")
			env.Setenv("GIT_COMMITTER_EMAIL", "sam@example.com")
			return nil
		},
		Cmds: map[string]func(ts *testscript.TestScript, neg bool, args []string){
			"claude-session": claudeSession,
			"git-repo":       gitRepo,
		},
	})
}

// claudeSession writes a Claude Code session: claude-session [-desktop] ID DIR TITLE
// PROMPT... (DIR is relative to the script's work folder; the prompt names a file in it).
// -desktop writes it as the Claude desktop app's Claude Code does: its entry point and the
// app's own records.
func claudeSession(ts *testscript.TestScript, neg bool, args []string) {
	desktop := len(args) > 0 && args[0] == "-desktop"
	if desktop {
		args = args[1:]
	}
	if neg || len(args) < 4 {
		ts.Fatalf("usage: claude-session [-desktop] ID DIR TITLE PROMPT...")
	}
	id, title, prompt := args[0], args[2], strings.Join(args[3:], " ")
	dir := ts.MkAbs(args[1])
	if err := os.MkdirAll(dir, 0o700); err != nil {
		ts.Fatalf("%v", err)
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	rec := func(v map[string]any) string { b, _ := json.Marshal(v); return string(b) }
	file := filepath.Join(dir, "main.go")
	entry := "cli"
	if desktop {
		entry = "claude-desktop"
	}
	var lines []string
	if desktop {
		lines = append(lines, rec(map[string]any{"type": "mode", "mode": "default", "sessionId": id}))
	}
	lines = append(lines,
		rec(map[string]any{"type": "user", "uuid": "u1", "parentUuid": nil, "sessionId": id, "cwd": dir, "version": "2.1.284", "entrypoint": entry,
			"timestamp": "2026-10-01T10:00:00Z", "message": map[string]any{"role": "user", "content": prompt + " " + file}}),
		rec(map[string]any{"type": "assistant", "uuid": "a1", "parentUuid": "u1", "sessionId": id, "cwd": dir, "entrypoint": entry,
			"timestamp": "2026-10-01T10:00:05Z", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Edited " + file}}}}),
		rec(map[string]any{"type": "custom-title", "customTitle": title, "sessionId": id}),
	)
	if desktop {
		lines = append(lines, rec(map[string]any{"type": "agent-name", "agentName": title, "sessionId": id}))
	}
	path := filepath.Join(ts.Getenv("HOME"), ".claude", "projects", claude.Slug(dir), id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		ts.Fatalf("%v", err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		ts.Fatalf("%v", err)
	}
}

// gitRepo makes a git repository with one commit and a remote: git-repo DIR URL.
func gitRepo(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 2 {
		ts.Fatalf("usage: git-repo DIR URL")
	}
	dir := ts.MkAbs(args[0])
	if err := os.MkdirAll(dir, 0o700); err != nil {
		ts.Fatalf("%v", err)
	}
	for _, a := range [][]string{{"init", "-q", "-b", "main"}, {"remote", "add", "origin", args[1]}, {"commit", "-q", "--allow-empty", "-m", "start"}} {
		cmd := exec.Command("git", a...)
		cmd.Dir = dir
		cmd.Env = os.Environ()
		for _, k := range []string{"HOME", "GIT_CONFIG_GLOBAL", "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL"} {
			cmd.Env = append(cmd.Env, k+"="+ts.Getenv(k))
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			ts.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
}
