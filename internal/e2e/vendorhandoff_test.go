package e2e

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
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

// Hand-offs to the second-wave clouds (Copilot, Jules, Devin, Amp) through their own CLIs,
// and the way back: the code, and, for Copilot's log and Amp's thread, the conversation as
// text written into a local agent.

// newVendorWorld is the hand-off world with the stand-in vendor CLIs (and codex) on PATH,
// and the clouds allowed.
func newVendorWorld(t *testing.T, clouds ...string) *handoffWorld {
	t.Helper()
	w := newHandoffWorld(t)
	self, _ := os.Executable()
	for _, name := range []string{"gh", "jules", "devin", "amp", "codex"} {
		if err := os.Symlink(self, filepath.Join(filepath.Dir(w.home), "bin", name)); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range clouds {
		cfg.SetCloudAllowed(c, true)
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return w
}

// planTo scans and plans handing the demo session off to cloud.
func planTo(t *testing.T, a *app.App, cloud string, opt move.Options) (*app.Inventory, *move.Plan) {
	t.Helper()
	ctx := context.Background()
	inv := a.Scan(ctx, app.ScanOptions{})
	p, err := a.PlanHandoff(ctx, inv, findEntry(t, inv), cloud, opt)
	if err != nil {
		t.Fatal(err)
	}
	return inv, p
}

func agentLog(t *testing.T) string {
	t.Helper()
	b, _ := os.ReadFile(os.Getenv("FAKE_AGENT_LOG"))
	return string(b)
}

var copilotSession = regexp.MustCompile(`^https://github\.com/example/demo/pull/\d+/agent-sessions/[0-9a-f-]{36}$`)

// The round trip with the Copilot cloud agent: the snapshot goes on a handoff branch, gh
// agent-task create starts the task from it with the briefing on standard input, Copilot
// works on its copilot/… branch, and bringing it back fetches that branch into a new
// worktree and writes the session log as a Claude Code session (the agent it was handed off
// from); undo takes both away.
func TestHandoffToCopilotAndBack(t *testing.T) {
	w := newVendorWorld(t, "copilot-cloud")
	w.dirty()
	ctx := context.Background()
	a := w.app()
	inv, p := planTo(t, a, "copilot-cloud", a.HandoffDefaults("copilot-cloud"))
	defer inv.Close()
	hp := p.Handoff
	if len(p.Blockers) > 0 || hp.Noun != "session" || hp.Follow || strings.Join(hp.Steps, ",") != "snapshot,push,start,lineage,mark" ||
		hp.MarkTitle != "↪ continued in GitHub Copilot on copilot-cloud" || strings.Contains(hp.Brief, "git checkout") {
		t.Fatalf("plan: %v %+v", p.Blockers, hp)
	}
	for _, tg := range a.HandoffTargets(inv, findEntry(t, inv)) {
		if tg.Cloud == "copilot-cloud" && !tg.OK {
			t.Fatalf("target: %+v", tg)
		}
	}
	before := w.statusOf()
	res, err := a.Apply(ctx, p, move.Input{}, nil)
	if err != nil {
		t.Fatalf("apply: %v %+v", err, res.Handoff)
	}
	hr := res.Handoff
	if !copilotSession.MatchString(hr.URL) || !strings.HasSuffix(hr.URL, hr.Session) || !hr.Pushed || hr.Message != "Handed off to Copilot cloud agent" {
		t.Fatalf("result: %+v", hr)
	}
	if w.statusOf() != before {
		t.Fatal("the user's checkout changed")
	}
	if log := agentLog(t); !strings.Contains(log, "gh agent-task create -F - --base "+hp.Branch+" -R example/demo") {
		t.Fatalf("gh ran as:\n%s", log)
	}
	s, err := fakecloud.Open(w.store).Get(hr.Session)
	if err != nil || s.Branch != hp.Branch || s.Base != hr.Snapshot || s.Messages[0].Text != hp.Brief {
		t.Fatalf("the task (the briefing went on standard input): %+v %v", s, err)
	}
	if err := fakecloud.Work(fakecloud.Proc{Vars: map[string]string{}}, hr.Session, false); err != nil {
		t.Fatal(err)
	}
	s, _ = fakecloud.Open(w.store).Get(hr.Session)

	// Back: the default is the agent it came from, Claude Code.
	inv2 := a.Scan(ctx, app.ScanOptions{})
	defer inv2.Close()
	e := cloudEntryFor(t, inv2, "copilot-cloud", hr.Session)
	if e.Original != "here:claude/"+demoSession || e.Cloud.Branch != s.Result || a.BringTarget(inv2, e) != "claude" {
		t.Fatalf("listed: %+v %+v", e, e.Cloud)
	}
	fp, _, err := a.Plan(ctx, inv2, e, "", move.Options{})
	if err != nil || len(fp.Blockers) > 0 {
		t.Fatalf("bring-back plan: %+v %v", fp, err)
	}
	f := fp.Fetch
	if !f.Write || f.Diff || f.Writer != "Claude Code" || f.ContinueIn != "claude" || f.Fidelity != agent.FidText || f.CloudBranch != s.Result ||
		f.BranchState != move.BranchPushed || !strings.HasPrefix(f.LocalBranch, "hopsesh/from/copilot-cloud/") || f.Messages != 2 ||
		f.Conversation != "The messages come back as text; tool calls stay in the cloud." || f.Command != "" || len(f.Loss) == 0 {
		t.Fatalf("fetch plan: %+v", f)
	}
	fres, err := a.Apply(ctx, fp, move.Input{}, nil)
	if err != nil || fres.Fetch.Outcome != move.FetchComplete || fres.Fetch.Branch != f.LocalBranch || !strings.HasPrefix(fres.Fetch.Key, "claude/") {
		t.Fatalf("fetched: %+v %v", fres, err)
	}
	for _, file := range []string{"cloud-work/" + hr.Session + ".md", "parser.go"} {
		if _, err := os.Stat(filepath.Join(f.Worktree, file)); err != nil {
			t.Errorf("the worktree lacks %s", file)
		}
	}
	if got := repos.CurrentBranch(ctx, f.Worktree); got != f.LocalBranch {
		t.Fatalf("the worktree is on %s", got)
	}
	if w.statusOf() != before {
		t.Fatal("the user's checkout changed")
	}
	rec, err := move.LoadFetch(a.StateDir, fres.Journal)
	if err != nil {
		t.Fatal(err)
	}
	b := a.Brought(rec)
	if !b.Written || b.Agent != "Claude Code" || b.Fidelity != "text" || len(b.Loss) == 0 || !strings.Contains(b.Message, "2 messages, as text, with its code on "+f.LocalBranch) {
		t.Fatalf("brought: %+v", b)
	}
	inv3 := a.Scan(ctx, app.ScanOptions{Hosts: []string{"here"}})
	defer inv3.Close()
	tr, err := inv3.Find(app.ParseRef(fres.Fetch.Key))
	if err != nil || tr.Session.CWD != f.Worktree || !strings.HasSuffix(tr.Session.Title, "(from Copilot cloud agent)") {
		t.Fatalf("the transcript: %+v %v", tr.Session, err)
	}
	body, _ := os.ReadFile(tr.Session.Path)
	for _, want := range []string{"[hopsesh] This task continues a Claude Code session", "I added cloud-work/" + hr.Session + ".md and pushed it to " + s.Result} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the transcript lacks %q", want)
		}
	}
	if tr.Lineage == nil || tr.Lineage.Hops[len(tr.Lineage.Hops)-1].Kind != lineage.HopFetch || tr.Lineage.Hops[len(tr.Lineage.Hops)-1].Fidelity != "text" {
		t.Fatalf("lineage: %+v", tr.Lineage)
	}

	// Into Codex instead, when the user picks it; the code alone still comes as before.
	cp, _, err := a.Plan(ctx, inv2, e, "codex", move.Options{})
	if err != nil || len(cp.Blockers) > 0 || cp.Fetch.Writer != "Codex" || cp.Fetch.ContinueIn != "codex" {
		t.Fatalf("into Codex: %+v %v", cp, err)
	}
	op, _, err := a.Plan(ctx, inv2, e, "", move.Options{CodeOnly: true})
	if err != nil || len(op.Blockers) > 0 || op.Fetch.Write || op.Fetch.ContinueIn != "" || op.Agent != "GitHub Copilot" {
		t.Fatalf("code only: %+v %v", op, err)
	}

	// Undo the fetch, then the hand-off (forced: the task moved on).
	if _, err := a.Undo(ctx, fres.Journal, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tr.Session.Path); err == nil {
		t.Error("undo leaves the transcript")
	}
	if repos.BranchExists(ctx, w.repo, f.LocalBranch) {
		t.Error("undo leaves the branch")
	}
	if _, err := a.Undo(ctx, res.Journal, true); err != nil {
		t.Fatal(err)
	}
	if out := w.git(w.origin.Bare, "for-each-ref", "refs/heads/"+hp.Branch); out != "" {
		t.Fatal("undo leaves the handoff branch")
	}
	if log := agentLog(t); strings.Contains(log, "--show-token") || strings.Contains(log, "auth token") {
		t.Error("hopsesh asked gh for a token")
	}
}

