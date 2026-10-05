package e2e

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/testkit/fakecloud"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// runHere runs a hop's teleport as the command line does, in the user's terminal; the user
// sends a message in the copy, so Claude Code saves it.
func runHere(t *testing.T) app.RunHere {
	return func(ctx context.Context, run agent.Command) error {
		c := exec.CommandContext(ctx, run.Argv[0], run.Argv[1:]...)
		c.Dir, c.Env = run.Dir, append(host.Without(os.Environ(), run.Unset), "FAKE_CLAUDE_SAYS=ok")
		out, err := c.CombinedOutput()
		if err != nil {
			t.Logf("teleport: %s", out)
		}
		return err
	}
}

// seedClaudeCloud adds a Claude Code cloud session on the demo repository that hopsesh
// handed off (its briefing is on record) and that pushed its work to a claude/… branch.
func seedClaudeCloud(t *testing.T, w *handoffWorld, a *app.App) fakecloud.Session {
	t.Helper()
	brief := "[hopsesh] add rate limiting to search"
	s, err := fakecloud.Open(w.store).Seed(fakecloud.Session{Cloud: fakecloud.ClaudeCloud, Title: "Add rate limiting", Repo: "github.com/example/demo",
		CloneURL: w.origin.FileURL(), Branch: "main", Base: w.git(w.repo, "rev-parse", "HEAD"), Code: "branch",
		Messages: []fakecloud.Message{{Role: "user", Text: brief}, {Role: "assistant", Text: "On it."}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := fakecloud.Work(fakecloud.Proc{Vars: map[string]string{}}, s.ID, false); err != nil {
		t.Fatal(err)
	}
	s, _ = fakecloud.Open(w.store).Get(s.ID)
	if err := move.SaveHandoff(a.StateDir, &move.Handoff{Journal: "earlier", Cloud: "claude-cloud", Repo: "github.com/example/demo", Machine: "here",
		Session: agent.SessionKey{Agent: "claude", Session: agent.SessionID(s.ID)}, Brief: brief}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Paste(context.Background(), "https://claude.ai/code/"+s.ID, w.repo); err != nil {
		t.Fatal(err)
	}
	return s
}

// Claude Code cloud → Codex cloud, through this machine: the plan shows both legs; the
// teleport brings the session here (a native copy, checked against the briefing hopsesh
// sent), and Codex cloud starts from the claude/… branch as the cloud pushed it, with a
// briefing of the copy. Lineage records both hops; undo takes both legs back.
func TestHopClaudeCloudToCodexCloud(t *testing.T) {
	w := newCodexWorld(t)
	a := w.app()
	a.Cfg.SetCloudAllowed("codex-cloud", true)
	a.RunHere = runHere(t)
	ctx := context.Background()
	s := seedClaudeCloud(t, w, a)
	before := w.statusOf()

	inv := a.Scan(ctx, app.ScanOptions{})
	defer inv.Close()
	e, err := inv.CloudEntry(a, "claude-cloud", agent.SessionID(s.ID))
	if err != nil {
		t.Fatal(err)
	}
	var codex *app.HandoffTarget
	for _, tg := range a.HopTargets(inv, e) {
		if tg.Cloud == "claude-cloud" {
			t.Fatal("a cloud is not a target of its own sessions")
		}
		if tg.Cloud == "codex-cloud" {
			codex = &tg
		}
	}
	if codex == nil || !codex.OK || !strings.Contains(codex.Note, "Comes here from Claude Code cloud first") {
		t.Fatalf("targets: %+v", codex)
	}
	// Without an environment the plan waits for one.
	p, err := a.PlanHop(ctx, inv, e, "codex-cloud", "", a.HandoffDefaults("codex-cloud"))
	if err != nil || !hasBlocker(p, "Pick a Codex cloud environment for github.com/example/demo") {
		t.Fatalf("no environment: %+v %v", p, err)
	}
	opt := a.HandoffDefaults("codex-cloud")
	opt.Env = "env_api"
	p, err = a.PlanHop(ctx, inv, e, "codex-cloud", "", opt)
	if err != nil || len(p.Blockers) > 0 {
		t.Fatalf("plan: %v %v", p.Blockers, err)
	}
	hp := p.Hop
	if p.Kind != move.KindHop || len(hp.Legs) != 2 || hp.Fidelity != "native → brief" || hp.Via != "here" || hp.Agent != "Claude Code" ||
		!strings.Contains(hp.Code, "Codex cloud starts from the claude/… branch Claude Code cloud pushed") ||
		!strings.Contains(hp.Terminal, "claude --teleport "+s.ID) || !strings.Contains(hp.Terminal, "send a message in it") ||
		hp.Then == nil || hp.Then.Env != "env_api" {
		t.Fatalf("hop plan: %+v", hp)
	}
	loss := strings.Join(hp.Loss, "\n")
	for _, want := range []string{"changes the cloud session did not commit", "Codex cloud gets a briefing", "Both cloud sessions stay where they are"} {
		if !strings.Contains(loss, want) {
			t.Errorf("the loss lacks %q:\n%s", want, loss)
		}
	}

	res, err := a.Apply(ctx, p, move.Input{}, nil)
	if err != nil {
		t.Fatalf("apply: %v %+v", err, res)
	}
	h := res.Hop
	if h.State != move.HopDone || h.Fetch == "" || h.Handoff == "" || res.Handoff == nil || !res.Handoff.Reuse || res.Handoff.Branch != s.Result ||
		!strings.HasPrefix(res.Handoff.Session, "task_e_") || res.Handoff.Pushed {
		t.Fatalf("hop: %+v %+v", h, res.Handoff)
	}
	log, _ := os.ReadFile(os.Getenv("FAKE_AGENT_LOG"))
	if !strings.Contains(string(log), "codex cloud exec --env env_api --branch "+s.Result+" [hopsesh] This task continues a Claude Code session") {
		t.Fatalf("codex ran as: %s", log)
	}
	if w.statusOf() != before {
		t.Fatal("the user's checkout changed")
	}
	f, err := move.LoadFetch(a.StateDir, h.Fetch)
	if err != nil || f.Adopted == nil || f.Adopted.Outcome != move.FetchComplete || f.Adopted.Check != move.CheckedBrief {
		t.Fatalf("the first leg: %+v %v", f, err)
	}
	// The copy here: both hops in its lineage, and marked as continued in Codex.
	inv2 := a.Scan(ctx, app.ScanOptions{Hosts: []string{"here"}})
	defer inv2.Close()
	copyHere, err := inv2.Find(app.ParseRef(h.Key))
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, x := range copyHere.Lineage.Hops {
		kinds = append(kinds, x.Kind)
	}
	if strings.Join(kinds, ",") != lineage.HopFetch+","+lineage.HopHandoff || copyHere.Session.Mark == nil || copyHere.Session.Mark.AgentName != "Codex cloud" {
		t.Fatalf("lineage %v, mark %+v", kinds, copyHere.Session.Mark)
	}
	// One journal, both legs.
	hj, err := journal.Load(a.StateDir, res.Journal)
	if err != nil || hj.Kind != journal.KindHop || strings.Join(hj.Parts, ",") != h.Fetch+","+h.Handoff {
		t.Fatalf("journal: %+v %v", hj, err)
	}
	acts, _ := a.Activities()
	for _, x := range acts {
		if (x.Journal.ID == h.Fetch || x.Journal.ID == h.Handoff) && !x.Part {
			t.Errorf("a leg is listed as its own operation: %+v", x)
		}
		if x.Journal.ID == res.Journal && !x.CanUndo {
			t.Errorf("the hop can't be undone: %s", x.Why)
		}
	}

	// Undo (the newest operation): both legs, the hand-off first.
	j, err := a.Undo(ctx, "", false)
	if err != nil || j.ID != res.Journal || !j.Undone || len(j.Manual) != 1 || j.Manual[0].Cloud != "codex-cloud" {
		t.Fatalf("undo: %+v %v", j, err)
	}
	if _, err := os.Stat(f.Worktree); !os.IsNotExist(err) {
		t.Error("undo leaves the worktree")
	}
	if _, err := os.Stat(f.Adopted.Path); !os.IsNotExist(err) {
		t.Error("undo leaves the teleported copy")
	}
	for _, id := range []string{h.Fetch, h.Handoff} {
		if leg, _ := journal.Load(a.StateDir, id); leg == nil || !leg.Undone {
			t.Errorf("leg %s is not undone", id)
		}
	}
}

// Codex cloud → Claude Code cloud, through this machine: the task's diff is committed here
// on hopsesh/from/codex-cloud/<id> and written as a Codex thread; the hand-off snapshots
// that worktree onto a handoff branch, pushes it, and starts the Claude Code session in the
// user's terminal. Undo takes both back, the pushed branch with a lease.
func TestHopCodexCloudToClaudeCloud(t *testing.T) {
	w := newCodexWorld(t)
	a := w.app()
	a.Cfg.SetCloudAllowed("codex-cloud", true)
	ctx := context.Background()
	task, err := fakecloud.Open(w.store).Seed(fakecloud.Session{Cloud: fakecloud.CodexCloud, Title: "Add a changelog entry", Repo: "github.com/example/demo",
		CloneURL: w.origin.FileURL(), Branch: "main", Base: w.git(w.repo, "rev-parse", "HEAD"), Code: "branch", Env: "env_api", EnvLabel: "acme-api",
		Messages: []fakecloud.Message{{Role: "user", Text: "Add a changelog entry"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := fakecloud.Work(fakecloud.Proc{Vars: map[string]string{}}, task.ID, false); err != nil {
		t.Fatal(err)
	}
	a.Cfg.SetCloudEnvironment("codex-cloud", "github.com/example/demo", "env_api")
	inv := a.Scan(ctx, app.ScanOptions{})
	defer inv.Close()
	e := cloudEntryFor(t, inv, "codex-cloud", task.ID)
	p, err := a.PlanHop(ctx, inv, e, "claude-cloud", "", a.HandoffDefaults("claude-cloud"))
	if err != nil || len(p.Blockers) > 0 {
		t.Fatalf("plan: %+v %v", p, err)
	}
	hp := p.Hop
	if hp.Fidelity != "code → brief" || hp.Agent != "Codex" || hp.Terminal != "" || !strings.Contains(hp.Code, "patch is committed here on hopsesh/from/codex-cloud/"+task.ID) ||
		hp.Then.Terminal == "" {
		t.Fatalf("hop plan: %+v %+v", hp, hp.Then)
	}
	// Without a terminal for Claude Code's step, the plan says so.
	steps := a.Steps
	a.Steps = nil
	if q, _ := a.PlanHop(ctx, inv, e, "claude-cloud", "", a.HandoffDefaults("claude-cloud")); q == nil || !hasBlocker(q, "only in a terminal you can answer") {
		t.Fatalf("no terminal: %+v", q)
	}
	a.Steps = steps

	res, err := a.Apply(ctx, p, move.Input{}, nil)
	if err != nil {
		t.Fatalf("apply: %v %+v", err, res)
	}
	hr := res.Handoff
	if res.Hop.State != move.HopDone || hr == nil || !hr.Pushed || !strings.HasPrefix(hr.Branch, "hopsesh/handoff/") || !strings.HasPrefix(hr.Session, "session_") ||
		res.Fetch.Branch != "hopsesh/from/codex-cloud/"+task.ID {
		t.Fatalf("hop: %+v %+v %+v", res.Hop, hr, res.Fetch)
	}
	tree := w.git(w.repo, "ls-tree", "-r", "--name-only", hr.Snapshot)
	if !strings.Contains(tree, "cloud-work/"+task.ID+".md") {
		t.Fatalf("the snapshot lacks the task's work:\n%s", tree)
	}
	cs, err := fakecloud.Open(w.store).Get(hr.Session)
	if err != nil || cs.Branch != hr.Branch || !strings.HasPrefix(cs.Messages[0].Text, "[hopsesh] This task continues a Codex session") {
		t.Fatalf("the Claude Code session: %+v %v", cs, err)
	}
	if _, err := a.Undo(ctx, res.Journal, false); err != nil {
		t.Fatal(err)
	}
	if out := w.git(w.origin.Bare, "for-each-ref", "refs/heads/"+hr.Branch); out != "" {
		t.Fatal("undo leaves the handoff branch")
	}
	if out := w.git(w.repo, "branch", "--list", "hopsesh/from/*"); out != "" {
		t.Fatalf("undo leaves %s", out)
	}
}

// Branch clean-up: the cloud's own branch brought home is offered for deletion only once
// its work is merged into the default branch; a handoff branch the user chose to keep is
// never offered, and undo of its hand-off leaves it. Deleting is a lease, and undo pushes
// the branch back.
func TestCleanupMergedBranches(t *testing.T) {
	w := newCodexWorld(t)
	a := w.app()
	a.Cfg.SetCloudAllowed("codex-cloud", true)
	ctx := context.Background()
	s := seedClaudeCloud(t, w, a)

	// Bring the Claude Code cloud session home (its claude/… branch comes with it).
	inv := a.Scan(ctx, app.ScanOptions{})
	defer inv.Close()
	e, err := inv.CloudEntry(a, "claude-cloud", agent.SessionID(s.ID))
	if err != nil {
		t.Fatal(err)
	}
	fp, _, err := a.Plan(ctx, inv, e, "", move.Options{TargetDir: w.repo})
	if err != nil || len(fp.Blockers) > 0 {
		t.Fatalf("plan: %+v %v", fp, err)
	}
	fres, err := a.Apply(ctx, fp, move.Input{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := runHere(t)(ctx, fp.Fetch.Run); err != nil {
		t.Fatal(err)
	}
	f, err := a.Adopt(ctx, fres.Journal, true)
	if err != nil || f.Adopted == nil || f.Adopted.Renamed != s.Result {
		t.Fatalf("brought: %+v %v", f, err)
	}

	// A hand-off whose branch the user keeps.
	w.dirty()
	opt := a.HandoffDefaults("codex-cloud")
	opt.Env, opt.Cleanup = "env_api", move.CleanupNever
	_, hp := planCodex(t, a, opt)
	hres, err := a.Apply(ctx, hp, move.Input{}, nil)
	if err != nil || !hres.Handoff.Pushed {
		t.Fatalf("hand-off: %v %+v", err, hres.Handoff)
	}
	branch := hres.Handoff.Branch

	find := func(cs []app.BranchCandidate, b string) app.BranchCandidate {
		t.Helper()
		for _, c := range cs {
			if c.Branch == b {
				return c
			}
		}
		t.Fatalf("no candidate %s in %+v", b, cs)
		return app.BranchCandidate{}
	}
	cands := a.CleanupCandidates(ctx)
	if v := find(cands, s.Result); v.Offer || v.Kind != app.BranchVendor || v.Why != "not merged into main yet" {
		t.Fatalf("not merged: %+v", v)
	}
	if h := find(cands, branch); h.Offer || h.Kind != app.BranchHandoff || !strings.HasPrefix(h.Why, "kept: delete_branch is never") {
		t.Fatalf("kept: %+v", h)
	}

	// The work brought home is merged into main on the remote.
	w.git(w.repo, "push", "-q", "origin", f.Adopted.Branch+":main")
	cands = a.CleanupCandidates(ctx)
	v := find(cands, s.Result)
	if !v.Offer || !v.Merged || !strings.HasPrefix(v.Why, "merged into main (in its history)") || v.Sha == "" {
		t.Fatalf("merged: %+v", v)
	}
	if _, err := a.DeleteBranches(ctx, []string{find(cands, branch).ID}); err == nil {
		t.Fatal("a branch that is not offered must not be deleted")
	}
	r, err := a.DeleteBranches(ctx, []string{v.ID})
	if err != nil || len(r.Deleted) != 1 || r.Journal == "" {
		t.Fatalf("delete: %+v %v", r, err)
	}
	if out := w.git(w.origin.Bare, "for-each-ref", "refs/heads/"+s.Result); out != "" {
		t.Fatal("the branch is still on the remote")
	}
	if _, err := a.Undo(ctx, r.Journal, false); err != nil {
		t.Fatal(err)
	}
	if out := w.git(w.origin.Bare, "rev-parse", "refs/heads/"+s.Result); out != v.Sha {
		t.Fatalf("undo puts the branch back: %s", out)
	}

	// Undo of the hand-off leaves the branch the user kept.
	j, err := a.Undo(ctx, hres.Journal, false)
	if err != nil || strings.Join(j.Kept, ",") != "origin "+branch {
		t.Fatalf("undo: %+v %v", j, err)
	}
	if out := w.git(w.origin.Bare, "for-each-ref", "refs/heads/"+branch); out == "" {
		t.Fatal("undo deleted a branch the user chose to keep")
	}
}

// A task handed off from a Claude Code session here comes back added to that session (a
// delta append: the session's own lines stay byte for byte), when it is as it was left;
// undo cuts the addition off again.
func TestBringCodexTaskIntoTheOriginal(t *testing.T) {
	w := newCodexWorld(t)
	a := w.app()
	ctx := context.Background()
	opt := a.HandoffDefaults("codex-cloud")
	opt.Env = "env_api"
	_, hp := planCodex(t, a, opt)
	if len(hp.Blockers) > 0 {
		t.Fatalf("plan: %v", hp.Blockers)
	}
	opt.Mark = false // the original stays as it was left
	_, hp = planCodex(t, a, opt)
	hres, err := a.Apply(ctx, hp, move.Input{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := fakecloud.Work(fakecloud.Proc{Vars: map[string]string{}}, hres.Handoff.Session, false); err != nil {
		t.Fatal(err)
	}
	inv := a.Scan(ctx, app.ScanOptions{})
	defer inv.Close()
	orig := findEntry(t, inv)
	before, _ := os.ReadFile(orig.Session.Path)
	e := cloudEntryFor(t, inv, "codex-cloud", hres.Handoff.Session)
	p, _, err := a.Plan(ctx, inv, e, "claude", move.Options{})
	if err != nil || !p.Fetch.Write || !p.Fetch.CanAppend || p.Fetch.Append {
		t.Fatalf("the offer: %+v %v", p.Fetch, err)
	}
	p, _, err = a.Plan(ctx, inv, e, "claude", move.Options{AppendOriginal: true})
	if err != nil || len(p.Blockers) > 0 || !p.Fetch.Append || p.Fetch.Relation != move.RelationAppend {
		t.Fatalf("append: %+v %v %v", p.Fetch, p.Blockers, err)
	}
	res, err := a.Apply(ctx, p, move.Input{}, nil)
	if err != nil || res.Fetch.Key != orig.Session.Key.String() {
		t.Fatalf("apply: %+v %v", res.Fetch, err)
	}
	after, _ := os.ReadFile(orig.Session.Path)
	if !strings.HasPrefix(string(after), string(before)) || len(after) <= len(before) || !strings.Contains(string(after[len(before):]), "Codex cloud ran this as task "+hres.Handoff.Session) {
		t.Fatalf("the original: %d → %d bytes", len(before), len(after))
	}
	if strings.Contains(string(after[len(before):]), "This task continues a Claude Code session") {
		t.Fatal("the briefing hopsesh sent is added back to the session it summed up")
	}
	f, _ := move.LoadFetch(a.StateDir, res.Journal)
	if b := a.Brought(f); !b.Appended || !b.Written || b.Key != orig.Session.Key.String() {
		t.Fatalf("brought: %+v", b)
	}
	if _, err := a.Undo(ctx, res.Journal, false); err != nil {
		t.Fatal(err)
	}
	if now, _ := os.ReadFile(orig.Session.Path); string(now) != string(before) {
		t.Fatal("undo must give the original back byte for byte")
	}
}

// A second-wave cloud whose session comes back as text hops too: a Copilot task's log comes
// here as a Claude Code session on the pull request's branch, and Codex cloud starts from
// that copilot/… branch as it is.
func TestHopCopilotToCodexCloud(t *testing.T) {
	w := newVendorWorld(t, "copilot-cloud", "codex-cloud")
	t.Setenv("FAKE_CODEX_ENVS", "env_api=acme-api")
	w.dirty()
	ctx := context.Background()
	a := w.app()
	inv, p := planTo(t, a, "copilot-cloud", a.HandoffDefaults("copilot-cloud"))
	defer inv.Close()
	res, err := a.Apply(ctx, p, move.Input{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := fakecloud.Work(fakecloud.Proc{Vars: map[string]string{}}, res.Handoff.Session, false); err != nil {
		t.Fatal(err)
	}
	s, _ := fakecloud.Open(w.store).Get(res.Handoff.Session)
	inv2 := a.Scan(ctx, app.ScanOptions{})
	defer inv2.Close()
	e := cloudEntryFor(t, inv2, "copilot-cloud", res.Handoff.Session)
	opt := a.HandoffDefaults("codex-cloud")
	opt.Env = "env_api"
	hp, err := a.PlanHop(ctx, inv2, e, "codex-cloud", "", opt)
	if err != nil || len(hp.Blockers) > 0 || hp.Hop.Fidelity != "text → brief" || hp.Hop.Agent != "Claude Code" {
		t.Fatalf("plan: %+v %v", hp, err)
	}
	hres, err := a.Apply(ctx, hp, move.Input{}, nil)
	if err != nil || hres.Hop.State != move.HopDone || !hres.Handoff.Reuse || hres.Handoff.Branch != s.Result {
		t.Fatalf("hop: %v %+v %+v", err, hres.Hop, hres.Handoff)
	}
	if _, err := a.Undo(ctx, hres.Journal, false); err != nil {
		t.Fatal(err)
	}
}
