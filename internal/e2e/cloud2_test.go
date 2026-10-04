package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/internal/testkit/fakecloud"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Second-wave clouds (cloud-only modules) come home through the same fetch plan, code
// first: Copilot's pull request branch, and Jules's patch committed on a branch of its own.

// vendorWorld is a cloud world with the stand-in vendor CLIs on PATH and a session of
// cloud that worked on the demo repository.
func vendorWorld(t *testing.T, cloud, title string, msgs ...fakecloud.Message) (*cloudWorld, fakecloud.Session) {
	t.Helper()
	w := newCloudWorld(t, false)
	self, _ := os.Executable()
	for _, name := range []string{"gh", "jules", "devin", "amp"} {
		if err := os.Symlink(self, filepath.Join(w.bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	st := fakecloud.Open(w.store)
	s, err := st.Seed(fakecloud.Session{Cloud: cloud, Title: title, Repo: "github.com/example/demo", CloneURL: w.session.CloneURL, Branch: "main",
		Base: w.git(w.repo, "rev-parse", "HEAD"), Code: "branch", Messages: msgs})
	if err != nil {
		t.Fatal(err)
	}
	if err := fakecloud.Work(fakecloud.Proc{Vars: map[string]string{}}, s.ID, false); err != nil {
		t.Fatal(err)
	}
	s, _ = st.Get(s.ID)
	return w, s
}

func allowed(t *testing.T, clouds ...string) *app.App {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range clouds {
		cfg.SetCloudAllowed(c, true)
	}
	return app.New(cfg, all.Registry(), config.StateDir(), nil)
}

func TestBringFromCopilot(t *testing.T) {
	w, s := vendorWorld(t, fakecloud.CopilotCloud, "Add rate limiting to search",
		fakecloud.Message{Role: "user", Text: "Add rate limiting to search"})
	a := allowed(t, "copilot-cloud")
	ctx := context.Background()
	inv := a.Scan(ctx, app.ScanOptions{})
	defer inv.Close()
	c := inv.Cloud("copilot-cloud")
	if c == nil || c.Status != app.CloudReady || c.Sessions != 1 || c.Partial || !c.Fetchable || !strings.HasPrefix(c.Version, "2.97") {
		t.Fatalf("cloud: %+v", c)
	}
	for _, m := range inv.Machines {
		for _, st := range m.Agents {
			if st.Agent == "copilot" && st.Install.Present {
				t.Errorf("a cloud-only module has data on %s", m.Name)
			}
		}
	}
	e, err := inv.CloudEntry(a, "copilot-cloud", agent.SessionID(s.ID))
	if err != nil || e.Cloud == nil || e.Cloud.Branch != s.Result || e.Cloud.PR == "" || e.Session.Title != s.Title ||
		e.Cloud.URL != "https://github.com/example/demo/pull/100/agent-sessions/"+s.ID {
		t.Fatalf("entry: %+v %+v %v", e, e.Cloud, err)
	}

	// With the conversation, the session log is written into a local agent: Claude Code
	// unless the user picks another (TestHandoffToCopilotAndBack applies it).
	p, _, err := a.Plan(ctx, inv, e, "", move.Options{TargetDir: w.repo})
	if err != nil || len(p.Blockers) > 0 || !p.Fetch.Write || p.Fetch.Writer != "Claude Code" || p.Fetch.ContinueIn != "claude" || p.Fetch.Fidelity != agent.FidText {
		t.Fatalf("plan with the conversation: %+v %v", p, err)
	}

	head := w.git(w.repo, "rev-parse", "HEAD")
	p, _, err = a.Plan(ctx, inv, e, "", move.Options{CodeOnly: true, TargetDir: w.repo})
	if err != nil || len(p.Blockers) > 0 || p.Fetch.BranchState != move.BranchPushed || p.Fetch.LocalBranch != "hopsesh/from/copilot-cloud/add-rate-limiting-to-search" {
		t.Fatalf("plan: %+v %+v %v", p, p.Fetch, err)
	}
	res, err := a.Apply(ctx, p, move.Input{}, nil)
	if err != nil || res.Fetch.Outcome != move.FetchCode || res.Fetch.Branch != p.Fetch.LocalBranch {
		t.Fatalf("apply: %+v %v", res, err)
	}
	if got := repos.CurrentBranch(ctx, p.Fetch.Worktree); got != p.Fetch.LocalBranch {
		t.Fatalf("the worktree is on %s", got)
	}
	if _, err := os.Stat(filepath.Join(p.Fetch.Worktree, "cloud-work", s.ID+".md")); err != nil {
		t.Fatal("Copilot's work is not in the worktree")
	}
	if now := w.git(w.repo, "rev-parse", "HEAD"); now != head || repos.CurrentBranch(ctx, w.repo) != "main" {
		t.Fatal("the user's checkout moved")
	}
	if _, err := a.Undo(ctx, res.Journal, false); err != nil {
		t.Fatal(err)
	}
	if repos.BranchExists(ctx, w.repo, p.Fetch.LocalBranch) {
		t.Fatal("undo leaves the branch")
	}
	if _, err := os.Stat(p.Fetch.Worktree); !os.IsNotExist(err) {
		t.Fatal("undo leaves the worktree")
	}
	log, _ := os.ReadFile(filepath.Join(filepath.Dir(w.store), "agents.log"))
	for _, want := range []string{"gh agent-task list --json", "gh pr list --repo example/demo"} {
		if !strings.Contains(string(log), want) {
			t.Errorf("the log has no %q:\n%s", want, log)
		}
	}
	if strings.Contains(string(log), "auth token") || strings.Contains(string(log), "--show-token") {
		t.Error("hopsesh asked gh for a token")
	}
}

func TestBringFromJules(t *testing.T) {
	w, s := vendorWorld(t, fakecloud.JulesCloud, "Write unit tests")
	a := allowed(t, "jules")
	ctx := context.Background()
	inv := a.Scan(ctx, app.ScanOptions{})
	defer inv.Close()
	if c := inv.Cloud("jules"); c == nil || c.Status != app.CloudReady || c.Sessions != 1 {
		t.Fatalf("cloud: %+v", c)
	}
	e, err := inv.CloudEntry(a, "jules", agent.SessionID(s.ID))
	if err != nil || e.Cloud == nil || e.Cloud.Branch != "" || e.Cloud.State != agent.CloudDone {
		t.Fatalf("entry: %+v %v", e.Cloud, err)
	}
	head := w.git(w.repo, "rev-parse", "HEAD")
	p, _, err := a.Plan(ctx, inv, e, "", move.Options{CodeOnly: true, TargetDir: w.repo})
	if err != nil || len(p.Blockers) > 0 || !p.Fetch.Diff || p.Fetch.LocalBranch != "hopsesh/from/jules/"+s.ID || p.Fetch.Base != head {
		t.Fatalf("plan: %+v %v", p.Fetch, err)
	}
	res, err := a.Apply(ctx, p, move.Input{}, nil)
	if err != nil || res.Fetch.Outcome != move.FetchCode || res.Fetch.Branch != p.Fetch.LocalBranch {
		t.Fatalf("apply: %+v %v", res, err)
	}
	wt := p.Fetch.Worktree
	if got := repos.CurrentBranch(ctx, wt); got != p.Fetch.LocalBranch {
		t.Fatalf("the worktree is on %s", got)
	}
	if _, err := os.Stat(filepath.Join(wt, "cloud-work", s.ID+".md")); err != nil {
		t.Fatal("Jules's patch is not in the worktree")
	}
	if msg := w.git(wt, "log", "-1", "--format=%s %P"); msg != "Jules session "+s.ID+" "+head {
		t.Fatalf("the patch's commit: %q", msg)
	}
	if out := w.git(wt, "status", "--porcelain"); out != "" {
		t.Fatalf("the worktree is not clean: %s", out)
	}
	if now := w.git(w.repo, "rev-parse", "HEAD"); now != head || repos.CurrentBranch(ctx, w.repo) != "main" {
		t.Fatal("the user's checkout moved")
	}
	if _, err := a.Undo(ctx, res.Journal, false); err != nil {
		t.Fatal(err)
	}
	if repos.BranchExists(ctx, w.repo, p.Fetch.LocalBranch) {
		t.Fatal("undo leaves the branch")
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("undo leaves the worktree")
	}

	// A patch that does not apply here fails before any branch is made; undo takes the
	// worktree away.
	st := fakecloud.Open(w.store)
	s.Diff = "diff --git a/nope.txt b/nope.txt\n--- a/nope.txt\n+++ b/nope.txt\n@@ -1 +1 @@\n-old\n+new\n"
	if err := st.Put(s); err != nil {
		t.Fatal(err)
	}
	p, _, err = a.Plan(ctx, inv, e, "", move.Options{CodeOnly: true, TargetDir: w.repo})
	if err != nil || len(p.Blockers) > 0 {
		t.Fatalf("plan: %+v %v", p, err)
	}
	res, err = a.Apply(ctx, p, move.Input{}, nil)
	if err == nil || !strings.Contains(err.Error(), "does not apply") {
		t.Fatalf("a patch that does not apply: %+v %v", res, err)
	}
	if repos.BranchExists(ctx, w.repo, p.Fetch.LocalBranch) {
		t.Fatal("a branch was made for a patch that did not apply")
	}
	if _, err := a.Undo(ctx, res.Journal, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.Fetch.Worktree); !os.IsNotExist(err) {
		t.Fatal("undo leaves the failed fetch's worktree")
	}
}

// Devin and Amp list through the same scan; Devin's code comes as its pull request's
// branch, Amp's threads have no code to bring yet (their text does: see
// TestHandoffToAmpAndBack).
func TestListDevinAndAmp(t *testing.T) {
	w, d := vendorWorld(t, fakecloud.DevinCloud, "Upgrade the router")
	if _, err := fakecloud.Open(w.store).Seed(fakecloud.Session{Cloud: fakecloud.AmpCloud, Title: "Speed up the importer",
		Messages: []fakecloud.Message{{Role: "user", Text: "Speed up the importer"}}}); err != nil {
		t.Fatal(err)
	}
	a := allowed(t, "devin", "amp")
	ctx := context.Background()
	inv := a.Scan(ctx, app.ScanOptions{})
	defer inv.Close()
	for _, name := range []string{"devin", "amp"} {
		if c := inv.Cloud(name); c == nil || c.Status != app.CloudReady || c.Sessions != 1 {
			t.Fatalf("%s: %+v", name, c)
		}
	}
	e, err := inv.CloudEntry(a, "devin", agent.SessionID(d.ID))
	if err != nil || e.Cloud.Branch != d.Result {
		t.Fatalf("devin entry: %+v %v", e.Cloud, err)
	}
	p, _, err := a.Plan(ctx, inv, e, "", move.Options{CodeOnly: true, TargetDir: w.repo})
	if err != nil || len(p.Blockers) > 0 || !strings.HasPrefix(p.Fetch.LocalBranch, "hopsesh/from/devin/") {
		t.Fatalf("devin plan: %+v %v", p.Fetch, err)
	}
	for _, x := range inv.Entries {
		if x.Location.Name == "amp" {
			p, _, err := a.Plan(ctx, inv, x, "", move.Options{CodeOnly: true, TargetDir: w.repo})
			if err != nil || len(p.Blockers) == 0 {
				t.Fatalf("amp has no code to bring: %+v %v", p, err)
			}
		}
	}
}