// When GitHub refuses the task after the branch went up, the result says so and undo
// removes the branch; a gh that is not signed in blocks the plan.
func TestHandoffToCopilotRefused(t *testing.T) {
	w := newVendorWorld(t, "copilot-cloud")
	w.dirty()
	ctx := context.Background()
	a := w.app()
	inv, p := planTo(t, a, "copilot-cloud", a.HandoffDefaults("copilot-cloud"))
	defer inv.Close()
	if len(p.Blockers) > 0 {
		t.Fatalf("plan: %v", p.Blockers)
	}
	t.Setenv("FAKE_CLOUD_FAIL", "repo-mismatch")
	res, err := a.Apply(ctx, p, move.Input{}, nil)
	if err == nil || !res.Handoff.Pushed || res.Handoff.Failed != move.StepStart || !strings.Contains(res.Handoff.Message, "Undo removes the branch") {
		t.Fatalf("refused: %+v %v", res.Handoff, err)
	}
	if _, err := a.Undo(ctx, res.Journal, false); err != nil {
		t.Fatal(err)
	}
	if out := w.git(w.origin.Bare, "for-each-ref", "refs/heads/hopsesh/"); out != "" {
		t.Fatalf("undo leaves the handoff branch: %s", out)
	}
	t.Setenv("FAKE_CLOUD_FAIL", "signed-out")
	a2 := w.app()
	inv2, p2 := planTo(t, a2, "copilot-cloud", a2.HandoffDefaults("copilot-cloud"))
	defer inv2.Close()
	if !hasBlocker(p2, "gh auth login") {
		t.Fatalf("signed out: %v", p2.Blockers)
	}
}

