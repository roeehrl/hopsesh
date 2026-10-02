package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/convert"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func addPullFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.String("in", "", "continue in this agent ("+strings.Join(agentIDs(), ", ")+"; default: the session's own)")
	f.String("fidelity", "history", "for another agent: history (the conversation as text) or note (a briefing only)")
	f.Bool("native", false, "for another agent that can: replay exact tool calls as its own (experimental)")
	f.String("note-file", "", "a handoff note for the other agent's briefing")
	f.Bool("go", false, "start the continued session with \"Continue.\"")
	f.String("via", "", "for another agent that has its own importer (Codex): import to let it convert the session; hopsesh adds its briefing")
	f.Bool("carry-rules", false, "for another agent: add your instructions for every project of the session's agent to the briefing")
	f.String("to", "", "continue in this local directory instead of matching the repository")
	f.Bool("clone", false, "clone the repository if it is not on this machine")
	f.String("repos", "", "folder for clones (default from config, ~/git)")
	f.Bool("ghq", false, "clone into <repos>/<host>/<owner>/<repo>")
	f.String("worktree", "auto", "auto: recreate the worktree if the session used one; create: always use a worktree on the session's branch; main: use the main checkout")
	f.Bool("fork", false, "keep the source session running (both continue) instead of handing off")
	f.Bool("rc", false, "turn the agent's remote control on, where it has one")
	f.Bool("notify", false, "have the moved session tell the old one where the work went (agents that can)")
	f.Bool("redact", false, "redact likely secrets in the copy")
	f.Bool("stop-local", false, "if this session is open on this machine, quit it first")
	f.Bool("no-mark", false, "do not mark the copy left behind")
	f.Bool("no-sync", false, "do not fetch or fast-forward the checkout here to the session's commit")
	f.Bool("push", false, "first push the session branch's unpushed commits on the other machine")
	f.Bool("replace", false, "when the copy here changed too, replace it anyway (hopsesh undo brings it back)")
	f.Bool("keep-both", false, "when both copies changed, keep both (this one comes in as a separate session)")
	f.Bool("app", false, "open it in the agent's desktop app instead of the terminal (agents that can)")
	f.Bool("run", false, "start the agent in the new location when done")
	f.Bool("dry-run", false, "show the plan and stop")
	f.Bool("yes", false, "do not ask for confirmation")
	f.Bool("json", false, "output JSON")
}

func (r *run) pullOptions(cmd *cobra.Command) (move.Options, error) {
	f := cmd.Flags()
	o := r.app.DefaultOptions()
	o.TargetDir, _ = f.GetString("to")
	if v, _ := f.GetString("repos"); v != "" {
		o.ReposDir = expandHome(v)
	}
	if v, _ := f.GetBool("ghq"); v {
		o.GHQLayout = true
	}
	w, _ := f.GetString("worktree")
	o.Worktree = move.WorktreeMode(w)
	o.Clone, _ = f.GetBool("clone")
	o.Fork, _ = f.GetBool("fork")
	o.RemoteControl, _ = f.GetBool("rc")
	o.Notify, _ = f.GetBool("notify")
	o.Redact, _ = f.GetBool("redact")
	o.StopLocal, _ = f.GetBool("stop-local")
	o.App, _ = f.GetBool("app")
	o.Native, _ = f.GetBool("native")
	o.Go, _ = f.GetBool("go")
	o.CarryRules, _ = f.GetBool("carry-rules")
	switch via, _ := f.GetString("via"); via {
	case "", "hopsesh":
	case move.ViaImport:
		o.Via = move.ViaImport
	default:
		return o, fmt.Errorf("--via is import (the other agent's own importer), not %q", via)
	}
	if v, _ := f.GetBool("no-mark"); v {
		o.Mark = false
	}
	if v, _ := f.GetBool("no-sync"); v {
		o.SyncCode = false
	}
	if v, _ := f.GetBool("push"); v {
		o.Push = true
	}
	switch {
	case flagSet(f.GetBool("replace")):
		o.Conflict = move.ConflictReplace
	case flagSet(f.GetBool("keep-both")):
		o.Conflict = move.ConflictKeepBoth
	}
	fid, _ := f.GetString("fidelity")
	switch convert.Fidelity(fid) {
	case convert.History, convert.Note:
		o.Fidelity = convert.Fidelity(fid)
	default:
		return o, fmt.Errorf("--fidelity is history or note, not %q", fid)
	}
	if p, _ := f.GetString("note-file"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			return o, err
		}
		o.Note = strings.TrimSpace(string(b))
	}
	return o, nil
}

func flagSet(v bool, _ error) bool { return v }

func pullCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pull [<machine>:][<agent>/]<id-or-title>",
		Short: "Bring a session here, in its own agent or (--in) another, and print the command to continue",
		Long: `Moves a session from another machine (or folder) to this machine, or continues it in another
agent (--in). hopsesh finds or clones the repository, recreates a worktree if the session
used one, copies or converts the session, rewrites its paths, and prints the command to
continue it.

Without a machine name, hopsesh takes the newest copy of the session, which is how a
session comes back after working on it elsewhere. A session that went to another agent
and back gets only the new work added to its original, which stays byte for byte.

The copy left behind is marked (--no-mark to skip). The checkout here is fetched and, when
clean, fast-forwarded to the session's commit (--no-sync to skip). Nothing changes until
you confirm (or pass --yes).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return pull(cmd, args[0]) },
	}
	addPullFlags(cmd)
	return cmd
}

// planCmd is pull that only plans: it never writes, so agents may run it without asking.
func planCmd() *cobra.Command {
	cmd := pullCmd()
	cmd.Use = "plan [<machine>:][<agent>/]<id-or-title>"
	cmd.Short = "Show what pulling a session would do, without changing anything"
	cmd.Long = "Same as pull --dry-run: finds the session, plans the move or continuation and prints the plan (or JSON with --json). It never writes."
	cmd.RunE = func(c *cobra.Command, args []string) error {
		_ = c.Flags().Set("dry-run", "true")
		_ = c.Flags().Set("yes", "false")
		_ = c.Flags().Set("run", "false")
		return pull(c, args[0])
	}
	return cmd
}

func pull(cmd *cobra.Command, refArg string) error {
	r, err := newRun(cmd)
	if err != nil {
		return err
	}
	opt, err := r.pullOptions(cmd)
	if err != nil {
		return err
	}
	in, _ := cmd.Flags().GetString("in")
	ref := app.ParseRef(refArg)
	inv := r.scan(cmd, ref.Machine, false)
	defer inv.Close()
	e, err := inv.Find(ref)
	if err != nil {
		return explainMissing(err, inv)
	}
	ctx, cancel := ctxTimeout(30)
	defer cancel()
	p, input, err := r.app.Plan(ctx, inv, e, agent.ID(in), opt)
	if err != nil {
		return err
	}
	dry, _ := cmd.Flags().GetBool("dry-run")
	if r.jsonOut && dry {
		return r.emitJSON(p)
	}
	if !r.jsonOut {
		r.renderPlan(p)
	}
	if len(p.Blockers) > 0 && p.Repo.Action == move.RepoNeedsClone && !dry && r.interactive() && !r.yes {
		if r.confirm(fmt.Sprintf("Clone %s into %s now?", p.Repo.Identity, p.Repo.LocalPath)) {
			opt.Clone = true
			if p, input, err = r.app.Plan(ctx, inv, e, agent.ID(in), opt); err != nil {
				return err
			}
		}
	}
	if len(p.Blockers) > 0 {
		return fmt.Errorf("cannot continue: %s", strings.Join(p.Blockers, "; "))
	}
	if dry {
		return nil
	}
	if !r.confirm("Proceed?") {
		if !r.interactive() && !r.yes {
			return errors.New("not confirmed: run interactively or pass --yes (plan shows the plan only)")
		}
		return errors.New("cancelled")
	}
	progress := func(s string) { r.printf("  • %s\n", s) }
	if r.jsonOut {
		progress = nil
	}
	res, err := r.app.Apply(ctx, p, input, progress)
	if err != nil {
		var ge *repos.GitError
		if errors.As(err, &ge) && p.Repo.Action == move.RepoClone {
			return fmt.Errorf("%w\nClone it yourself, then run again with --to <path> (or put it under %s)", err, opt.ReposDir)
		}
		return err
	}
	if r.jsonOut {
		return r.emitJSON(map[string]any{"plan": p, "result": res, "command": res.Command})
	}
	r.renderResult(p, res)
	if run, _ := cmd.Flags().GetBool("run"); run {
		c := exec.Command(p.Resume.Argv[0], p.Resume.Argv[1:]...)
		if res.PromptFile != "" {
			if b, err := os.ReadFile(res.PromptFile); err == nil && len(p.Resume.Argv) > 0 {
				c = exec.Command(p.Resume.Argv[0], append(p.Resume.Argv[1:len(p.Resume.Argv)-1], string(b))...)
			}
		}
		c.Dir, c.Stdin, c.Stdout, c.Stderr = p.Resume.Dir, os.Stdin, os.Stdout, os.Stderr
		return c.Run()
	}
	return nil
}

// explainMissing adds unreachable machines to a "no session" error.
func explainMissing(err error, inv *app.Inventory) error {
	if !errors.Is(err, agent.ErrNotFound) {
		return err
	}
	var problems []string
	for _, m := range inv.Machines {
		if m.Status != app.StatusOK {
			problems = append(problems, m.Name+": "+m.Status)
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w (not reached: %s)", err, strings.Join(problems, ", "))
	}
	return err
}

func (r *run) renderPlan(p *move.Plan) {
	there := "here"
	if p.Target.Location != app.LocalName() {
		there = "on " + p.Target.Location
	}
	if p.Kind == move.KindContinue {
		r.printf("Continue %q from %s on %s in %s %s\n", p.Title, p.Continue.From, p.Source.Location, p.Agent, there)
	} else {
		r.printf("Move %q (%s) from %s to %s\n", p.Title, p.Agent, p.Source.Location, strings.TrimPrefix(there, "on "))
	}
	r.printf("  from      %s (%s)\n", p.Source.CWD, p.Key)
	r.printf("  to        %s\n", p.Target.CWD)
	switch p.Repo.Action {
	case move.RepoUse:
		r.printf("  repo      use %s", p.Repo.LocalPath)
	case move.RepoClone:
		r.printf("  repo      clone %s into %s", p.Repo.Remote, p.Repo.LocalPath)
	case move.RepoNeedsClone:
		r.printf("  repo      not here (%s)", p.Repo.Identity)
	case move.RepoDir:
		r.printf("  repo      the folder you chose")
	default:
		r.printf("  repo      none (same path)")
	}
	if p.Repo.Worktree != "" {
		r.printf(", worktree %s", p.Repo.Worktree)
	}
	r.printf("\n")
	if p.Sync != "" {
		r.printf("  code      %s\n", p.Sync)
	}
	if c := p.Continue; c != nil {
		switch c.Relation {
		case move.RelationAppend:
			r.printf("  session   add the new work to %s here (%s)\n", c.AppendTo.Key, c.AppendTo.Title)
		default:
			r.printf("  session   a new %s session (%s)\n", p.Agent, c.Fidelity)
		}
		r.printf("  carries   %s\n", c.Report.Summary)
		if n := p.NativeCopy; n != nil {
			r.printf("  also      keeps the %s session there byte for byte, so going back to %s adds only the new work\n", n.Agent, n.Agent)
		}
	} else {
		r.printf("  files     %d (%s)\n", len(p.Files.Files), move.Human(p.Bytes))
	}
	switch p.Mark {
	case move.MarkNow:
		r.printf("  left copy marked on %s\n", p.Source.Location)
	case move.MarkWhenStopped:
		r.printf("  left copy marked on %s once it ends (it is still open)\n", p.Source.Location)
	}
	for _, w := range p.Warnings {
		r.printf("  ! %s\n", w)
	}
	for _, b := range p.Blockers {
		r.printf("  ✗ %s\n", b)
	}
}

func (r *run) renderResult(p *move.Plan, res *move.Result) {
	if p.Kind == move.KindContinue {
		r.printf("\n✓ %q continues in %s.\n", p.Title, p.Agent)
	} else {
		r.printf("\n✓ %q is on this machine: %d file(s), %s.\n", p.Title, res.Files, move.Human(res.Bytes))
	}
	if res.Secrets.Total > 0 {
		verb := "found (not redacted)"
		if p.Options.Redact {
			verb = "redacted"
		}
		r.printf("  %d likely secret(s) %s.\n", res.Secrets.Total, verb)
	}
	switch {
	case res.PushError != "":
		r.printf("  ! Could not push on %s: %s\n", p.Source.Location, res.PushError)
	case res.Pushed != "":
		r.printf("  Pushed %s on %s.\n", p.Repo.SourceBranch, p.Source.Location)
	}
	if res.SyncNote != "" {
		r.printf("  Code: %s.\n", res.SyncNote)
	}
	switch res.Mark {
	case "done":
		r.printf("  The copy on %s is marked.\n", p.Source.Location)
	case "pending":
		r.printf("  The copy on %s is still open; it is marked once it ends (on a later scan).\n", p.Source.Location)
	case "failed":
		r.printf("  ! Could not mark the copy on %s: %s\n", p.Source.Location, res.MarkError)
	}
	for _, w := range res.Warnings {
		r.printf("  ! %s\n", w)
	}
	r.printf("  Undo with: hopsesh undo %s\n\n", res.Journal)
	r.printf("Continue it:\n\n  %s\n", res.Command)
	if res.Notice != "" {
		r.printf("\nTo tell the old session yourself, paste this there:\n  %s\n", res.Notice)
	}
	r.offerSkill()
}
