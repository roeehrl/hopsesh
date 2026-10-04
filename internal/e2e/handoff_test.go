package e2e

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/internal/testkit"
	"github.com/roeehrl/hopsesh/internal/testkit/fakecloud"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// handoffWorld is the demo home (Claude Code sessions in git/demo) with the stand-in
// claude on PATH, the repository's GitHub remote a local bare repository with main pushed,
// and work in progress in the checkout: a commit not pushed, a changed file, an untracked
// note and credential-like files.
type handoffWorld struct {
	*cloudWorld
	origin fakecloud.Origin
}

const demoSession = "0b6c6a8e-1d2f-4c3b-9a7e-5f4d3c2b1a01"

func newHandoffWorld(t *testing.T) *handoffWorld {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in origin's hook assumes a POSIX system")
	}
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	w := &handoffWorld{cloudWorld: &cloudWorld{t: t, home: filepath.Join(root, "home"), store: filepath.Join(root, "cloud")}}
	w.repo = filepath.Join(w.home, "git", "demo")
	if err := testkit.DemoHome(w.home); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin")
	self, _ := os.Executable()
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(self, filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	gitConfig := filepath.Join(root, "gitconfig")
	env := testkit.Env(w.home)
	for k, v := range map[string]string{"PATH": bin + ":" + testPath(), "HOPSESH_MACHINE": "here", "FAKE_CLOUD_DIR": w.store, "FAKE_CLOUD_FAIL": "",
		"FAKE_AGENT_LOG": filepath.Join(root, "agents.log"), "GIT_CONFIG_GLOBAL": gitConfig, "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME": "Sam Doe", "GIT_AUTHOR_EMAIL": "sam@example.com", "GIT_COMMITTER_NAME": "Sam Doe", "GIT_COMMITTER_EMAIL": "sam@example.com",
		// As inside another agent's session: the driver must run without these.
		"CLAUDE_CODE_CHILD_SESSION": "1", "ANTHROPIC_API_KEY": "sk-ant-not-a-real-key", "CCR_FORCE_BUNDLE": ""} {
		env[k] = v
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	var err error
	if w.origin, err = fakecloud.NewOrigin(root, "https://github.com/example/demo.git"); err != nil {
		t.Fatal(err)
	}
	if err := w.origin.Redirect(gitConfig); err != nil {
		t.Fatal(err)
	}
	write := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(w.repo, p)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(w.repo, p), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("notes.txt", "the codeword is PLUM\n")
	w.git(w.repo, "add", "-A")
	w.git(w.repo, "commit", "-qm", "notes")
	w.git(w.repo, "push", "-q", "-u", "origin", "main")
	return w
}

// dirty adds work in progress.
func (w *handoffWorld) dirty() {
	write := func(p, s string) {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(w.repo, p)), 0o700)
		if err := os.WriteFile(filepath.Join(w.repo, p), []byte(s), 0o600); err != nil {
			w.t.Fatal(err)
		}
	}
	write("parser.go", "package demo\n")
	w.git(w.repo, "add", "parser.go")
	w.git(w.repo, "commit", "-qm", "unpushed")
	write("notes.txt", "the codeword is PLUM, and more\n")
	write("docs/plan.md", "the plan\n")
	write(".env", "TOKEN=secret\n")
	write("certs/dev.pem", "-----BEGIN PRIVATE KEY-----\n")
}

// planHandoff scans and plans handing the demo session off to Claude Code cloud.
func (w *handoffWorld) planHandoff(a *app.App, opt move.Options) (*app.Inventory, *move.Plan) {
	w.t.Helper()
	ctx := context.Background()
	inv := a.Scan(ctx, app.ScanOptions{})
	e, err := inv.Find(app.ParseRef("claude/" + demoSession))
	if err != nil {
		w.t.Fatal(err)
	}
	p, err := a.PlanHandoff(ctx, inv, e, "claude-cloud", opt)
	if err != nil {
		w.t.Fatal(err)
	}
	return inv, p
}

// statusOf is the user's checkout as git sees it: HEAD, its branch, the index and status.
func (w *handoffWorld) statusOf() string {
	idx, _ := os.ReadFile(filepath.Join(w.repo, ".git", "index"))
	return w.git(w.repo, "rev-parse", "HEAD") + "|" + w.git(w.repo, "symbolic-ref", "HEAD") + "|" +
		w.git(w.repo, "--no-optional-locks", "status", "--porcelain", "--untracked-files=all") + "|" + string(idx)
}