// Jules: the CLI takes no starting branch, so the briefing asks Jules to check out the
// handoff branch (and the plan says so); jules remote new starts the session; its patch
// comes back committed on top of the handoff branch.
func TestHandoffToJulesAndBack(t *testing.T) {
	w := newVendorWorld(t, "jules")
	w.dirty()
	ctx := context.Background()
	a := w.app()
	inv, p := planTo(t, a, "jules", a.HandoffDefaults("jules"))
	defer inv.Close()
	hp := p.Handoff
	ask := "`git fetch origin " + hp.Branch + " && git checkout " + hp.Branch + "`"
	if len(p.Blockers) > 0 || !strings.Contains(hp.Brief, ask) || !strings.Contains(hp.Brief, "in a clone of github.com/example/demo") ||
		!strings.Contains(strings.Join(hp.Notes, "\n"), "Jules can't be told which branch to start from") || len(hp.Limits) == 0 {
		t.Fatalf("plan: %v %+v", p.Blockers, hp)
	}
	res, err := a.Apply(ctx, p, move.Input{}, nil)
	if err != nil {
		t.Fatalf("apply: %v %+v", err, res.Handoff)
	}
	hr := res.Handoff
	if hr.URL != "https://jules.google.com/session/"+hr.Session || !hr.Pushed {
		t.Fatalf("result: %+v", hr)
	}
	if log := agentLog(t); !strings.Contains(log, "jules remote new --repo example/demo --session [hopsesh] This task continues a Claude Code session") {
		t.Fatalf("jules ran as:\n%s", log)
	}
	s, err := fakecloud.Open(w.store).Get(hr.Session)
	if err != nil || s.Branch != hp.Branch || s.Base != hr.Snapshot {
		t.Fatalf("Jules started from %q (the briefing asked for %s): %v", s.Branch, hp.Branch, err)
	}
	if err := fakecloud.Work(fakecloud.Proc{Vars: map[string]string{}}, hr.Session, false); err != nil {
		t.Fatal(err)
	}
	inv2 := a.Scan(ctx, app.ScanOptions{})
	defer inv2.Close()
	e := cloudEntryFor(t, inv2, "jules", hr.Session)
	// Jules's words do not come back: the conversation plan says to take the code.
	if p, _, err := a.Plan(ctx, inv2, e, "", move.Options{}); err != nil || !hasBlocker(p, "only the code") {
		t.Fatalf("with the conversation: %+v %v", p, err)
	}
	fp, _, err := a.Plan(ctx, inv2, e, "", move.Options{CodeOnly: true})
	if err != nil || len(fp.Blockers) > 0 {
		t.Fatalf("bring-back plan: %+v %v", fp, err)
	}
	f := fp.Fetch
	if !f.Diff || f.CloudBranch != hp.Branch || f.BranchState != move.BranchPushed || f.LocalBranch != "hopsesh/from/jules/"+hr.Session {
		t.Fatalf("fetch plan: %+v", f)
	}
	fres, err := a.Apply(ctx, fp, move.Input{}, nil)
	if err != nil || fres.Fetch.Outcome != move.FetchCode {
		t.Fatalf("fetched: %+v %v", fres, err)
	}
	for _, file := range []string{"cloud-work/" + hr.Session + ".md", "parser.go"} {
		if _, err := os.Stat(filepath.Join(f.Worktree, file)); err != nil {
			t.Errorf("the worktree lacks %s", file)
		}
	}
	if parent := w.git(f.Worktree, "log", "-1", "--format=%P"); parent != hr.Snapshot {
		t.Fatalf("the patch is not on top of the handoff branch: %s", parent)
	}
	if _, err := a.Undo(ctx, fres.Journal, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.Worktree); !os.IsNotExist(err) {
		t.Fatal("undo leaves the worktree")
	}
}

