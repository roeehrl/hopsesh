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
	"github.com/roeehrl/hopsesh/internal/testkit/fakeagent"
	"github.com/roeehrl/hopsesh/internal/testkit/fakecloud"
	"github.com/roeehrl/hopsesh/internal/ui/cli"
)

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"hopsesh": func() { os.Exit(hopsesh()) },
		"claude":  func() { os.Exit(fakeagent.Claude()) },
		"codex":   func() { os.Exit(fakeagent.Codex()) },
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
			"agent-turn":     agentTurn,
			"claude-session": claudeSession,
			"cloud-world":    cloudWorld,
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

// agentTurn appends a turn to a session the way its agent does when you keep working in
// it: agent-turn AGENT ID TEXT (claude or codex; ID may be a glob). It sets $SESSION to the
// session's file.
func agentTurn(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 3 {
		ts.Fatalf("usage: agent-turn AGENT ID TEXT")
	}
	home, id, text := ts.Getenv("HOME"), args[1], args[2]
	rec := func(v map[string]any) string { b, _ := json.Marshal(v); return string(b) }
	var pattern, line string
	switch args[0] {
	case "claude":
		pattern = filepath.Join(home, ".claude", "projects", "*", id+".jsonl")
		line = rec(map[string]any{"type": "user", "uuid": "turn-" + text, "sessionId": id, "timestamp": "2026-10-01T11:00:00Z",
			"message": map[string]any{"role": "user", "content": text}})
	case "codex":
		pattern = filepath.Join(home, ".codex", "sessions", "*", "*", "*", "rollout-*-"+id+".jsonl")
		line = rec(map[string]any{"timestamp": "2026-10-01T11:00:00Z", "type": "response_item",
			"payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": text}}}})
	default:
		ts.Fatalf("agent-turn: unknown agent %q", args[0])
	}
	files, _ := filepath.Glob(pattern)
	if len(files) != 1 {
		ts.Fatalf("agent-turn: %d sessions match %s", len(files), pattern)
	}
	f, err := os.OpenFile(files[0], os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		ts.Fatalf("%v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		ts.Fatalf("%v", err)
	}
	ts.Setenv("SESSION", files[0])
}

// cloudWorld makes a repository whose GitHub remote is a local bare repository, and a
// Claude Code cloud session on it that pushed its work to a claude/… branch: cloud-world
// DIR. It sets $CLOUDID to the session's id and $FAKE_CLOUD_DIR to the stand-in cloud.
func cloudWorld(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 1 {
		ts.Fatalf("usage: cloud-world DIR")
	}
	for _, k := range []string{"HOME", "GIT_CONFIG_GLOBAL", "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL"} {
		os.Setenv(k, ts.Getenv(k)) // fakecloud runs git in this process
	}
	store := ts.MkAbs("cloud")
	ts.Setenv("FAKE_CLOUD_DIR", store)
	os.Setenv("FAKE_CLOUD_DIR", store)
	o, err := fakecloud.NewOrigin(ts.MkAbs("origins"), "https://github.com/example/demo.git")
	ts.Check(err)
	ts.Check(o.Redirect(ts.Getenv("GIT_CONFIG_GLOBAL")))
	gitRepo(ts, false, []string{args[0], o.URL})
	dir := ts.MkAbs(args[0])
	for _, a := range [][]string{{"push", "-q", "origin", "main"}} {
		cmd := exec.Command("git", a...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			ts.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
	s, err := fakecloud.Open(store).Seed(fakecloud.Session{Cloud: fakecloud.ClaudeCloud, Title: "Add rate limiting", Repo: "github.com/example/demo",
		CloneURL: o.FileURL(), Branch: "main", Code: "branch",
		Messages: []fakecloud.Message{{Role: "user", Text: "[hopsesh] add rate limiting to search"}, {Role: "assistant", Text: "On it."}}})
	ts.Check(err)
	ts.Check(fakecloud.Work(fakecloud.Proc{Vars: map[string]string{}}, s.ID, false))
	ts.Setenv("CLOUDID", s.ID)
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