// The whole hand-off: a plan that says what goes and what stays, a snapshot on a handoff
// branch (the checkout untouched), the cloud session started with the briefing from a
// worktree on that branch, lineage and the mark; then the cloud works, and the session
// comes back with the cloud's work through the bring-back path; undo of the hand-off
// deletes the branch and the mark and owes the archive.
func TestHandoffToClaudeCloudAndBack(t *testing.T) {
	w := newHandoffWorld(t)
	w.dirty()
	a := w.app()
	ctx := context.Background()
	opt := a.HandoffDefaults("claude-cloud")
	opt.Untracked = []string{"docs/plan.md", "*.pem"}
	opt.Note = "fix TestParseQuoted next"
	inv, p := w.planHandoff(a, opt)
	defer inv.Close()
	hp := p.Handoff
	if p.Kind != move.KindHandoff || len(p.Blockers) > 0 {
		t.Fatalf("plan: %+v", p.Blockers)
	}
	if hp.Reuse || !strings.HasPrefix(hp.Branch, "hopsesh/handoff/") || !strings.HasSuffix(hp.Branch, "-0b6c6a8e") || hp.Code != agent.ViaBranch ||
		hp.Unpushed != 1 || strings.Join(hp.Untracked, ",") != "docs/plan.md" || len(hp.Withheld) != 2 || hp.HistoryFile {
		t.Fatalf("code: %+v", hp)
	}
	if !strings.HasPrefix(hp.Brief, "[hopsesh] This task continues a Claude Code session") || !strings.Contains(hp.Brief, "Not carried: .env, certs/dev.pem") ||
		!strings.Contains(hp.Brief, "fix TestParseQuoted next") || !strings.Contains(hp.Brief, hp.Branch) || hp.Tokens == 0 || hp.Tokens > 2100 {
		t.Fatalf("brief: %d tokens\n%s", hp.Tokens, hp.Brief)
	}
	if hp.MarkTitle != "↪ continued in Claude Code on claude-cloud" || p.Mark != move.MarkNow || len(hp.Steps) != 5 {
		t.Fatalf("mark and steps: %s %s %v", hp.MarkTitle, p.Mark, hp.Steps)
	}
	before := w.statusOf()
	res, err := a.Apply(ctx, p, move.Input{}, nil)
	if err != nil {
		t.Fatalf("apply: %v %+v", err, res.Handoff)
	}
	hr := res.Handoff
	if after := w.statusOf(); after != before {
		t.Fatalf("the user's checkout changed:\n%s\n---\n%s", before, after)
	}
	for _, st := range hr.Steps {
		if st.State != move.StepDone {
			t.Errorf("step %s: %s %s", st.Name, st.State, st.Detail)
		}
	}
	if !strings.HasPrefix(hr.Session, "session_01") || hr.URL != "https://claude.ai/code/"+hr.Session || !hr.Pushed || res.Mark != "done" {
		t.Fatalf("result: %+v", hr)
	}
	// The cloud got the briefing and the snapshot branch, from a worktree on it.
	s, err := fakecloud.Open(w.store).Get(hr.Session)
	if err != nil {
		t.Fatal(err)
	}
	if s.Branch != hp.Branch || s.Base != hr.Snapshot || s.Code != "branch" || !strings.HasPrefix(s.Messages[0].Text, "[hopsesh] ") {
		t.Fatalf("the cloud session: %+v", s)
	}
	if log, _ := os.ReadFile(os.Getenv("FAKE_AGENT_LOG")); !strings.Contains(string(log), "claude -p [hopsesh] This task continues") || !strings.Contains(string(log), "--cloud --output-format json") {
		t.Fatalf("the driver ran as: %s", log)
	}
	tree := w.git(w.repo, "ls-tree", "-r", "--name-only", hr.Snapshot)
	if !strings.Contains(tree, "docs/plan.md") || !strings.Contains(tree, "parser.go") || strings.Contains(tree, ".env") || strings.Contains(tree, "dev.pem") {
		t.Fatalf("snapshot tree:\n%s", tree)
	}
	if got := w.git(w.repo, "show", hr.Snapshot+":notes.txt"); got != "the codeword is PLUM, and more" {
		t.Fatalf("the snapshot carries the working tree's notes.txt: %q", got)
	}
	if repos.BranchExists(ctx, w.repo, hp.Branch) {
		t.Fatal("the driver's local branch stays")
	}
	if out := w.git(w.repo, "worktree", "list"); strings.Count(out, "\n") != 0 {
		t.Fatalf("the driver's worktree stays: %s", out)
	}

	// The scan: the session here is marked and points at the cloud session, which is
	// listed through the lineage with the original it came from.
	inv2 := a.Scan(ctx, app.ScanOptions{})
	defer inv2.Close()
	var local, cloud *app.Entry
	for i, e := range inv2.Entries {
		if string(e.Session.Key.Session) == demoSession {
			local = &inv2.Entries[i]
		}
		if e.Location.IsCloud() && string(e.Session.Key.Session) == hr.Session {
			cloud = &inv2.Entries[i]
		}
	}
	if local == nil || local.Session.Mark == nil || agent.MarkTitle(*local.Session.Mark, "") != hp.MarkTitle || local.Lineage == nil {
		t.Fatalf("the session here: %+v", local)
	}
	hop := local.Lineage.Hops[len(local.Lineage.Hops)-1]
	if hop.Kind != lineage.HopHandoff || hop.Fidelity != "brief" || hop.Code == nil || hop.Code.Branch != hp.Branch || hop.Code.Snapshot != hr.Snapshot {
		t.Fatalf("lineage: %+v", hop)
	}
	if tr := w.git(w.repo, "log", "-1", "--format=%(trailers:key=Hopsesh-Handoff,valueonly)", hr.Snapshot); tr != local.Lineage.Logical {
		t.Fatalf("the trailer is the lineage's id: %q vs %q", tr, local.Lineage.Logical)
	}
	if cloud == nil || cloud.Original != "here:claude/"+demoSession {
		t.Fatalf("the cloud session: %+v", cloud)
	}

	// The cloud works and pushes; the session comes back through the bring-back path.
	if err := fakecloud.Work(fakecloud.Proc{Vars: map[string]string{}}, hr.Session, false); err != nil {
		t.Fatal(err)
	}
	fp, _, err := a.Plan(ctx, inv2, *cloud, "", move.Options{})
	if err != nil || len(fp.Blockers) > 0 || !fp.Fetch.CanAppend {
		t.Fatalf("bring-back plan: %+v %v", fp, err)
	}
	fres, err := a.Apply(ctx, fp, move.Input{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out := w.terminal(fres.Command, ""); !strings.Contains(out, "Teleported") {
		t.Fatalf("teleport: %s", out)
	}
	f, err := a.Adopt(ctx, fres.Journal, true)
	if err != nil || f.Waiting() {
		t.Fatalf("adopt: %+v %v", f, err)
	}
	b := a.Brought(f)
	if b.Outcome != move.FetchComplete || b.Restored != 2 || !strings.HasPrefix(b.Branch, "hopsesh/from/claude-cloud/") {
		t.Fatalf("brought: %+v", b)
	}
	if _, err := os.Stat(filepath.Join(fp.Fetch.Worktree, "cloud-work", hr.Session+".md")); err != nil {
		t.Fatal("the cloud's work did not come back")
	}
	if _, err := os.Stat(filepath.Join(fp.Fetch.Worktree, "docs", "plan.md")); err != nil {
		t.Fatal("the cloud's branch builds on the snapshot")
	}
	if _, err := a.Undo(ctx, fres.Journal, false); err != nil {
		t.Fatal(err)
	}

	// Undo of the hand-off: the branch goes (with a lease), the mark goes, the cloud
	// session is a step the user owes.
	j, err := a.Undo(ctx, res.Journal, false)
	if err != nil {
		t.Fatal(err)
	}
	if out := w.git(w.origin.Bare, "for-each-ref", "refs/heads/"+hp.Branch); out != "" {
		t.Fatalf("undo leaves the branch: %s", out)
	}
	if len(j.Manual) != 1 || j.Manual[0].Key.Session != agent.SessionID(hr.Session) || j.Manual[0].URL != hr.URL {
		t.Fatalf("owed: %+v", j.Manual)
	}
	inv3 := a.Scan(ctx, app.ScanOptions{Hosts: []string{"here"}})
	defer inv3.Close()
	for _, e := range inv3.Entries {
		if string(e.Session.Key.Session) == demoSession && e.Session.Mark != nil {
			t.Fatal("undo leaves the mark")
		}
	}
	if after := w.statusOf(); after != before {
		t.Fatal("the user's checkout changed")
	}
}

// A clean branch already on the remote goes as it is: nothing is snapshotted or pushed.
func TestHandoffReusesAPushedBranch(t *testing.T) {
	w := newHandoffWorld(t)
	a := w.app()
	ctx := context.Background()
	inv, p := w.planHandoff(a, a.HandoffDefaults("claude-cloud"))
	defer inv.Close()
	hp := p.Handoff
	if len(p.Blockers) > 0 || !hp.Reuse || hp.Branch != "main" || strings.Join(hp.Steps, ",") != "start,lineage,mark" {
		t.Fatalf("plan: %+v %+v", p.Blockers, hp)
	}
	res, err := a.Apply(ctx, p, move.Input{}, nil)
	if err != nil || res.Handoff.Pushed || res.Handoff.Snapshot != "" {
		t.Fatalf("apply: %+v %v", res.Handoff, err)
	}
	s, _ := fakecloud.Open(w.store).Get(res.Handoff.Session)
	if s.Branch != "main" {
		t.Fatalf("the cloud cloned %s", s.Branch)
	}
	j, err := a.Undo(ctx, res.Journal, false)
	if err != nil || len(j.Manual) != 1 {
		t.Fatalf("undo: %+v %v", j, err)
	}
	if out := w.git(w.origin.Bare, "rev-parse", "main"); out == "" {
		t.Fatal("undo must never delete the user's own branch")
	}
}

// The failures, as the plan and the steps report them.
func TestHandoffFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("push-refused", func(t *testing.T) {
		w := newHandoffWorld(t)
		w.dirty()
		a := w.app()
		inv, p := w.planHandoff(a, a.HandoffDefaults("claude-cloud"))
		defer inv.Close()
		before := w.statusOf()
		t.Setenv("FAKE_CLOUD_FAIL", "push-refused")
		res, err := a.Apply(ctx, p, move.Input{}, nil)
		if err == nil || res.Handoff.Failed != move.StepPush || res.Handoff.Retry != "bundle" ||
			!strings.Contains(err.Error(), "GitHub refused the handoff branch (branch protection or permissions)") {
			t.Fatalf("push refused: %v %+v", err, res.Handoff)
		}
		if res.Handoff.Steps[0].State != move.StepDone || res.Handoff.Steps[2].State != move.StepTodo {
			t.Fatalf("steps: %+v", res.Handoff.Steps)
		}
		if w.statusOf() != before {
			t.Fatal("the checkout changed")
		}
		if _, err := a.Undo(ctx, res.Journal, false); err != nil {
			t.Fatalf("undo after a refused push: %v", err)
		}
	})
	t.Run("start-refused", func(t *testing.T) {
		w := newHandoffWorld(t)
		w.dirty()
		a := w.app()
		inv, p := w.planHandoff(a, a.HandoffDefaults("claude-cloud"))
		defer inv.Close()
		t.Setenv("FAKE_CLOUD_FAIL", "repo-mismatch")
		res, err := a.Apply(ctx, p, move.Input{}, nil)
		if err == nil || res.Handoff.Failed != move.StepStart || !strings.HasPrefix(err.Error(), "The branch was pushed, but Claude Code refused the session") ||
			!strings.HasSuffix(err.Error(), "Undo removes the branch.") {
			t.Fatalf("start refused: %v", err)
		}
		t.Setenv("FAKE_CLOUD_FAIL", "")
		if _, err := a.Undo(ctx, res.Journal, false); err != nil {
			t.Fatal(err)
		}
		if out := w.git(w.origin.Bare, "for-each-ref", "refs/heads/"+p.Handoff.Branch); out != "" {
			t.Fatal("undo leaves the pushed branch")
		}
	})
	t.Run("signed-out", func(t *testing.T) {
		w := newHandoffWorld(t)
		t.Setenv("FAKE_CLOUD_FAIL", "signed-out")
		a := w.app()
		inv, p := w.planHandoff(a, a.HandoffDefaults("claude-cloud"))
		defer inv.Close()
		if !hasBlocker(p, "Claude Code here uses an API key or another provider. Cloud sessions need a claude.ai login") {
			t.Fatalf("blockers: %v", p.Blockers)
		}
		for _, tg := range a.HandoffTargets(inv, findEntry(t, inv)) {
			if tg.Cloud == "claude-cloud" && (tg.OK || !strings.Contains(tg.Why, "claude.ai login")) {
				t.Fatalf("target: %+v", tg)
			}
		}
	})
	t.Run("not-eligible", func(t *testing.T) {
		w := newHandoffWorld(t)
		t.Setenv("FAKE_CLOUD_FAIL", "not-eligible")
		a := w.app()
		inv, p := w.planHandoff(a, a.HandoffDefaults("claude-cloud"))
		defer inv.Close()
		if !hasBlocker(p, "your Claude plan doesn't include cloud sessions") {
			t.Fatalf("blockers: %v", p.Blockers)
		}
	})
	t.Run("repo-not-github", func(t *testing.T) {
		w := newHandoffWorld(t)
		w.git(w.repo, "remote", "set-url", "origin", "https://gitlab.example.com/example/demo.git")
		w.git(w.repo, "config", "url."+w.origin.FileURL()+".insteadOf", "https://gitlab.example.com/example/demo.git")
		a := w.app()
		inv, p := w.planHandoff(a, a.HandoffDefaults("claude-cloud"))
		defer inv.Close()
		if !hasBlocker(p, "This repository's remote is gitlab.example.com. Claude Code cloud needs github.com to clone it; it can take it as an upload") ||
			!strings.Contains(p.Handoff.BundleOffer, "upload") {
			t.Fatalf("blockers: %v %q", p.Blockers, p.Handoff.BundleOffer)
		}
		for _, tg := range a.HandoffTargets(inv, findEntry(t, inv)) {
			if tg.Cloud == "claude-cloud" && (!tg.OK || !tg.Bundle || !strings.Contains(tg.Note, "Results can't be pushed back: the remote is gitlab.example.com")) {
				t.Fatalf("target: %+v", tg)
			}
			if tg.Cloud == "codex-cloud" && (tg.OK || tg.Why != "hopsesh does not reach Codex cloud yet") {
				t.Fatalf("codex: %+v", tg)
			}
		}
		// The upload: nothing is pushed; the snapshot stays on a local branch until undo.
		w.dirty()
		opt := a.HandoffDefaults("claude-cloud")
		opt.Bundle = true
		inv2, p2 := w.planHandoff(a, opt)
		defer inv2.Close()
		if len(p2.Blockers) > 0 || p2.Handoff.Code != agent.ViaBundle || strings.Contains(strings.Join(p2.Handoff.Steps, ","), "push") {
			t.Fatalf("bundle plan: %v %+v", p2.Blockers, p2.Handoff.Steps)
		}
		res, err := a.Apply(ctx, p2, move.Input{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		s, _ := fakecloud.Open(w.store).Get(res.Handoff.Session)
		if s.Code != "bundle" || res.Handoff.Pushed || !repos.BranchExists(ctx, w.repo, p2.Handoff.Branch) {
			t.Fatalf("bundle: %+v %+v", s, res.Handoff)
		}
		if out := w.git(w.origin.Bare, "for-each-ref", "refs/heads/hopsesh/"); out != "" {
			t.Fatalf("an upload pushes nothing: %s", out)
		}
		if _, err := a.Undo(ctx, res.Journal, false); err != nil {
			t.Fatal(err)
		}
		if repos.BranchExists(ctx, w.repo, p2.Handoff.Branch) {
			t.Fatal("undo leaves the upload's local branch")
		}
	})
	t.Run("no-remote", func(t *testing.T) {
		w := newHandoffWorld(t)
		w.git(w.repo, "remote", "remove", "origin")
		a := w.app()
		inv, p := w.planHandoff(a, a.HandoffDefaults("claude-cloud"))
		defer inv.Close()
		if !hasBlocker(p, "This session isn't in a git repository with a remote, so a cloud can't get its code") {
			t.Fatalf("blockers: %v", p.Blockers)
		}
	})
}

func hasBlocker(p *move.Plan, text string) bool {
	for _, b := range p.Blockers {
		if strings.Contains(b, text) {
			return true
		}
	}
	return false
}

func findEntry(t *testing.T, inv *app.Inventory) app.Entry {
	t.Helper()
	e, err := inv.Find(app.ParseRef("claude/" + demoSession))
	if err != nil {
		t.Fatal(err)
	}
	return e
}