// Devin: `devin --cloud -p` from the handoff worktree, the session found in devin list
// (its reply names none); its pull request's branch comes back.
func TestHandoffToDevin(t *testing.T) {
	w := newVendorWorld(t, "devin")
	ctx := context.Background()
	a := w.app()
	inv, p := planTo(t, a, "devin", a.HandoffDefaults("devin"))
	defer inv.Close()
	hp := p.Handoff
	if len(p.Blockers) > 0 || !strings.Contains(hp.Brief, "git checkout "+hp.Branch) {
		t.Fatalf("plan: %v %+v", p.Blockers, hp)
	}
	res, err := a.Apply(ctx, p, move.Input{}, nil)
	if err != nil || !strings.HasPrefix(res.Handoff.Session, "devin-") || !strings.HasPrefix(res.Handoff.URL, "https://app.devin.ai/sessions/") {
		t.Fatalf("apply: %v %+v", err, res.Handoff)
	}
	if log := agentLog(t); !strings.Contains(log, "devin --cloud --respect-workspace-trust false -p -- [hopsesh] ") {
		t.Fatalf("devin ran as:\n%s", log)
	}
	if err := fakecloud.Work(fakecloud.Proc{Vars: map[string]string{}}, res.Handoff.Session, false); err != nil {
		t.Fatal(err)
	}
	inv2 := a.Scan(ctx, app.ScanOptions{})
	defer inv2.Close()
	e := cloudEntryFor(t, inv2, "devin", res.Handoff.Session)
	fp, _, err := a.Plan(ctx, inv2, e, "", move.Options{CodeOnly: true})
	if err != nil || len(fp.Blockers) > 0 || !strings.HasPrefix(fp.Fetch.LocalBranch, "hopsesh/from/devin/") || fp.Fetch.BranchState != move.BranchPushed {
		t.Fatalf("bring-back plan: %+v %v", fp, err)
	}
}

