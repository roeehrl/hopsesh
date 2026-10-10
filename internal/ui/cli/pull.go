package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/convert"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/proc"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/internal/core/termapp"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func addPullFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.String("in", "", "continue in this agent ("+strings.Join(writerIDs(), ", ")+"; default: the session's own; from copilot-cloud or amp, whose text is written into an agent here: the one it was handed off from, else claude)")
	f.String("fidelity", "history", "for another agent: history (the conversation as text) or note (a briefing only)")
	f.Bool("bounded", false, "create a bounded continuation on the same branch; preserve the original session and portable archive")
	f.Bool("new-session", false, "create a new native session without selecting or modifying an existing destination copy")
	f.Bool("native", false, "for another agent that can: replay exact tool calls as its own (experimental)")
	f.String("note-file", "", "a handoff note for the other agent's briefing")
	f.Bool("go", false, "start the continued session with \"Continue.\"")
	f.String("via", "", "for another agent: import (its own importer converts the session, where it has one; hopsesh adds its briefing) or hopsesh (hopsesh converts it; the default unless that agent is set to import)")
	f.Bool("carry-rules", false, "for another agent: add your instructions for every project of the session's agent to the briefing")
	f.String("to", "", "continue in this local directory instead of matching the repository (from a cloud: the repository's checkout here; plan only: a cloud, to plan a hand-off)")
	f.Bool("code-only", false, "from a cloud: the session's branch only, in a worktree, without the conversation")
	f.Bool("append", false, "from a cloud: add its work to the session it was handed off from, when that is as it was left")
	f.Bool("clone", false, "clone the repository if it is not on this machine")
	f.String("repos", "", "folder for clones (default from config, ~/git)")
	f.Bool("ghq", false, "clone into <repos>/<host>/<owner>/<repo>")
	f.String("worktree", "auto", "auto: recreate the worktree if the session used one; create: always use a worktree on the session's branch; main: use the main checkout")
	f.Bool("fork", false, "keep the source session running (both continue) instead of handing off")
	f.Bool("rc", false, "turn the agent's remote control on, where it has one")
	f.Bool("notify", false, "record a durable movement notice (default from config; --notify=false disables it)")
	f.Bool("redact", false, "redact likely secrets in the copy")
	f.Bool("stop-local", false, "if this session is open on this machine, quit it first")
	f.Bool("no-mark", false, "do not mark the copy left behind")
	f.Bool("no-sync", false, "do not fetch or fast-forward the checkout here to the session's commit")
	f.Bool("push", false, "first push the session branch's unpushed commits on the other machine")
	f.Bool("replace", false, "when the copy here changed too, replace it anyway (hopsesh undo brings it back)")
	f.String("operation-id", "", "idempotency key for retrying the same transfer")
	f.String("target-profile", "", "destination account profile ID (hopsesh accounts list)")
	f.String("target-session", "", "explicit destination session when a branch has several replicas here")
	f.Bool("keep-both", false, "when both copies changed, keep both (this one comes in as a separate session)")
	f.Bool("app", false, "open it in the agent's desktop app instead of the terminal (agents that can)")
	f.Bool("run", false, "start the agent in the new location when done")
	f.String("terminal", "", "with --run: start it in a new tab of this terminal app instead of here (see hopsesh terminals)")
	f.Bool("dry-run", false, "show the plan and stop")
	f.Bool("yes", false, "do not ask for confirmation")
	f.Bool("json", false, "output JSON")
}

func (r *run) pullOptions(cmd *cobra.Command) (move.Options, error) {
	f := cmd.Flags()
	o := r.app.DefaultOptions()
	o.TargetDir, _ = f.GetString("to")
	o.OperationID, _ = f.GetString("operation-id")
	o.TargetSession, _ = f.GetString("target-session")
	o.NewReplica, _ = f.GetBool("new-session")
	o.TargetProfile, _ = f.GetString("target-profile")
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
	if f.Changed("notify") {
		o.Notify, _ = f.GetBool("notify")
	}
	o.Redact, _ = f.GetBool("redact")
	o.StopLocal, _ = f.GetBool("stop-local")
	o.App, _ = f.GetBool("app")
	o.Native, _ = f.GetBool("native")
	o.Bounded, _ = f.GetBool("bounded")
	o.Go, _ = f.GetBool("go")
	o.CarryRules, _ = f.GetBool("carry-rules")
	switch via, _ := f.GetString("via"); via {
	case "":
	case move.ViaImport, move.ViaHopsesh:
		o.Via = via
	default:
		return o, fmt.Errorf("--via is import or hopsesh, not %q", via)
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
		Use:   "pull [<machine>:][<agent>/]<id-or-title> | <cloud>:<id> | <cloud link>",
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
you confirm (or pass --yes).

From a cloud (<cloud>:<id>, or the session's link; hopsesh clouds lists them): hopsesh
brings the cloud's code into a new worktree of the repository (your checkout stays as it
is) and its conversation as far as the cloud gives it back. Claude Code cloud's comes whole
through claude --teleport, which hopsesh prints for your terminal (--run runs it here):
Claude Code saves its copy only after you send a message in it, so send one. Once the copy appears, hopsesh checks it (that it begins with the briefing hopsesh
sent, for a session hopsesh handed off), keeps the cloud's branch under
hopsesh/from/<cloud>/, and records it for undo. Codex cloud, Copilot and Amp come back as
text written into an agent here (--in), Jules and Devin as code only (--code-only). Allow
the cloud first: hopsesh clouds allow <cloud>.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return pull(cmd, args[0]) },
	}
	addPullFlags(cmd)
	return cmd
}

