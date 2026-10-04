package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/internal/testkit/fakeagent"
	"github.com/roeehrl/hopsesh/internal/testkit/fakecloud"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// The test binary is the stand-in claude when it runs under that name.
func TestMain(m *testing.M) {
	if strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") == "claude" {
		os.Exit(fakeagent.Claude())
	}
	os.Exit(m.Run())
}

// cloudWorld is this machine with the stand-in claude on PATH, a demo repository whose
// GitHub remote is a local bare repository, and a Claude Code cloud session that pushed
// its work to a claude/… branch.
type cloudWorld struct {
	t                     *testing.T
	home, repo, store, wd string
	session               fakecloud.Session
}

func newCloudWorld(t *testing.T, work bool) *cloudWorld {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in origin's hook and the teleport's shell line assume a POSIX system")
	}
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	w := &cloudWorld{t: t, home: filepath.Join(root, "home"), store: filepath.Join(root, "cloud")}
	w.repo = filepath.Join(w.home, "git", "demo")
	bin := filepath.Join(root, "bin")
	self, _ := os.Executable()
	for _, d := range []string{bin, w.repo, filepath.Join(w.home, ".claude", "projects")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(self, filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	gitConfig := filepath.Join(root, "gitconfig")
	for k, v := range map[string]string{"HOME": w.home, "USERPROFILE": w.home, "PATH": bin + ":" + testPath(), "HOPSESH_MACHINE": "here",
		"HOPSESH_CONFIG_DIR": filepath.Join(w.home, "config"), "HOPSESH_STATE_DIR": filepath.Join(w.home, "state"), "HOPSESH_TAILSCALE": "off",
		"CLAUDE_CONFIG_DIR": "", "CODEX_HOME": "", "FAKE_CLOUD_DIR": w.store, "FAKE_CLOUD_FAIL": "", "FAKE_AGENT_LOG": filepath.Join(root, "agents.log"),
		"GIT_CONFIG_GLOBAL": gitConfig, "GIT_CONFIG_NOSYSTEM": "1", "GIT_AUTHOR_NAME": "Sam Doe", "GIT_AUTHOR_EMAIL": "sam@example.com",
		"GIT_COMMITTER_NAME": "Sam Doe", "GIT_COMMITTER_EMAIL": "sam@example.com",
		// As inside another agent's session: the teleport must run without these.
		"CLAUDE_CODE_CHILD_SESSION": "1", "ANTHROPIC_API_KEY": "sk-ant-not-a-real-key"} {
		t.Setenv(k, v)
	}
	o, err := fakecloud.NewOrigin(root, "https://github.com/example/demo.git")
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Redirect(gitConfig); err != nil {
		t.Fatal(err)
	}
	w.git(w.repo, "init", "-q", "-b", "main")
	w.git(w.repo, "remote", "add", "origin", o.URL)
	w.git(w.repo, "commit", "-q", "--allow-empty", "-m", "init")
	w.git(w.repo, "push", "-q", "origin", "main")
	w.session, err = fakecloud.Open(w.store).Seed(fakecloud.Session{Cloud: fakecloud.ClaudeCloud, Title: "Add rate limiting", Repo: "github.com/example/demo",
		CloneURL: o.FileURL(), Branch: "main", Base: w.git(w.repo, "rev-parse", "HEAD"), Code: "branch",
		Messages: []fakecloud.Message{{Role: "user", Text: "[hopsesh] add rate limiting to search"}, {Role: "assistant", Text: "On it."}}})
	if err != nil {
		t.Fatal(err)
	}
	if work {
		if err := fakecloud.Work(fakecloud.Proc{Vars: map[string]string{}}, w.session.ID, false); err != nil {
			t.Fatal(err)
		}
		w.session, _ = fakecloud.Open(w.store).Get(w.session.ID)
	}
	return w
}

func (w *cloudWorld) git(dir string, args ...string) string {
	w.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		w.t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (w *cloudWorld) app() *app.App {
	w.t.Helper()
	cfg, err := config.Load()
	if err != nil {
		w.t.Fatal(err)
	}
	cfg.SetCloudAllowed("claude-cloud", true)
	return app.New(cfg, all.Registry(), config.StateDir(), nil)
}

// plan pastes the session's link with the demo checkout and plans bringing it here.
func (w *cloudWorld) plan(a *app.App, checkout string, opt move.Options) (*app.Inventory, *move.Plan) {
	w.t.Helper()
	ctx := context.Background()
	if _, err := a.Paste(ctx, "https://claude.ai/code/"+w.session.ID, checkout); err != nil {
		w.t.Fatal(err)
	}
	inv := a.Scan(ctx, app.ScanOptions{})
	if c := inv.Cloud("claude-cloud"); c == nil || c.Status != app.CloudReady {
		return inv, nil
	}
	e, err := inv.CloudEntry(a, "claude-cloud", agent.SessionID(w.session.ID))
	if err != nil {
		w.t.Fatal(err)
	}
	p, _, err := a.Plan(ctx, inv, e, "", opt)
	if err != nil {
		w.t.Fatal(err)
	}
	return inv, p
}

// terminal runs the plan's command line as the user's terminal would.
func (w *cloudWorld) terminal(line, fail string) string {
	w.t.Helper()
	cmd := exec.Command("sh", "-c", line)
	cmd.Env = append(os.Environ(), "FAKE_CLOUD_FAIL="+fail)
	out, _ := cmd.CombinedOutput()
	return string(out)
}

// The whole round trip: a pasted link, the plan, a worktree, the teleport in the user's
// terminal (without the variables an agent's own session would pass on), the adoption with
// its count, the cloud's branch renamed, lineage, and an undo that takes it all away.
func TestBringFromClaudeCloud(t *testing.T) {
	w := newCloudWorld(t, true)
	a := w.app()
	ctx := context.Background()
	inv, p := w.plan(a, w.repo, move.Options{})
	defer inv.Close()
	if p == nil || p.Kind != move.KindFetch || len(p.Blockers) > 0 {
		t.Fatalf("plan: %+v", p)
	}
	fp := p.Fetch
	if fp.BranchState != move.BranchUnknown || fp.Repo != "github.com/example/demo" || !strings.Contains(fp.Command, "env -u CLAUDE_CODE_CHILD_SESSION -u ANTHROPIC_API_KEY claude --teleport "+w.session.ID) ||
		fp.Worktree != filepath.Join(w.home, "git", "demo-cloud-"+strings.ToLower(w.session.ID[len(w.session.ID)-6:])) || !fp.Rename {
		t.Fatalf("fetch plan: %+v", fp)
	}
	res, err := a.Apply(ctx, p, move.Input{}, nil)
	if err != nil || res.Fetch == nil || res.Fetch.Outcome != move.FetchWaiting {
		t.Fatalf("apply: %+v %v", res, err)
	}
	if b := repos.CurrentBranch(ctx, w.repo); b != "main" {
		t.Fatalf("the user's checkout moved to %s", b)
	}
	f, err := a.Adopt(ctx, res.Journal, false)
	if err != nil || !f.Waiting() {
		t.Fatalf("nothing to adopt before the teleport: %+v %v", f, err)
	}
	if out := w.terminal(res.Command, ""); !strings.Contains(out, "Teleported") {
		t.Fatalf("teleport: %s", out)
	}
	f, err = a.Adopt(ctx, res.Journal, true)
	if err != nil || f.Waiting() {
		t.Fatalf("adopt: %+v %v", f, err)
	}
	b := a.Brought(f)
	branch := "hopsesh/from/claude-cloud/" + strings.TrimPrefix(w.session.Result, "claude/")
	if b.Outcome != move.FetchComplete || b.Restored != 3 || b.Expected != 3 || b.Branch != branch || b.Renamed != w.session.Result ||
		!strings.Contains(b.Command, "claude --resume") || !strings.Contains(b.Message, "3 of 3 messages") {
		t.Fatalf("brought: %+v", b)
	}
	if got := repos.CurrentBranch(ctx, fp.Worktree); got != branch {
		t.Fatalf("the worktree is on %s", got)
	}
	// The next scan lists the copy with its lineage, and the cloud session by it.
	inv2 := a.Scan(ctx, app.ScanOptions{})
	defer inv2.Close()
	var local, cloud *app.Entry
	for i, e := range inv2.Entries {
		if string(e.Session.Key.String()) == b.Key {
			local = &inv2.Entries[i]
		}
		if e.Location.IsCloud() && string(e.Session.Key.Session) == w.session.ID {
			cloud = &inv2.Entries[i]
		}
	}
	if local == nil || local.Lineage == nil || len(local.Lineage.Hops) != 1 || local.Lineage.Hops[0].Kind != "fetch" {
		t.Fatalf("the copy here: %+v", local)
	}
	if cloud == nil || cloud.Cloud.Branch != w.session.Result || cloud.Checkout == "" {
		t.Fatalf("the cloud session: %+v", cloud)
	}
	if _, err := a.Undo(ctx, res.Journal, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fp.Worktree); !os.IsNotExist(err) {
		t.Fatal("undo leaves the worktree")
	}
	if repos.BranchExists(ctx, w.repo, branch) || repos.BranchExists(ctx, w.repo, w.session.Result) {
		t.Fatal("undo leaves the branch")
	}
	if _, err := os.Stat(f.Adopted.Path); !os.IsNotExist(err) {
		t.Fatal("undo leaves the teleported copy")
	}
}

// The teleport's outcomes, as the stand-in cloud plays them, and the refusals a plan makes.
func TestBringOutcomes(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name, fail string
		work       bool
		outcome    string
		message    string
	}{
		{"partial", "partial", true, move.FetchPartial, "restored 1 of 3 messages. This is a known Claude Code problem (#94836)."},
		{"empty", "empty", true, move.FetchEmpty, "copied none of the session's messages. This is a known Claude Code problem (#95873)."},
		{"no-branch", "no-branch", false, move.FetchComplete, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newCloudWorld(t, c.work)
			a := w.app()
			inv, p := w.plan(a, w.repo, move.Options{})
			defer inv.Close()
			if p == nil || len(p.Blockers) > 0 {
				t.Fatalf("plan: %+v", p)
			}
			res, err := a.Apply(ctx, p, move.Input{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			w.terminal(res.Command, c.fail)
			f, err := a.Adopt(ctx, res.Journal, true)
			if err != nil || f.Waiting() {
				t.Fatalf("adopt: %+v %v", f, err)
			}
			b := a.Brought(f)
			if b.Outcome != c.outcome || !strings.Contains(b.Message, c.message) {
				t.Fatalf("brought: %+v", b)
			}
			if c.name == "no-branch" && (!b.NoBranch || b.Branch != "") {
				t.Fatalf("no branch: %+v", b)
			}
		})
	}
	t.Run("signed-out", func(t *testing.T) {
		w := newCloudWorld(t, true)
		t.Setenv("FAKE_CLOUD_FAIL", "signed-out")
		a := w.app()
		inv, _ := w.plan(a, w.repo, move.Options{})
		defer inv.Close()
		c := inv.Cloud("claude-cloud")
		if c.Status != app.CloudSignedOut || !strings.Contains(c.Error, "API key") {
			t.Fatalf("cloud: %+v", c)
		}
		e, _ := inv.CloudEntry(a, "claude-cloud", agent.SessionID(w.session.ID))
		p, _, err := a.Plan(ctx, inv, e, "", move.Options{TargetDir: w.repo})
		if err != nil || len(p.Blockers) != 1 || !strings.Contains(p.Blockers[0], "Cloud sessions need a claude.ai login") {
			t.Fatalf("plan: %+v %v", p, err)
		}
	})
	t.Run("repo-mismatch", func(t *testing.T) {
		w := newCloudWorld(t, true)
		other := filepath.Join(w.home, "git", "other")
		if err := os.MkdirAll(other, 0o700); err != nil {
			t.Fatal(err)
		}
		w.git(other, "init", "-q", "-b", "main")
		w.git(other, "remote", "add", "origin", "https://github.com/example/other.git")
		w.git(other, "commit", "-q", "--allow-empty", "-m", "init")
		a := w.app()
		inv, _ := w.plan(a, w.repo, move.Options{})
		defer inv.Close()
		e, _ := inv.CloudEntry(a, "claude-cloud", agent.SessionID(w.session.ID))
		p, _, err := a.Plan(ctx, inv, e, "", move.Options{TargetDir: other})
		if err != nil || len(p.Blockers) != 1 || !strings.Contains(p.Blockers[0], "This checkout is github.com/example/other, but the cloud session works on github.com/example/demo") {
			t.Fatalf("plan: %+v %v", p, err)
		}
	})
}

// The code only: the cloud's branch is fetched and checked out in a worktree under its
// hopsesh name, with no teleport.
func TestBringCodeOnly(t *testing.T) {
	w := newCloudWorld(t, true)
	a := w.app()
	ctx := context.Background()
	if _, err := a.Paste(ctx, w.session.ID, w.repo); err != nil {
		t.Fatal(err)
	}
	inv := a.Scan(ctx, app.ScanOptions{})
	defer inv.Close()
	e, _ := inv.CloudEntry(a, "claude-cloud", agent.SessionID(w.session.ID))
	e.Cloud.Branch = w.session.Result // as a hand-off or an earlier fetch records it
	p, _, err := a.Plan(ctx, inv, e, "", move.Options{CodeOnly: true})
	if err != nil || len(p.Blockers) > 0 || p.Fetch.BranchState != move.BranchPushed {
		t.Fatalf("plan: %+v %v", p, err)
	}
	res, err := a.Apply(ctx, p, move.Input{}, nil)
	if err != nil || res.Fetch.Outcome != move.FetchCode {
		t.Fatalf("apply: %+v %v", res, err)
	}
	if got := repos.CurrentBranch(ctx, p.Fetch.Worktree); got != p.Fetch.LocalBranch || !strings.HasPrefix(got, "hopsesh/from/claude-cloud/") {
		t.Fatalf("the worktree is on %s", got)
	}
	if _, err := os.Stat(filepath.Join(p.Fetch.Worktree, "cloud-work", w.session.ID+".md")); err != nil {
		t.Fatal("the cloud's work is not in the worktree")
	}
	// The cloud works on; the code again moves the same branch forward (its first worktree
	// gone, the branch kept), and undo moves it back.
	first, branch := p.Fetch.Worktree, p.Fetch.LocalBranch
	before := w.git(w.repo, "rev-parse", branch)
	w.git(w.repo, "worktree", "remove", first)
	s, _ := fakecloud.Open(w.store).Get(w.session.ID)
	s.Branch = s.Result
	if err := fakecloud.Open(w.store).Put(s); err != nil {
		t.Fatal(err)
	}
	if err := fakecloud.Work(fakecloud.Proc{Vars: map[string]string{}}, w.session.ID, true); err != nil {
		t.Fatal(err)
	}
	p2, _, err := a.Plan(ctx, inv, e, "", move.Options{CodeOnly: true})
	if err != nil || len(p2.Blockers) > 0 || !p2.Fetch.FastForward || p2.Fetch.LocalBranch != branch {
		t.Fatalf("again: %+v %v", p2.Fetch, err)
	}
	res2, err := a.Apply(ctx, p2, move.Input{}, nil)
	if err != nil || !res2.Fetch.FastForwarded || res2.Fetch.Branch != branch {
		t.Fatalf("again: %+v %v", res2.Fetch, err)
	}
	if now := w.git(w.repo, "rev-parse", branch); now == before || now != res2.Fetch.Base {
		t.Fatalf("%s did not move forward: %s → %s", branch, before, now)
	}
	if _, err := a.Undo(ctx, res2.Journal, false); err != nil {
		t.Fatal(err)
	}
	if now := w.git(w.repo, "rev-parse", branch); now != before {
		t.Fatalf("undo leaves %s at %s", branch, now)
	}
	if _, err := a.Undo(ctx, res.Journal, false); err != nil {
		t.Fatal(err)
	}
	if repos.BranchExists(ctx, w.repo, branch) {
		t.Fatal("undo leaves the branch")
	}
}