// Amp: `amp -ox` starts an orb thread on the repository's project; the thread comes back as
// text, here into Codex, which the user picked; no code comes down.
func TestHandoffToAmpAndBack(t *testing.T) {
	w := newVendorWorld(t, "amp")
	ctx := context.Background()
	a := w.app()
	inv, p := planTo(t, a, "amp", a.HandoffDefaults("amp"))
	defer inv.Close()
	hp := p.Handoff
	if len(p.Blockers) > 0 || !strings.Contains(hp.Brief, "git checkout "+hp.Branch) {
		t.Fatalf("plan: %v %+v", p.Blockers, hp)
	}
	res, err := a.Apply(ctx, p, move.Input{}, nil)
	if err != nil || !strings.HasPrefix(res.Handoff.Session, "T-") || res.Handoff.URL != "https://ampcode.com/threads/"+res.Handoff.Session {
		t.Fatalf("apply: %v %+v", err, res.Handoff)
	}
	if log := agentLog(t); !strings.Contains(log, "amp -ox [hopsesh] ") || !strings.Contains(log, " --project example/demo --title ") {
		t.Fatalf("amp ran as:\n%s", log)
	}
	if err := fakecloud.Work(fakecloud.Proc{Vars: map[string]string{}}, res.Handoff.Session, false); err != nil {
		t.Fatal(err)
	}
	inv2 := a.Scan(ctx, app.ScanOptions{})
	defer inv2.Close()
	e := cloudEntryFor(t, inv2, "amp", res.Handoff.Session)
	fp, _, err := a.Plan(ctx, inv2, e, "codex", move.Options{})
	if err != nil || len(fp.Blockers) > 0 {
		t.Fatalf("bring-back plan: %+v %v", fp, err)
	}
	f := fp.Fetch
	if !f.Write || f.Writer != "Codex" || f.Fidelity != agent.FidText || f.CloudBranch != "" || f.Messages != 2 ||
		!strings.Contains(strings.Join(f.Loss, "\n"), "amp sync") {
		t.Fatalf("fetch plan: %+v", f)
	}
	fres, err := a.Apply(ctx, fp, move.Input{}, nil)
	if err != nil || fres.Fetch.Outcome != move.FetchComplete || !strings.HasPrefix(fres.Fetch.Key, "codex/") {
		t.Fatalf("fetched: %+v %v", fres, err)
	}
	inv3 := a.Scan(ctx, app.ScanOptions{Hosts: []string{"here"}})
	defer inv3.Close()
	thread, err := inv3.Find(app.ParseRef(fres.Fetch.Key))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(thread.Session.Path)
	if !strings.Contains(string(body), "I ran the tests in the orb; they pass.") {
		t.Fatalf("the thread lacks Amp's reply:\n%s", body)
	}
	if _, err := a.Undo(ctx, fres.Journal, false); err != nil {
		t.Fatal(err)
	}
}