// planCmd is pull that only plans: it never writes, so agents may run it without asking.
func planCmd() *cobra.Command {
	cmd := pullCmd()
	cmd.Use = "plan [<machine>:][<agent>/]<id-or-title> | <cloud>:<id> | <cloud link>"
	cmd.Short = "Show what pulling a session would do, without changing anything"
	cmd.Long = "Same as pull --dry-run: finds the session, plans the move or continuation and prints the plan (or JSON with --json). It never writes."
	cmd.Long += "\n\nWith --to <cloud> (claude-cloud, codex-cloud, copilot-cloud, jules, devin, amp) it plans a hand-off to that cloud instead, as hopsesh handoff --dry-run does."
	addHandoffFlags(cmd)
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
	defer r.app.Catalog.Close()
	if to, _ := cmd.Flags().GetString("to"); r.app.IsCloud(to) {
		if dry, _ := cmd.Flags().GetBool("dry-run"); !dry {
			return fmt.Errorf("to hand a session off to %s, use: hopsesh handoff %s --to %s", to, refArg, to)
		}
		return handoff(cmd, refArg, to)
	}
	opt, err := r.pullOptions(cmd)
	if err != nil {
		return err
	}
	if cloud, id, ok := r.cloudRef(refArg); ok {
		return r.pullCloud(cmd, cloud, id, opt)
	}
	in, _ := cmd.Flags().GetString("in")
	ref := app.ParseRef(refArg)
	inv := r.scanFor(cmd, ref.Machine, ref)
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
		if p.Options.App && len(p.Resume.Argv) > 0 {
			c := p.Resume
			command := proc.CommandContext(ctx, c.Argv[0], c.Argv[1:]...)
			command.Dir = c.Dir
			command.Env = append(host.Without(os.Environ(), c.Unset), c.Env...)
			return command.Run()
		}
		l := app.Launch{Kind: termapp.KindSession, Run: p.Resume, Key: p.Placement.Key,
			Labels: termapp.Labels{Title: p.Title, Agent: p.Agent, Machine: app.LocalName()}}
		if res.PromptFile != "" && len(l.Run.Argv) > 1 {
			if b, err := os.ReadFile(res.PromptFile); err == nil {
				l.Run.Argv = append(l.Run.Argv[:len(l.Run.Argv)-1:len(l.Run.Argv)-1], string(b))
			}
		}
		if name, _ := cmd.Flags().GetString("terminal"); name != "" {
			return r.openElsewhere(ctx, name, l)
		}
		return r.runInThisTerminal(l)
	}
	return nil
}

