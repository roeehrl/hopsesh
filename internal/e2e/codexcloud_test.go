package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/internal/testkit/fakecloud"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// codexWorld is the hand-off world with the stand-in codex on PATH too, Codex cloud allowed,
// and two environments on the stand-in account.
func newCodexWorld(t *testing.T) *handoffWorld {
	t.Helper()
	w := newHandoffWorld(t)
	self, _ := os.Executable()
	if err := os.Symlink(self, filepath.Join(filepath.Dir(w.home), "bin", "codex")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CODEX_ENVS", "env_api=acme-api,env_web=acme-web")
	t.Setenv("CODEX_STARTING_DIFF", "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.SetCloudAllowed("codex-cloud", true)
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return w
}

// planCodex scans and plans handing the demo session off to Codex cloud.
func planCodex(t *testing.T, a *app.App, opt move.Options) (*app.Inventory, *move.Plan) {
	t.Helper()
	ctx := context.Background()
	inv := a.Scan(ctx, app.ScanOptions{})
	e := findEntry(t, inv)
	p, err := a.PlanHandoff(ctx, inv, e, "codex-cloud", opt)
	if err != nil {
		t.Fatal(err)
	}
	return inv, p
}

// cloudEntryFor is the scanned entry of a cloud session.
func cloudEntryFor(t *testing.T, inv *app.Inventory, cloud, id string) app.Entry {
	t.Helper()
	for _, e := range inv.Entries {
		if e.Location.IsCloud() && e.Location.Name == cloud && string(e.Session.Key.Session) == id {
			return e
		}
	}
	t.Fatalf("%s:%s is not listed", cloud, id)
	return app.Entry{}
}

// The whole round trip with Codex cloud: the plan asks for an environment (suggesting the
// ones recent tasks used), the snapshot goes on a handoff branch, `codex cloud exec` starts
// the task with the briefing; the task works; bringing it back commits its diff on a
// hopsesh/from/codex-cloud/<id> branch in a new worktree and writes the task as a Codex
// thread; undo takes the fetch away, and the hand-off's undo is refused once the task moved
// on, unless forced.
func TestHandoffToCodexCloudAndBack(t *testing.T) {
	w := newCodexWorld(t)
	w.dirty()
	ctx := context.Background()
	if _, err := fakecloud.Open(w.store).Seed(fakecloud.Session{Cloud: fakecloud.CodexCloud, Title: "An earlier task", Env: "env_api", EnvLabel: "acme-api",
		State: fakecloud.StateDone}); err != nil {
		t.Fatal(err)
	}
	a := w.app()
	inv, p := planCodex(t, a, a.HandoffDefaults("codex-cloud"))
	defer inv.Close()
	hp := p.Handoff
	want := "Pick a Codex cloud environment for github.com/example/demo. If you have none, open `codex cloud` once to create one."
	if !hp.EnvNeeded || hp.Env != "" || hp.EnvNote != want || !hasBlocker(p, want) {
		t.Fatalf("no environment: %+v %v", hp, p.Blockers)
	}
	if len(hp.Envs) != 1 || hp.Envs[0].Value != "env_api" || hp.Envs[0].Label != "acme-api (used by 1 of your recent tasks)" {
		t.Fatalf("suggestions: %+v", hp.Envs)
	}
	for _, tg := range a.HandoffTargets(inv, findEntry(t, inv)) {
		if tg.Cloud == "codex-cloud" && (!tg.OK || tg.Note != "Gets a briefing and the code on a branch" || len(tg.Limits) == 0 ||
			!strings.Contains(tg.Limits[0], "Codex can't see any cloud environments from its command line")) {
			t.Fatalf("target: %+v", tg)
		}
	}
	opt := a.HandoffDefaults("codex-cloud")
	opt.Env = "env_api"
	opt.Untracked = []string{"docs/plan.md"}
	inv2, p := planCodex(t, a, opt)
	defer inv2.Close()
	hp = p.Handoff
	if len(p.Blockers) > 0 || hp.Env != "env_api" || hp.EnvName != "acme-api" || !hp.Remember || hp.Noun != "task" || hp.Follow ||
		strings.Join(hp.Steps, ",") != "snapshot,push,start,lineage,mark" || hp.MarkTitle != "↪ continued in Codex cloud" ||
		hp.Usage != "Cloud tasks use your plan's allowance." || hp.CanStartingDiff {
		t.Fatalf("plan: %v %+v", p.Blockers, hp)
	}
	if !strings.Contains(strings.Join(hp.Loss, "\n"), "Codex can't see any cloud environments") {
		t.Fatalf("loss: %v", hp.Loss)
	}
	before := w.statusOf()
	res, err := a.Apply(ctx, p, move.Input{}, nil)
	if err != nil {
		t.Fatalf("apply: %v %+v", err, res.Handoff)
	}
	hr := res.Handoff
	if !strings.HasPrefix(hr.Session, "task_e_") || hr.URL != "https://chatgpt.com/codex/tasks/"+hr.Session || !hr.Pushed || hr.Noun != "task" ||
		hr.Env != "env_api" || hr.Manual != "The task stays in your Codex cloud list; archive it there if you want it gone." || hr.Message != "Handed off to Codex cloud" {
		t.Fatalf("result: %+v", hr)
	}
	if w.statusOf() != before {
		t.Fatal("the user's checkout changed")
	}
	log, _ := os.ReadFile(os.Getenv("FAKE_AGENT_LOG"))
	if !strings.Contains(string(log), "codex cloud exec --env env_api --branch "+hp.Branch+" [hopsesh] This task continues a Claude Code session") {
		t.Fatalf("the driver ran as: %s", log)
	}
	s, err := fakecloud.Open(w.store).Get(hr.Session)
	if err != nil || s.Branch != hp.Branch || s.Base != hr.Snapshot || s.Env != "env_api" || !strings.HasPrefix(s.Messages[0].Text, "[hopsesh] ") {
		t.Fatalf("the task: %+v %v", s, err)
	}
	if !a.RememberEnv(p) || a.Cfg.CloudSettings("codex-cloud").Environments["github.com/example/demo"] != "env_api" {
		t.Fatal("the environment is remembered for the repository")
	}

	// The task is listed with what hopsesh knows of it; the cloud works on it.
	inv3 := a.Scan(ctx, app.ScanOptions{})
	defer inv3.Close()
	e := cloudEntryFor(t, inv3, "codex-cloud", hr.Session)
	if e.Original != "here:"+p.Key.String() || e.Cloud.Repo != "github.com/example/demo" || e.Cloud.Branch != hp.Branch || e.Cloud.State != agent.CloudRunning ||
		e.Cloud.EnvLabel != "acme-api" {
		t.Fatalf("listed: %+v %+v", e, e.Cloud)
	}
	running, _, err := a.Plan(ctx, inv3, e, "", move.Options{})
	if err != nil || !hasBlocker(running, "The task is still running in Codex cloud, so there is no code to bring yet") {
		t.Fatalf("a running task: %+v %v", running, err)
	}
	if err := fakecloud.Work(fakecloud.Proc{Vars: map[string]string{}}, hr.Session, false); err != nil {
		t.Fatal(err)
	}
	inv4 := a.Scan(ctx, app.ScanOptions{})
	defer inv4.Close()
	e = cloudEntryFor(t, inv4, "codex-cloud", hr.Session)
	fp, _, err := a.Plan(ctx, inv4, e, "", move.Options{})
	if err != nil || len(fp.Blockers) > 0 {
		t.Fatalf("bring-back plan: %+v %v", fp, err)
	}
	f := fp.Fetch
	if !f.Write || !f.Diff || f.Writer != "Codex" || f.Fidelity != agent.FidCode || f.LocalBranch != "hopsesh/from/codex-cloud/"+hr.Session ||
		f.BranchState != move.BranchPushed || f.CloudBranch != hp.Branch || f.Changes != "+1 −0 · 1 file" || f.Messages != 2 ||
		f.Conversation != "Only the task title, summary and code come back. The steps stay in the cloud." || f.Command != "" || len(f.Loss) == 0 {
		t.Fatalf("fetch plan: %+v", f)
	}
	fres, err := a.Apply(ctx, fp, move.Input{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fres.Fetch.Outcome != move.FetchComplete || fres.Fetch.Branch != f.LocalBranch || !strings.HasPrefix(fres.Fetch.Key, "codex@") || !strings.Contains(fres.Command, "codex resume") {
		t.Fatalf("fetched: %+v %s", fres.Fetch, fres.Command)
	}
	for _, file := range []string{"cloud-work/" + hr.Session + ".md", "parser.go", "docs/plan.md"} {
		if _, err := os.Stat(filepath.Join(f.Worktree, file)); err != nil {
			t.Errorf("the worktree lacks %s (the task's diff on its branch)", file)
		}
	}
	if got := w.git(f.Worktree, "rev-parse", "--abbrev-ref", "HEAD"); got != f.LocalBranch {
		t.Fatalf("the worktree is on %s", got)
	}
	if got := w.git(f.Worktree, "log", "-1", "--format=%s"); got != "Codex cloud task "+hr.Session {
		t.Fatalf("commit: %s", got)
	}
	if w.statusOf() != before {
		t.Fatal("the user's checkout changed")
	}
	rec, err := move.LoadFetch(a.StateDir, fres.Journal)
	if err != nil {
		t.Fatal(err)
	}
	b := a.Brought(rec)
	if !b.Written || b.Agent != "Codex" || b.Outcome != move.FetchComplete || !strings.Contains(b.Message, "is here in Codex: the task's title and what came of it, with its code on "+f.LocalBranch) ||
		len(b.Loss) == 0 || b.Fidelity != "code" {
		t.Fatalf("brought: %+v", b)
	}
	// The thread: the briefing hopsesh sent as its prompt, what came of the task, the
	// briefing for the way back; listed by Codex in the worktree.
	inv5 := a.Scan(ctx, app.ScanOptions{Hosts: []string{"here"}})
	defer inv5.Close()
	thread, err := inv5.Find(app.ParseRef(fres.Fetch.Key))
	if err != nil || thread.Session.CWD != f.Worktree || !strings.HasSuffix(thread.Session.Title, "(from Codex cloud)") {
		t.Fatalf("the thread: %+v %v", thread.Session, err)
	}
	body, _ := os.ReadFile(thread.Session.Path)
	for _, want := range []string{"[hopsesh] This task continues a Claude Code session", "Codex cloud ran this as task " + hr.Session, "cloud-work/" + hr.Session + ".md",
		"This conversation was moved from Codex cloud"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the thread lacks %q", want)
		}
	}
	if thread.Lineage == nil || thread.Lineage.Hops[len(thread.Lineage.Hops)-1].Kind != lineage.HopFetch || thread.Lineage.Hops[len(thread.Lineage.Hops)-1].Fidelity != "code" {
		t.Fatalf("lineage: %+v", thread.Lineage)
	}

	// Undo the fetch: the thread, the branch and the worktree go.
	if _, err := a.Undo(ctx, fres.Journal, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(thread.Session.Path); err == nil {
		t.Error("undo leaves the thread")
	}
	if repos.BranchExists(ctx, w.repo, f.LocalBranch) {
		t.Error("undo leaves the branch")
	}
	if _, err := os.Stat(f.Worktree); err == nil {
		t.Error("undo leaves the worktree")
	}
	// The hand-off: the task moved on since, so undo asks first; forced, the branch and the
	// mark go and the task is a step the user owes.
	if _, err := a.Undo(ctx, res.Journal, false); err == nil || !strings.Contains(err.Error(), "new activity") {
		t.Fatalf("undo after the task worked: %v", err)
	}
	j, err := a.Undo(ctx, res.Journal, true)
	if err != nil || len(j.Manual) != 1 || j.Manual[0].Cloud != "codex-cloud" || j.Manual[0].URL != hr.URL {
		t.Fatalf("forced undo: %+v %v", j, err)
	}
	if out := w.git(w.origin.Bare, "for-each-ref", "refs/heads/"+hp.Branch); out != "" {
		t.Fatal("undo leaves the handoff branch")
	}
}

// Bringing a task back into Claude Code writes a Claude Code transcript in the worktree.
func TestBringCodexTaskIntoClaude(t *testing.T) {
	w := newCodexWorld(t)
	ctx := context.Background()
	w.git(w.repo, "push", "-q", "origin", "main:refs/heads/topic")
	s, err := fakecloud.Open(w.store).Seed(fakecloud.Session{Cloud: fakecloud.CodexCloud, Title: "Add a cloud note", Env: "env_web", EnvLabel: "acme-web",
		Repo: "github.com/example/demo", CloneURL: w.origin.FileURL(), Branch: "topic", Base: w.git(w.repo, "rev-parse", "HEAD")})
	if err != nil {
		t.Fatal(err)
	}
	if err := fakecloud.Work(fakecloud.Proc{Vars: map[string]string{}}, s.ID, false); err != nil {
		t.Fatal(err)
	}
	a := w.app()
	a.Cfg.SetCloudEnvironment("codex-cloud", "github.com/example/demo", "acme-web")
	inv := a.Scan(ctx, app.ScanOptions{})
	defer inv.Close()
	e := cloudEntryFor(t, inv, "codex-cloud", s.ID)
	if e.Cloud.Repo != "github.com/example/demo" || e.Checkout == "" && e.Git == nil {
		t.Fatalf("the repository comes from the environment's configuration: %+v", e.Cloud)
	}
	p, _, err := a.Plan(ctx, inv, e, "claude", move.Options{TargetDir: w.repo})
	if err != nil || len(p.Blockers) > 0 || p.Fetch.Writer != "Claude Code" || p.Fetch.CloudBranch != "" || p.Fetch.LocalBranch != "hopsesh/from/codex-cloud/"+s.ID {
		t.Fatalf("plan: %+v %v", p, err)
	}
	res, err := a.Apply(ctx, p, move.Input{}, nil)
	if err != nil || !strings.HasPrefix(res.Fetch.Key, "claude@") || !strings.Contains(res.Command, " --resume ") {
		t.Fatalf("apply: %+v %v", res, err)
	}
	inv2 := a.Scan(ctx, app.ScanOptions{Hosts: []string{"here"}})
	defer inv2.Close()
	tr, err := inv2.Find(app.ParseRef(res.Fetch.Key))
	if err != nil || tr.Session.CWD != p.Fetch.Worktree {
		t.Fatalf("the transcript: %+v %v", tr.Session, err)
	}
	body, _ := os.ReadFile(tr.Session.Path)
	if !strings.Contains(string(body), "Add a cloud note") || !strings.Contains(string(body), "Codex cloud ran this as task "+s.ID) {
		t.Fatalf("transcript:\n%s", body)
	}
	f, _ := move.LoadFetch(a.StateDir, res.Journal)
	if b := a.Brought(f); b.Agent != "Claude Code" || !b.Written {
		t.Fatalf("brought: %+v", b)
	}
	if _, err := a.Undo(ctx, res.Journal, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tr.Session.Path); err == nil {
		t.Error("undo leaves the transcript")
	}
}

// A small change on a branch already pushed can go with the task as its starting diff:
// nothing is pushed, and the task's diff comes back on top of that branch.
func TestCodexStartingDiff(t *testing.T) {
	w := newCodexWorld(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(w.repo, "notes.txt"), []byte("the codeword is PLUM, now in a starting diff\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := w.app()
	opt := a.HandoffDefaults("codex-cloud")
	opt.Env = "acme-api"
	inv, p := planCodex(t, a, opt)
	defer inv.Close()
	hp := p.Handoff
	if !hp.CanStartingDiff || !strings.Contains(hp.StartingDiffOffer, "main is on github.com as it is here, so the 1 changed file can go with the task as a starting diff") {
		t.Fatalf("offer: %+v", hp)
	}
	opt.StartingDiff = true
	inv2, p := planCodex(t, a, opt)
	defer inv2.Close()
	hp = p.Handoff
	if len(p.Blockers) > 0 || hp.Code != agent.ViaStartingDiff || hp.Branch != "main" || strings.Join(hp.Steps, ",") != "snapshot,start,lineage,mark" {
		t.Fatalf("plan: %v %+v", p.Blockers, hp)
	}
	res, err := a.Apply(ctx, p, move.Input{}, nil)
	if err != nil || res.Handoff.Pushed || res.Handoff.Code != "starting-diff" {
		t.Fatalf("apply: %+v %v", res.Handoff, err)
	}
	s, _ := fakecloud.Open(w.store).Get(res.Handoff.Session)
	if s.Branch != "main" || !strings.Contains(s.Diff, "+the codeword is PLUM, now in a starting diff") {
		t.Fatalf("the task's starting diff: %+v", s)
	}
	if out := w.git(w.origin.Bare, "for-each-ref", "refs/heads/hopsesh/"); out != "" {
		t.Fatalf("nothing is pushed: %s", out)
	}
	if err := fakecloud.Work(fakecloud.Proc{Vars: map[string]string{}}, s.ID, false); err != nil {
		t.Fatal(err)
	}
	inv3 := a.Scan(ctx, app.ScanOptions{})
	defer inv3.Close()
	fp, _, err := a.Plan(ctx, inv3, cloudEntryFor(t, inv3, "codex-cloud", s.ID), "", move.Options{})
	if err != nil || len(fp.Blockers) > 0 || fp.Fetch.CloudBranch != "main" {
		t.Fatalf("bring-back: %+v %v", fp, err)
	}
	fres, err := a.Apply(ctx, fp, move.Input{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(fp.Fetch.Worktree, "notes.txt")); string(b) != "the codeword is PLUM, now in a starting diff\n" {
		t.Fatalf("the starting diff comes back with the task's work: %q", b)
	}
	if _, err := a.Undo(ctx, fres.Journal, false); err != nil {
		t.Fatal(err)
	}
	// A starting diff is refused where it does not fit: unpushed commits.
	w.dirty()
	inv4, p4 := planCodex(t, a, opt)
	defer inv4.Close()
	if !hasBlocker(p4, "A starting diff needs a branch already on the remote and a few changed files; here the session's branch is not on the remote as it is here") {
		t.Fatalf("blockers: %v", p4.Blockers)
	}
}

// Codex cloud's failures, as the scan, the plan and the steps report them.
func TestCodexCloudFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("no-env", func(t *testing.T) {
		w := newCodexWorld(t)
		w.dirty()
		a := w.app()
		opt := a.HandoffDefaults("codex-cloud")
		opt.Env = "env_gone"
		inv, p := planCodex(t, a, opt)
		defer inv.Close()
		if len(p.Blockers) > 0 {
			t.Fatalf("blockers: %v", p.Blockers)
		}
		res, err := a.Apply(ctx, p, move.Input{}, nil)
		want := "The branch was pushed, but Codex refused the task: environment 'env_gone' not found; run `codex cloud` to list available environments. Undo removes the branch."
		if err == nil || err.Error() != want || res.Handoff.Failed != move.StepStart {
			t.Fatalf("start refused: %v", err)
		}
		if _, err := a.Undo(ctx, res.Journal, false); err != nil {
			t.Fatal(err)
		}
		if out := w.git(w.origin.Bare, "for-each-ref", "refs/heads/"+p.Handoff.Branch); out != "" {
			t.Fatal("undo leaves the pushed branch")
		}
	})
	t.Run("signed-out", func(t *testing.T) {
		w := newCodexWorld(t)
		t.Setenv("FAKE_CLOUD_FAIL", "signed-out")
		a := w.app()
		opt := a.HandoffDefaults("codex-cloud")
		opt.Env = "env_api"
		inv, p := planCodex(t, a, opt)
		defer inv.Close()
		if !hasBlocker(p, "Codex here uses an API key. Cloud tasks need a ChatGPT login") {
			t.Fatalf("blockers: %v", p.Blockers)
		}
		if c := inv.Cloud("codex-cloud"); c.Status != app.CloudSignedOut {
			t.Fatalf("cloud: %+v", c)
		}
		for _, tg := range a.HandoffTargets(inv, findEntry(t, inv)) {
			if tg.Cloud == "codex-cloud" && (tg.OK || tg.Why != "Codex here isn't signed in with ChatGPT. Cloud tasks need a ChatGPT login") {
				t.Fatalf("target: %+v", tg)
			}
		}
	})
	t.Run("not-eligible", func(t *testing.T) {
		w := newCodexWorld(t)
		t.Setenv("FAKE_CLOUD_FAIL", "not-eligible")
		a := w.app()
		inv, p := planCodex(t, a, a.HandoffDefaults("codex-cloud"))
		defer inv.Close()
		if !hasBlocker(p, "Your plan doesn't include Codex cloud") {
			t.Fatalf("blockers: %v", p.Blockers)
		}
		if c := inv.Cloud("codex-cloud"); c.Status != app.CloudNotEligible {
			t.Fatalf("cloud: %+v", c)
		}
	})
	t.Run("repo-not-github", func(t *testing.T) {
		w := newCodexWorld(t)
		w.git(w.repo, "remote", "set-url", "origin", "https://gitlab.example.com/example/demo.git")
		w.git(w.repo, "config", "url."+w.origin.FileURL()+".insteadOf", "https://gitlab.example.com/example/demo.git")
		a := w.app()
		opt := a.HandoffDefaults("codex-cloud")
		opt.Env = "env_api"
		inv, p := planCodex(t, a, opt)
		defer inv.Close()
		if !hasBlocker(p, "This repository's remote is gitlab.example.com. Codex cloud needs github.com") || p.Handoff.CanBundle {
			t.Fatalf("blockers: %v", p.Blockers)
		}
		for _, tg := range a.HandoffTargets(inv, findEntry(t, inv)) {
			if tg.Cloud == "codex-cloud" && (tg.OK || tg.Why != "this repository isn't on GitHub") {
				t.Fatalf("target: %+v", tg)
			}
		}
	})
	t.Run("apply-conflict", func(t *testing.T) {
		w := newCodexWorld(t)
		a := w.app()
		w.git(w.repo, "push", "-q", "origin", "main:refs/heads/topic")
		s, err := fakecloud.Open(w.store).Seed(fakecloud.Session{Cloud: fakecloud.CodexCloud, Title: "Clash", Env: "env_api", Repo: "github.com/example/demo",
			CloneURL: w.origin.FileURL(), Branch: "topic", Base: w.git(w.repo, "rev-parse", "HEAD")})
		if err != nil {
			t.Fatal(err)
		}
		if err := fakecloud.Work(fakecloud.Proc{Vars: map[string]string{}}, s.ID, false); err != nil {
			t.Fatal(err)
		}
		// The branch the task started from moves on with the same file.
		clash := filepath.Join(t.TempDir(), "clash")
		w.git("", "clone", "-q", "--branch", "topic", w.origin.FileURL(), clash)
		if err := os.MkdirAll(filepath.Join(clash, "cloud-work"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(clash, "cloud-work", s.ID+".md"), []byte("someone else\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		w.git(clash, "add", "-A")
		w.git(clash, "commit", "-qm", "clash")
		w.git(clash, "push", "-q", "origin", "topic")
		// hopsesh knows the task started from topic (the lineage of a hand-off says so; here, a
		// pasted link and the branch).
		inv := a.Scan(ctx, app.ScanOptions{})
		defer inv.Close()
		e := cloudEntryFor(t, inv, "codex-cloud", s.ID)
		e.Cloud.Branch, e.Cloud.Repo = "topic", "github.com/example/demo"
		p, _, err := a.Plan(ctx, inv, e, "", move.Options{TargetDir: w.repo})
		if err != nil || len(p.Blockers) > 0 || p.Fetch.CloudBranch != "topic" {
			t.Fatalf("plan: %+v %v", p, err)
		}
		res, err := a.Apply(ctx, p, move.Input{}, nil)
		if err == nil || !strings.Contains(err.Error(), "Codex cloud's patch does not apply on") {
			t.Fatalf("a patch that does not apply: %v", err)
		}
		if repos.BranchExists(ctx, w.repo, p.Fetch.LocalBranch) {
			t.Fatal("no branch is made for a patch that does not apply")
		}
		if _, err := a.Undo(ctx, res.Journal, false); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(p.Fetch.Worktree); err == nil {
			t.Fatal("undo leaves the worktree")
		}
	})
}
