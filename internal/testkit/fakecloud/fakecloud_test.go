package fakecloud

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
)

// The stub cloud-only module passes the cloud conformance kit against the fake.
func TestStubConformance(t *testing.T) {
	agenttest.RunCloud(t, Stub(), Programs(t.TempDir(), nil))
}

// world is a home with a demo repository whose GitHub remote is redirected to a bare
// repository, and a fake cloud store.
type world struct {
	t                      *testing.T
	home, repo, store, log string
	vars                   map[string]string
}

func newWorld(t *testing.T) *world {
	if runtime.GOOS == "windows" {
		t.Skip("the pre-receive hook and the fixtures assume a POSIX shell")
	}
	dir := t.TempDir()
	w := &world{t: t, home: filepath.Join(dir, "home"), store: filepath.Join(dir, "cloud"), log: filepath.Join(dir, "agents.log")}
	w.repo = filepath.Join(w.home, "git", "demo")
	gitConfig := filepath.Join(dir, "gitconfig")
	w.vars = map[string]string{"HOME": w.home, "FAKE_CLOUD_DIR": w.store, "FAKE_AGENT_LOG": w.log, "GIT_CONFIG_GLOBAL": gitConfig,
		"GIT_CONFIG_NOSYSTEM": "1", "GIT_AUTHOR_NAME": "Sam Doe", "GIT_AUTHOR_EMAIL": "sam@example.com",
		"GIT_COMMITTER_NAME": "Sam Doe", "GIT_COMMITTER_EMAIL": "sam@example.com", "CLAUDE_CONFIG_DIR": "",
		"CLAUDE_CODE_CHILD_SESSION": "", "ANTHROPIC_API_KEY": ""} // the tests may run inside an agent's session
	for k, v := range w.vars {
		t.Setenv(k, v)
	}
	o, err := NewOrigin(dir, "https://github.com/example/demo.git")
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Redirect(gitConfig); err != nil {
		t.Fatal(err)
	}
	must(t, os.MkdirAll(w.repo, 0o700))
	w.git("init", "-q", "-b", "main")
	w.git("remote", "add", "origin", o.URL)
	must(t, os.WriteFile(filepath.Join(w.repo, "README.md"), []byte("demo\n"), 0o600))
	w.git("add", "-A")
	w.git("commit", "-q", "-m", "init")
	w.git("push", "-q", "origin", "main")
	return w
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (w *world) git(args ...string) string {
	w.t.Helper()
	out, err := git(nil, w.repo, args...)
	if err != nil {
		w.t.Fatal(err)
	}
	return out
}

// run runs a stand-in program in the demo repository.
func (w *world) run(fail string, main func(Proc) int, args ...string) (string, string, int) {
	var out, errOut bytes.Buffer
	vars := map[string]string{}
	if fail != "" {
		vars["FAKE_CLOUD_FAIL"] = fail
	}
	code := main(Proc{Args: args, Vars: vars, Dir: w.repo, Stdout: &out, Stderr: &errOut})
	return out.String(), errOut.String(), code
}

func claudeMain(p Proc) int { _, code := Claude(p); return code }
func codexMain(p Proc) int  { _, code := Codex(p); return code }

// The remote stays github.com/example/demo for hopsesh, while git reaches the bare origin.
func TestOriginKeepsTheGitHubIdentity(t *testing.T) {
	w := newWorld(t)
	if id := repos.Identity(w.git("config", "--get", "remote.origin.url")); id != "github.com/example/demo" {
		t.Fatalf("identity %q", id)
	}
	if url := w.git("ls-remote", "--get-url", "origin"); !strings.HasPrefix(url, "file://") {
		t.Fatalf("git must reach the bare repository, got %s", url)
	}
}

// Claude Code: a session from a pushed branch, a follow-up, the cloud's work on a
// claude/… branch, and teleports: whole, partial, empty and without a branch.
func TestClaudeCloudRoundTrip(t *testing.T) {
	w := newWorld(t)
	out, _, code := w.run("", claudeMain, "-p", "[hopsesh] fix the bug", "--cloud", "--output-format", "json")
	var created struct {
		OK        bool   `json:"ok"`
		SessionID string `json:"session_id"`
		URL       string `json:"url"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &created) != nil || !created.OK || !strings.HasPrefix(created.SessionID, "session_01") {
		t.Fatalf("create: %d %s", code, out)
	}
	id := created.SessionID
	if _, stderr, code := w.run("", claudeMain, "--cloud", id); code == 0 || !strings.Contains(stderr, "Attaching to an existing cloud session is not enabled") {
		t.Fatalf("--cloud <id> without -p must refuse: %d %s", code, stderr)
	}
	cse := "cse_" + strings.TrimPrefix(id, "session_")
	if out, _, code := w.run("", claudeMain, "-p", "and add a test", "--cloud", cse, "--output-format", "json"); code != 0 || !strings.Contains(out, id) {
		t.Fatalf("a follow-up by the cse_ form of the id: %d %s", code, out)
	}
	if _, stderr, code := w.run("archived", claudeMain, "-p", "more", "--cloud", id); code == 0 || !strings.Contains(stderr, "archived") {
		t.Fatalf("an archived session refuses follow-ups: %s", stderr)
	}
	must(t, Work(Proc{Vars: map[string]string{}}, id, false))
	s, _ := Open(w.store).Get(id)
	if s.State != StateIdle || !strings.HasPrefix(s.Result, "claude/web-session-") || len(s.Messages) != 3 {
		t.Fatalf("after work: %+v", s)
	}

	teleport := func(fail string) (string, []map[string]any) {
		t.Helper()
		w.git("checkout", "-q", "main")
		out, stderr, code := w.run(fail, claudeMain, "--teleport", id)
		if code != 0 {
			t.Fatalf("teleport (%s): %s", fail, stderr)
		}
		files, _ := filepath.Glob(filepath.Join(w.home, ".claude", "projects", "*", "*.jsonl"))
		var newest string
		var recs []map[string]any
		for _, f := range files {
			fi, _ := os.Stat(f)
			if newest == "" || fi.ModTime().After(mustStat(t, newest).ModTime()) {
				newest = f
			}
		}
		b, _ := os.ReadFile(newest)
		os.Remove(newest)
		for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			var r map[string]any
			must(t, json.Unmarshal([]byte(l), &r))
			recs = append(recs, r)
		}
		return out, recs
	}
	_, recs := teleport("")
	if recs[0]["type"] != "teleported-from" || recs[0]["messageCount"] != float64(3) || len(recs) != 4 {
		t.Fatalf("a whole teleport: %v", recs)
	}
	if w.git("rev-parse", "--abbrev-ref", "HEAD") != s.Result {
		t.Fatal("teleport checks out the cloud's branch")
	}
	if _, recs = teleport("partial"); recs[0]["messageCount"] != float64(3) || len(recs) != 2 {
		t.Fatalf("a partial teleport restores one message of three: %v", recs)
	}
	if _, recs = teleport("empty"); recs[0]["messageCount"] != float64(0) || len(recs) != 1 {
		t.Fatalf("an empty teleport: %v", recs)
	}
	if out, _ := teleport("no-branch"); !strings.Contains(out, "Failed to checkout branch") {
		t.Fatalf("no branch: %s", out)
	}
	must(t, os.WriteFile(filepath.Join(w.repo, "README.md"), []byte("dirty\n"), 0o600))
	if _, stderr, code := w.run("", claudeMain, "--teleport", id); code == 0 || !strings.Contains(stderr, "uncommitted") {
		t.Fatalf("teleport needs a clean tree: %s", stderr)
	}
	if _, stderr, code := w.run("signed-out", claudeMain, "--teleport", id); code == 0 || !strings.Contains(stderr, "organization UUID") {
		t.Fatalf("signed out: %s", stderr)
	}
}

func mustStat(t *testing.T, p string) os.FileInfo {
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

// Claude Code starts only from a pushed branch; a push the origin refuses fails the
// cloud's work.
func TestClaudeCloudNeedsAPushedBranch(t *testing.T) {
	w := newWorld(t)
	w.git("checkout", "-q", "-b", "local-only")
	if _, stderr, code := w.run("", claudeMain, "--cloud", "do it"); code == 0 || !strings.Contains(stderr, "push it first") {
		t.Fatalf("an unpushed branch: %d %s", code, stderr)
	}
	w.git("checkout", "-q", "main")
	out, _, code := w.run("", claudeMain, "--cloud", "do it")
	if code != 0 {
		t.Fatal(out)
	}
	id := strings.TrimSpace(out[strings.LastIndex(out, "/")+1:])
	err := Work(Proc{Vars: map[string]string{"FAKE_CLOUD_FAIL": "push-refused"}}, id, false)
	if err == nil || !strings.Contains(err.Error(), "branch protection") {
		t.Fatalf("a refused push: %v", err)
	}
	if s, _ := Open(w.store).Get(id); s.State != StateFailed {
		t.Fatalf("state %s", s.State)
	}
}

// Codex: exec from a pushed branch with an environment, list --json in the documented
// fields, the task's diff once it works, and codex apply.
func TestCodexCloud(t *testing.T) {
	w := newWorld(t)
	if _, stderr, code := w.run("", codexMain, "cloud", "exec", "do it"); code != 2 || !strings.Contains(stderr, "--env <ENV_ID>") {
		t.Fatalf("exec without --env: %d %s", code, stderr)
	}
	if _, stderr, code := w.run("no-env", codexMain, "cloud", "exec", "--env", "env_x", "do it"); code == 0 || !strings.Contains(stderr, "not found") {
		t.Fatalf("an unknown environment: %s", stderr)
	}
	if _, stderr, code := w.run("signed-out", codexMain, "cloud", "list", "--json"); code == 0 || !strings.Contains(stderr, "ChatGPT") {
		t.Fatalf("signed out: %s", stderr)
	}
	out, _, code := w.run("", codexMain, "cloud", "exec", "--env", "env_x", "--branch", "main", "[hopsesh] fix the bug")
	if code != 0 || !strings.Contains(out, "https://chatgpt.com/codex/tasks/task_e_") {
		t.Fatalf("exec: %d %s", code, out)
	}
	id := strings.TrimSpace(out[strings.LastIndex(out, "/")+1:])
	list := func(fail string) map[string]any {
		out, _, code := w.run(fail, codexMain, "cloud", "list", "--json", "--limit", "5")
		var l struct {
			Tasks []map[string]any `json:"tasks"`
		}
		if code != 0 || json.Unmarshal([]byte(out), &l) != nil || len(l.Tasks) == 0 {
			t.Fatalf("list: %s", out)
		}
		return l.Tasks[0]
	}
	task := list("")
	for _, f := range []string{"id", "url", "title", "status", "updated_at", "environment_id", "environment_label", "summary", "is_review", "attempt_total"} {
		if _, ok := task[f]; !ok {
			t.Errorf("list --json lacks %s", f)
		}
	}
	if task["status"] != "pending" {
		t.Fatalf("a task before work is pending: %v", task["status"])
	}
	if _, _, code := w.run("", codexMain, "cloud", "diff", id); code == 0 {
		t.Fatal("a pending task has no diff")
	}
	must(t, Work(Proc{Vars: map[string]string{}}, id, false))
	if list("")["status"] != "ready" {
		t.Fatal("a task after work is ready")
	}
	diff, _, code := w.run("", codexMain, "cloud", "diff", id)
	if code != 0 || !strings.Contains(diff, "cloud-work/"+id+".md") {
		t.Fatalf("diff: %s", diff)
	}
	if out, stderr, code := w.run("", codexMain, "apply", id); code != 0 || !strings.Contains(out, "applied") {
		t.Fatalf("apply: %s %s", out, stderr)
	}
	if _, err := os.Stat(filepath.Join(w.repo, "cloud-work", id+".md")); err != nil {
		t.Fatal("codex apply must change the working tree")
	}
	if _, _, code := w.run("", codexMain, "apply", id); code == 0 {
		t.Fatal("applying twice conflicts, and codex apply exits non-zero")
	}
	if list("")["status"] != "applied" {
		t.Fatal("an applied task says so")
	}
}

// The fake agent program works as fakecloud too: fakecloud work runs as a command.
func TestFakecloudProgram(t *testing.T) {
	w := newWorld(t)
	s, err := Open(w.store).Seed(Session{Cloud: FakeCloud, Repo: "github.com/example/demo", CloneURL: w.git("ls-remote", "--get-url", "origin"), Branch: "main"})
	must(t, err)
	var out, errOut bytes.Buffer
	if code := Main(Proc{Args: []string{"work", "--same-branch", s.ID}, Dir: w.repo, Stdout: &out, Stderr: &errOut}); code != 0 {
		t.Fatal(errOut.String())
	}
	w.git("pull", "-q", "origin", "main")
	if _, err := os.Stat(filepath.Join(w.repo, "cloud-work", s.ID+".md")); err != nil {
		t.Fatal("work on the same branch pushes to it")
	}
	b, _ := os.ReadFile(w.log)
	if !strings.Contains(string(b), "fakecloud work --same-branch "+s.ID) {
		t.Fatalf("the call is logged: %s", b)
	}
}