// openElsewhere opens a launch in a new tab of the named terminal app.
func (r *run) openElsewhere(ctx context.Context, name string, l app.Launch) error {
	t, err := r.app.TerminalByID(name)
	if err != nil {
		return err
	}
	prog, err := os.Executable()
	if err != nil {
		return err
	}
	o, err := r.app.OpenInTerminal(ctx, prog, t, l)
	if err != nil {
		return err
	}
	if !r.jsonOut {
		if o.FellBack {
			r.printf("Opened in %s instead (%s).\n", o.Terminal, o.Reason)
		} else {
			r.printf("Opened in %s.\n", o.Terminal)
		}
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
	if p.Options.TargetSession != "" {
		r.printf("  returning %s · account %s\n", p.Options.TargetSession, p.Options.TargetProfile)
	}
	r.printf("  notice    %t (durable movement notice)\n", p.Options.Notify)
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
		r.renderComparison(c.Comparison)
		switch c.Relation {
		case move.RelationAppend:
			r.printf("  session   add the new work to %s here (%s)\n", c.AppendTo.Key, c.AppendTo.Title)
		default:
			r.printf("  session   a new %s session (%s)\n", p.Agent, c.Fidelity)
		}
		r.printf("  carries   %s\n", c.Report.Summary)
		r.printf("  capacity  %s\n", c.Report.ContextSummary())
		if c.Report.Archive != "" {
			r.printf("  archive   %s\n", c.Report.Archive)
		}
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
	if p.ReviewNewSession {
		r.printf("  This does not mean you changed accounts. To review a fresh session on the same lineage branch, remove --target-session and add --new-session. Keep the selected destination account. Both original sessions are preserved; independent destination work still requires --keep-both.\n")
	}
	if p.Continue != nil && (p.Conflict != "" || p.Options.Conflict == move.ConflictKeepBoth) {
		if p.Options.Conflict == move.ConflictKeepBoth {
			r.printf("  outcome   create a separate %s session; both originals preserved. Destination-only work is not combined.\n", p.Agent)
		} else {
			r.printf("  review    add --keep-both to review a separate %s session; both originals preserved.\n", p.Agent)
		}
		r.printf("  cancel    declining confirmation changes neither original.\n")
	}
}

func (r *run) renderComparison(c *move.Comparison) {
	if c == nil {
		return
	}
	if c.Verified {
		r.printf("  comparison %s · %d shared revisions · up to 2 sample messages per side\n", comparisonLine(c.Reason, 240), c.SharedRevisions)
	} else {
		r.printf("  comparison unavailable · %s\n", comparisonLine(c.Reason, 240))
	}
	for _, side := range []struct {
		label string
		data  move.ComparisonSide
	}{{"source", c.Source}, {"destination", c.Destination}} {
		s, id := side.data, side.data.Identity
		agentName := id.AgentName
		if agentName == "" {
			agentName = string(id.Agent)
		}
		profile := id.ProfileName
		if profile == "" {
			profile = id.Profile
		}
		if profile == "" {
			profile = "Default account"
		}
		machine := id.Machine
		if machine == "" {
			machine = id.MachineID
		}
		r.printf("  %s %s · %s on %s · %q\n", side.label, comparisonLine(agentName, 120), comparisonLine(profile, 120), comparisonLine(machine, 120), comparisonLine(id.Title, 160))
		r.printf("    session %s\n", comparisonLine(id.Key.String(), 0))
		if id.MachineID != "" && id.MachineID != machine {
			r.printf("    endpoint %s\n", comparisonLine(id.MachineID, 0))
		}
		if !c.Verified || !s.ExclusiveKnown {
			r.printf("    unique work unknown · %s\n", comparisonLine(s.Reason, 240))
			continue
		}
		r.printf("    unique: %d revisions · %d records · %d messages (%d user, %d assistant) · %d tools · %d other\n", s.Revisions, s.Counts.Nodes, s.Counts.Messages, s.Counts.UserMessages, s.Counts.AssistantMessages, s.Counts.Tools, s.Counts.Other)
		shown, shortened := 0, s.Truncated || s.PreviewOmitted > 0
		for _, sample := range s.Preview {
			if sample.Role != "user" && sample.Role != "assistant" || sample.Text == "" {
				continue
			}
			if shown == 2 {
				shortened = true
				break
			}
			text := comparisonLine(sample.Text, 240)
			shortened = shortened || sample.Truncated || text != comparisonLine(sample.Text, 0)
			r.printf("    %s: %s\n", sample.Role, text)
			shown++
		}
		if shortened || s.Counts.Messages > shown {
			r.printf("    excerpts shortened or omitted; counts above are exact\n")
		}
	}
}

// Core previews are allowlisted; terminal presentation also keeps identity and
// excerpts on one line and excludes control/format characters.
func comparisonLine(s string, limit int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if limit > 0 {
		runes := []rune(s)
		if len(runes) > limit {
			s = string(runes[:limit-1]) + "…"
		}
	}
	return s
}

func (r *run) renderResult(p *move.Plan, res *move.Result) {
	if p.NoWork {
		r.printf("\n✓ Conversation already synchronized; lineage receipts updated. 0 new messages, 0 transfers.\n")
	} else if p.Kind == move.KindContinue {
		r.printf("\n✓ %q is prepared for %s.\n", p.Title, p.Agent)
		r.printf("  %s\n", p.ContinuationHint())
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
		r.printf("\nMovement notice:\n  %s\n", res.Notice)
	}
	r.offerSkill()
}
