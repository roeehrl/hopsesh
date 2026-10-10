package fakecloud

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/internal/core/term"
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
	dir := t.TempDir()
	w := &world{t: t, home: filepath.Join(dir, "home"), store: filepath.Join(dir, "cloud"), log: filepath.Join(dir, "agents.log")}
	w.repo = filepath.Join(w.home, "git", "demo")
	gitConfig := filepath.Join(dir, "gitconfig")
	w.vars = map[string]string{"HOME": w.home, "USERPROFILE": w.home, "FAKE_CLOUD_DIR": w.store, "FAKE_AGENT_LOG": w.log, "GIT_CONFIG_GLOBAL": gitConfig,
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

// tty runs a stand-in program as in a terminal 100 columns wide, whose user types typed.
func (w *world) tty(fail, typed string, main func(Proc) int, args ...string) (string, int) {
	var out bytes.Buffer
	vars := map[string]string{}
	if fail != "" {
		vars["FAKE_CLOUD_FAIL"] = fail
	}
	p := Proc{Args: args, Vars: vars, Dir: w.repo, Stdout: &out, Stderr: &out, TTY: true, Width: 100}
	if typed != "" {
		p.In = strings.NewReader(typed)
	}
	code := main(p)
	return term.Plain(out.Bytes()), code
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

// Git invokes the fixture hook itself, including Git for Windows' bundled
// shell. Missing/broken hook execution must fail qualification, never skip it.
func TestOriginRefusedPushPreservesRemoteRef(t *testing.T) {
	w := newWorld(t)
	before := w.git("ls-remote", "origin", "refs/heads/main")
	w.git("commit", "-q", "--allow-empty", "-m", "new work")
	t.Setenv("FAKE_CLOUD_FAIL", "push-refused")
	_, err := git(nil, w.repo, "push", "origin", "main")
	if err == nil || !strings.Contains(err.Error(), "refused: branch protection (fake)") {
		t.Fatal("fixture hook did not explicitly refuse the push", err)
	}
	if after := w.git("ls-remote", "origin", "refs/heads/main"); after != before {
		t.Fatal("refused push changed the remote branch")
	}
	t.Setenv("FAKE_CLOUD_FAIL", "")
	w.git("push", "-q", "origin", "main")
	if after := w.git("ls-remote", "origin", "refs/heads/main"); after == before || !strings.HasPrefix(after, w.git("rev-parse", "HEAD")) {
		t.Fatal("approved retry did not publish the exact local commit")
	}
}

// Claude Code: a session from a pushed branch, a follow-up, the cloud's work on a
// claude/… branch, and teleports: whole, partial, empty and without a branch.
func TestClaudeCloudRoundTrip(t *testing.T) {
	w := newWorld(t)
	// As 2.1.284: no --print with --cloud, and no --cloud without a terminal.
	if _, stderr, code := w.run("", claudeMain, "-p", "[hopsesh] fix the bug", "--cloud", "--output-format", "json"); code != 1 ||
		!strings.Contains(stderr, "Error: --cloud cannot be combined with --print.") {
		t.Fatalf("-p with --cloud: %d %s", code, stderr)
	}
	if _, stderr, code := w.run("", claudeMain, "--cloud", "[hopsesh] fix the bug"); code != 1 || !strings.Contains(stderr, "Error: --cloud requires an interactive terminal.") {
		t.Fatalf("--cloud without a terminal: %d %s", code, stderr)
	}
	out, code := w.tty("", "", claudeMain, "--cloud", "[hopsesh] fix the bug")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if code != 0 || len(lines) != 3 || lines[0] != "Created cloud session: Session ready" || !strings.HasPrefix(lines[1], "View: https://claude.ai/code/session_01") ||
		!strings.HasSuffix(lines[1], "?from=cli&m=0") || !strings.HasPrefix(lines[2], "Resume with: claude --teleport session_01") {
		t.Fatalf("create: %d %q", code, out)
	}
	id := strings.TrimPrefix(lines[2], "Resume with: claude --teleport ")
	if lines[1] != "View: https://claude.ai/code/"+id+"?from=cli&m=0" {
		t.Fatalf("the two lines name one session: %q", out)
	}
	if out, code := w.tty("", "", claudeMain, "--cloud", id); code == 0 || !strings.Contains(out, "Attaching to an existing cloud session is not enabled") {
		t.Fatalf("--cloud <id> must refuse: %d %s", code, out)
	}
	if _, stderr, code := w.run("", claudeMain, "-p", "and add a test", "--cloud", "cse_"+strings.TrimPrefix(id, "session_")); code != 1 || !strings.Contains(stderr, "--print") {
		t.Fatalf("a follow-up with -p is refused: %d %s", code, stderr)
	}
	// The folder's trust: asked in a folder not on the list; yes adds it, no ends there.
	trusted := filepath.Join(t.TempDir(), "trusted")
	t.Setenv("FAKE_CLAUDE_TRUSTED", trusted)
	if out, code := w.tty("", "2", claudeMain, "--cloud", "[hopsesh] x"); code != 1 || !strings.Contains(out, "Quick safety check: Is this a project you created or one you trust?") ||
		strings.Contains(out, "Created cloud session") {
		t.Fatalf("trust refused: %d %q", code, out)
	}
	if out, code := w.tty("", "\x03", claudeMain, "--cloud", "[hopsesh] x"); code != 130 || strings.Contains(out, "Created cloud session") {
		t.Fatalf("Ctrl-C at the question: %d %q", code, out)
	}
	if out, code := w.tty("", "\r", claudeMain, "--cloud", "[hopsesh] x"); code != 0 || !strings.Contains(out, "Quick safety check") || !strings.Contains(out, "Created cloud session") {
		t.Fatalf("trusted: %d %q", code, out)
	}
	if out, code := w.tty("", "", claudeMain, "--cloud", "[hopsesh] x"); code != 0 || strings.Contains(out, "Quick safety check") {
		t.Fatalf("asked again in a trusted folder: %d %q", code, out)
	}
	if out, code := w.tty("trust-no", "", claudeMain, "--cloud", "[hopsesh] x"); code != 1 || !strings.Contains(out, "Quick safety check") {
		t.Fatalf("a user who says no: %d %q", code, out)
	}
	t.Setenv("FAKE_CLAUDE_TRUSTED", "")
	must(t, Work(Proc{Vars: map[string]string{}}, id, false))
	s, _ := Open(w.store).Get(id)
	if s.State != StateIdle || !strings.HasPrefix(s.Result, "claude/web-session-") || len(s.Messages) != 2 {
		t.Fatalf("after work: %+v", s)
	}

	copies := func() []string {
		files, _ := filepath.Glob(filepath.Join(w.home, ".claude", "projects", "*", "*.jsonl"))
		return files
	}
	// As 2.1.289: the teleport writes nothing until the user sends a message.
	t.Setenv("FAKE_CLAUDE_SAYS", "")
	if out, _, code := w.run("", claudeMain, "--teleport", id); code != 0 || len(copies()) != 0 || !strings.Contains(out, "no local copy saved") {
		t.Fatalf("a teleport without a message saves nothing: %d %v %s", code, copies(), out)
	}
	t.Setenv("FAKE_CLAUDE_SAYS", "ok")
	teleport := func(fail string) (string, []map[string]any) {
		t.Helper()
		w.git("checkout", "-q", "main")
		out, stderr, code := w.run(fail, claudeMain, "--teleport", id)
		if code != 0 {
			t.Fatalf("teleport (%s): %s", fail, stderr)
		}
		files := copies()
		if len(files) != 1 {
			t.Fatalf("teleport (%s) wrote %v", fail, files)
		}
		b, _ := os.ReadFile(files[0])
		os.Remove(files[0])
		if strings.Contains(string(b), `"sessionId":"`+id) || strings.Contains(string(b), "teleported-from") || strings.Contains(string(b), "remoteSessionId") {
			t.Fatalf("the copy names the cloud session: %s", b)
		}
		var recs []map[string]any
		for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			var r map[string]any
			must(t, json.Unmarshal([]byte(l), &r))
			recs = append(recs, r)
		}
		return out, recs
	}
	// The cloud's messages, the "continued from another machine" record, then the new turn.
	shape := func(recs []map[string]any) string {
		var kinds []string
		for _, r := range recs {
			k := r["type"].(string)
			if r["isMeta"] == true {
				k = "meta"
			}
			if st, ok := r["subtype"].(string); ok {
				k += ":" + st
			}
			kinds = append(kinds, k)
		}
		return strings.Join(kinds, ",")
	}
	_, recs := teleport("")
	if got := shape(recs); got != "user,assistant,meta,user,assistant,system:turn_duration" {
		t.Fatalf("a whole teleport: %s", got)
	}
	if w.git("rev-parse", "--abbrev-ref", "HEAD") != s.Result {
		t.Fatal("teleport checks out the cloud's branch")
	}
	if _, recs = teleport("partial"); shape(recs) != "user,meta,user,assistant,system:turn_duration" {
		t.Fatalf("a partial teleport restores one message of two: %s", shape(recs))
	}
	if _, recs = teleport("empty"); shape(recs) != "meta,user,assistant,system:turn_duration" {
		t.Fatalf("an empty teleport: %s", shape(recs))
	}
	if out, recs := teleport("no-branch"); !strings.Contains(out, "Failed to checkout branch") || shape(recs) != "user,assistant,meta,system:informational,user,assistant,system:turn_duration" {
		t.Fatalf("no branch: %s %s", out, shape(recs))
	}
	must(t, os.WriteFile(filepath.Join(w.repo, "README.md"), []byte("dirty\n"), 0o600))
	if _, stderr, code := w.run("", claudeMain, "--teleport", id); code == 0 || !strings.Contains(stderr, "uncommitted") {
		t.Fatalf("teleport needs a clean tree: %s", stderr)
	}
	if _, stderr, code := w.run("signed-out", claudeMain, "--teleport", id); code == 0 || !strings.Contains(stderr, "organization UUID") {
		t.Fatalf("signed out: %s", stderr)
	}
}

// Claude Code starts only from a pushed branch; a push the origin refuses fails the
// cloud's work.
func TestClaudeCloudNeedsAPushedBranch(t *testing.T) {
	w := newWorld(t)
	w.git("checkout", "-q", "-b", "local-only")
	if out, code := w.tty("", "", claudeMain, "--cloud", "do it"); code == 0 || !strings.Contains(out, "push it first") {
		t.Fatalf("an unpushed branch: %d %s", code, out)
	}
	w.git("checkout", "-q", "main")
	out, code := w.tty("", "", claudeMain, "--cloud", "do it")
	if code != 0 {
		t.Fatal(out)
	}
	id := strings.TrimSpace(out[strings.LastIndex(out, " ")+1:])
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
