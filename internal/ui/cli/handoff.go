package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func addHandoffFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.Bool("history-file", false, "also commit the conversation as .hopsesh/handoff.md on the handoff branch (off by default: anyone who can see the branch can read it)")
	f.StringArray("untracked", nil, "carry these untracked files (a path or a glob; repeat it); credential-like files never go")
	f.Bool("bundle", false, "let the agent upload the repository itself instead of pushing a branch (where the cloud takes one)")
	f.String("brief-file", "", "use this text as the briefing instead of hopsesh's (it is checked for secrets too)")
	f.String("cleanup", "", "when hopsesh deletes the handoff branch: after-merge, on-undo or never (default from config)")
	f.String("env", "", "the cloud environment to run in, for a cloud that needs one (codex-cloud: its id or name; default: the one set for the repository)")
	f.Int("attempts", 0, "ask the cloud for this many attempts at once, where it runs them (codex-cloud: 1 to 4)")
	f.Bool("starting-diff", false, "send the changes with the cloud task as a starting diff instead of pushing a branch (codex-cloud; the session's branch must be on the remote as it is here)")
}

func handoffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "handoff [<machine>:][<agent>/]<id-or-title> | <cloud>:<id> | <cloud link> --to <cloud>",
		Short: "Hand a session off to an agent's cloud: a briefing and the code, never the conversation",
		Long: `Starts a session in an agent's cloud (claude-cloud: Claude Code on the web; codex-cloud: a Codex cloud
task; copilot-cloud: a GitHub Copilot cloud agent task, through gh; jules, devin, amp: a
session in Jules, Devin Cloud or an Amp orb, through their own CLIs) that continues this one.
No cloud takes a conversation, so the cloud agent gets a briefing (about 2,000 tokens, secrets
masked) as its first prompt, and the code on a branch: the session's own branch when it is
clean and already on GitHub, otherwise a new hopsesh/handoff/… branch with a snapshot of
the unpushed commits and changed files. Your checkout, index and branch are not touched.
Untracked files go only when you name them (--untracked), and files that look like
credentials (.env, *.pem, *.key, …) never go.

The session here is marked "continued in <cloud>" (--no-mark to skip). Nothing changes
until you confirm (or pass --yes). hopsesh undo deletes the branch and the mark; the cloud
session itself stays in the cloud until you archive it there. Allow the cloud first:
hopsesh clouds allow <cloud>. The cloud session uses your plan's allowance.

Claude Code starts a cloud session only in a terminal: after the push, hopsesh runs
claude --cloud "<briefing>" in this one (on standard error with --json), in its hand-off
folder for the repository, and reads the session's link Claude Code prints. The first time,
Claude Code asks whether you trust that folder: answer it there. If hopsesh sees no link, it
asks you to paste it. Without a terminal (run by an agent), the plan says so.

Codex cloud runs each task in an environment you made on the web: name it with --env (its
id or its name; hopsesh lists the ones your recent tasks used, and remembers the one you
pick for the repository). A small change on a branch already pushed can go with the task as
a starting diff (--starting-diff) instead of on a new branch. Only older Codex cloud
environments work: the codex command can't use environments made in today's Codex cloud
(chatgpt.com) yet.

The Copilot cloud agent starts from the handoff branch (gh agent-task create --base) and
opens its pull request against it. The jules, devin and amp commands can't name the branch
a session starts from, so the briefing asks the cloud agent to check it out first.

A cloud session hands on to another cloud through this machine (claude-cloud:<id> --to
codex-cloud, codex-cloud:<id> --to claude-cloud, …): hopsesh brings it here first, as
hopsesh pull does (Claude Code's teleport runs in this terminal: send a message in the
copy, and the next leg starts once you leave it), keeps that copy, and hands it off from here. The plan shows both legs and
what the trip loses; hopsesh undo takes both legs back. --in chooses the agent the session
is in here (where the cloud's text is written), --to-dir the repository's checkout here.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			to, _ := cmd.Flags().GetString("to")
			return handoff(cmd, args[0], to)
		},
	}
	f := cmd.Flags()
	f.String("to", "", "the cloud ("+strings.Join(cloudNames(), ", ")+")")
	f.String("note-file", "", "a handoff note for the briefing (what is done, what is next)")
	f.Bool("carry-rules", false, "add your instructions for every project of the session's agent to the briefing (the cloud does not see them otherwise)")
	f.Bool("no-mark", false, "do not mark the session here")
	f.Bool("dry-run", false, "show the plan and stop")
	f.Bool("yes", false, "do not ask for confirmation")
	f.Bool("json", false, "output JSON")
	f.String("in", "", "from a cloud: the agent the session is in here between the two clouds (default: as hopsesh pull)")
	f.String("to-dir", "", "from a cloud: the repository's checkout here (default: the one hopsesh finds)")
	addHandoffFlags(cmd)
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

// cloudNames are the clouds the registry's modules declare.
func cloudNames() []string {
	var out []string
	if modules == nil {
		return out
	}
	for _, m := range modules.All() {
		for _, c := range m.Spec().Clouds {
			out = append(out, c.Name)
		}
	}
	return out
}

// handoffOptions reads the hand-off flags over the cloud's configured defaults.
func (r *run) handoffOptions(cmd *cobra.Command, cloud string) (move.Options, error) {
	f := cmd.Flags()
	o := r.app.HandoffDefaults(cloud)
	if f.Changed("history-file") {
		o.HistoryFile, _ = f.GetBool("history-file")
	}
	if f.Changed("bundle") {
		o.Bundle, _ = f.GetBool("bundle")
	}
	o.Untracked, _ = f.GetStringArray("untracked")
	if v, _ := f.GetBool("no-mark"); v {
		o.Mark = false
	}
	o.CarryRules, _ = f.GetBool("carry-rules")
	o.Env, _ = f.GetString("env")
	o.Attempts, _ = f.GetInt("attempts")
	o.StartingDiff, _ = f.GetBool("starting-diff")
	if c, _ := f.GetString("cleanup"); c != "" {
		switch c {
		case move.CleanupAfterMerge, move.CleanupOnUndo, move.CleanupNever:
			o.Cleanup = c
		default:
			return o, fmt.Errorf("--cleanup is after-merge, on-undo or never, not %q", c)
		}
	}
	for flag, dst := range map[string]*string{"note-file": &o.Note, "brief-file": &o.Brief} {
		if p, _ := f.GetString(flag); p != "" {
			b, err := os.ReadFile(p)
			if err != nil {
				return o, err
			}
			*dst = strings.TrimSpace(string(b))
		}
	}
	return o, nil
}

func handoff(cmd *cobra.Command, refArg, cloud string) error {
	r, err := newRun(cmd)
	if err != nil {
		return err
	}
	if !r.app.IsCloud(cloud) {
		return fmt.Errorf("unknown cloud %q (see hopsesh clouds)", cloud)
	}
	opt, err := r.handoffOptions(cmd, cloud)
	if err != nil {
		return err
	}
	if from, id, ok := r.cloudRef(refArg); ok {
		return r.hop(cmd, from, id, cloud, opt)
	}
	ref := app.ParseRef(refArg)
	inv := r.scanFor(cmd, ref.Machine, ref)
	defer inv.Close()
	e, err := inv.Find(ref)
	if err != nil {
		return explainMissing(err, inv)
	}
	ctx, cancel := ctxTimeout(30)
	defer cancel()
	p, err := r.app.PlanHandoff(ctx, inv, e, cloud, opt)
	if err != nil {
		return err
	}
	dry, _ := cmd.Flags().GetBool("dry-run")
	if r.jsonOut && dry {
		return r.emitJSON(p)
	}
	if !r.jsonOut {
		r.renderHandoffPlan(p)
	}
	if len(p.Blockers) > 0 {
		return fmt.Errorf("cannot hand off: %s", strings.Join(p.Blockers, "; "))
	}
	if dry {
		return nil
	}
	if !r.confirm("Hand off?") {
		if !r.interactive() && !r.yes {
			return errors.New("not confirmed: run interactively or pass --yes (hopsesh plan … --to <cloud> shows the plan only)")
		}
		return errors.New("cancelled")
	}
	progress := func(s string) { r.printf("  • %s\n", s) }
	if r.jsonOut {
		progress = nil
	}
	res, applyErr := r.app.Apply(ctx, p, move.Input{}, progress)
	if r.jsonOut {
		out := map[string]any{"plan": p, "result": res}
		if res != nil {
			out["handoff"] = res.Handoff
		}
		if applyErr != nil {
			out["error"] = applyErr.Error()
		}
		if err := r.emitJSON(out); err != nil {
			return err
		}
		return applyErr
	}
	if res != nil && res.Handoff != nil {
		r.renderHandedOff(res)
	}
	if applyErr == nil && r.app.RememberEnv(p) {
		if err := config.Save(r.app.Cfg); err != nil {
			r.printf("  ! Could not remember the environment for %s: %v\n", p.Handoff.Repo, err)
		} else if !r.jsonOut {
			r.printf("  The environment %s is now %s's (hopsesh clouds env).\n", p.Handoff.EnvName, p.Handoff.Repo)
		}
	}
	return applyErr
}

func (r *run) renderHandoffPlan(p *move.Plan) {
	hp := p.Handoff
	r.printf("Hand off %q (%s on %s) to %s\n", p.Title, p.Agent, p.Source.Location, hp.CloudTitle)
	r.printf("  carries   %s\n", hp.Conversation)
	note := ""
	if hp.Masked > 0 {
		note = fmt.Sprintf(", %d likely secret(s) masked", hp.Masked)
	}
	if hp.Edited {
		note += ", your text"
	}
	r.printf("  briefing  about %d tokens%s (see it with --json, or edit it with --brief-file)\n", hp.Tokens, note)
	if hp.Repo != "" {
		r.printf("  repo      %s\n", hp.Repo)
	}
	switch {
	case hp.Branch == "":
	case hp.Code == agent.ViaStartingDiff:
		r.printf("  code      %d changed file(s) as a starting diff, on branch %s (nothing is pushed)\n", len(hp.Tracked)+len(hp.Untracked), hp.Branch)
	case hp.Code == agent.ViaBundle:
		r.printf("  code      an upload by %s from branch %s (nothing is pushed)\n", hp.Agent, hp.Branch)
	case hp.Reuse:
		r.printf("  code      branch %s, already on the remote\n", hp.Branch)
	default:
		r.printf("  code      branch %s from %s", hp.Branch, short7(hp.Base))
		if hp.Unpushed > 0 {
			r.printf(" + %d unpushed commit(s)", hp.Unpushed)
		}
		if n := len(hp.Tracked) + len(hp.Untracked); n > 0 {
			r.printf(" + %d changed file(s)", n)
		}
		r.printf("\n")
	}
	for _, c := range hp.Offered {
		r.printf("  untracked %s stays (--untracked %s carries it)\n", c.Path, c.Path)
	}
	if len(hp.Withheld) > 0 {
		var names []string
		for _, w := range hp.Withheld {
			names = append(names, w.Path)
		}
		r.printf("  stays     %s (never sent)\n", strings.Join(names, ", "))
	}
	if hp.HistoryFile {
		r.printf("  history   the conversation goes on the branch as %s. %s\n", hp.HistoryPath, hp.HistoryWarning)
	}
	switch p.Mark {
	case move.MarkNow:
		r.printf("  mark      this session becomes %q\n", hp.MarkTitle)
	case move.MarkWhenStopped:
		r.printf("  mark      %q once it ends (it is still open)\n", hp.MarkTitle)
	}
	if hp.EnvNeeded {
		switch {
		case hp.Env != "":
			r.printf("  env       %s\n", hp.EnvName)
		default:
			for _, e := range hp.Envs {
				r.printf("  env?      --env %s   %s\n", e.Value, e.Label)
			}
		}
	}
	if hp.Attempts > 1 {
		r.printf("  attempts  %d at once\n", hp.Attempts)
	}
	for _, c := range hp.Checks {
		mark := map[string]string{"ok": "✓", "warn": "!", "err": "✗"}[c.State]
		r.printf("  %s %s\n", mark, c.Text)
	}
	if hp.BundleOffer != "" && hp.Code != agent.ViaBundle {
		r.printf("  → hand it off as an upload: add --bundle\n")
	}
	if hp.CanStartingDiff && hp.Code != agent.ViaStartingDiff {
		r.printf("  → %s: add --starting-diff\n", hp.StartingDiffOffer)
	}
	if hp.Terminal != "" {
		r.printf("  terminal  %s\n", hp.Terminal)
		if hp.Folder != "" {
			r.printf("            the folder: %s\n", hp.Folder)
		}
	}
	for _, l := range hp.Limits {
		r.printf("  · %s\n", l)
	}
	for _, n := range hp.Notes {
		r.printf("  · %s\n", n)
	}
	r.printf("  · %s\n", hp.Usage)
}

func (r *run) renderHandedOff(res *move.Result) {
	hr := res.Handoff
	for _, st := range hr.Steps {
		mark := map[string]string{move.StepDone: "✓", move.StepFailed: "✗", move.StepTodo: "○", move.StepSkipped: "–"}[st.State]
		line := st.Label
		if st.State == move.StepTodo && hr.Failed != "" {
			line += " · not done"
		}
		if st.Detail != "" {
			line += " · " + st.Detail
		}
		r.printf("  %s %s\n", mark, line)
	}
	if hr.Failed != "" {
		r.printf("\n✗ %s\n", hr.Message)
		if hr.Retry == "bundle" {
			r.printf("  Try it as an upload instead: add --bundle.\n")
		}
		r.printf("  Undo with: hopsesh undo %s\n", res.Journal)
		return
	}
	r.printf("\n✓ Handed off to %s\n", hr.CloudTitle)
	r.printf("  %s %s is running.\n\n  %s\n\n", capital(nonEmpty(hr.Noun, "session")), hr.Session, hr.URL)
	if hr.Pasted {
		r.printf("  (from the link you pasted)\n")
	}
	switch {
	case hr.Branch != "" && hr.Code == string(agent.ViaBranch):
		r.printf("  Branch %s on %s\n", hr.Branch, hr.Repo)
	case hr.Code == string(agent.ViaStartingDiff):
		r.printf("  The changes went with it as a starting diff, on %s\n", hr.Branch)
	}
	if hr.EnvName != "" {
		r.printf("  Environment %s\n", hr.EnvName)
	}
	if len(hr.Stayed) > 0 {
		r.printf("  Stayed here: %s\n", strings.Join(hr.Stayed, ", "))
	}
	switch res.Mark {
	case "done":
		r.printf("  This session is now marked %q.\n", hr.MarkText)
	case "pending":
		r.printf("  This session is still open; it is marked once it ends.\n")
	case "failed":
		r.printf("  ! Could not mark this session: %s\n", res.MarkError)
	}
	for _, w := range res.Warnings {
		r.printf("  ! %s\n", w)
	}
	r.printf("  When it finishes: hopsesh pull %s:%s brings it here.\n", hr.Cloud, hr.Session)
	if hr.NoFollowUp != "" {
		r.printf("  Follow-ups: %s\n", hr.NoFollowUp)
	}
	r.printf("  Undo with: hopsesh undo %s (%s)\n", res.Journal, hr.Manual)
}

// noFollowUpYet is what followup says while no cloud takes a follow-up from hopsesh.
const noFollowUpYet = "no cloud accepts a follow-up from hopsesh yet: Claude Code has no command that sends one outside its own terminal session, and the other clouds' commands have none. Write to the session on its own page"

// followers are the clouds whose module sends follow-ups (agent.CloudFollower).
func followers() []string {
	var out []string
	if modules == nil {
		return out
	}
	for _, m := range modules.All() {
		if _, ok := m.(agent.CloudFollower); !ok {
			continue
		}
		for _, c := range m.Spec().Clouds {
			out = append(out, c.Name)
		}
	}
	return out
}

// followupCmd is hidden while no cloud takes a follow-up (none does in this release): it
// stays out of help, completions and the skill, and run directly it says so and fails.
// The path behind it is kept for the first cloud whose module is an agent.CloudFollower.
func followupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Hidden: true,
		Use:    "followup <cloud>:<id> | <cloud link> <text>",
		Short:  "Send a message to a cloud session (it starts a turn there, on your plan)",
		Long: `Queues one message in a cloud session through its agent's own command and returns at once, for
a cloud whose command line can send one. The message starts a model turn in the cloud, which
uses your plan's allowance. Nothing is sent until you confirm (or pass --yes).

None of the clouds hopsesh reaches takes one yet: Claude Code 2.1 has no command that sends
one outside its own terminal session, so open the session on claude.ai to write to it, and
the Codex, gh, jules, devin and amp commands hopsesh drives have none either.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(followers()) == 0 {
				return errors.New(noFollowUpYet)
			}
			if len(args) != 2 {
				return fmt.Errorf("followup takes a cloud session and the text, not %d arguments", len(args))
			}
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			cloud, id, ok := r.cloudRef(args[0])
			if !ok || id == "" {
				return fmt.Errorf("%q names no cloud session: use <cloud>:<id> or the session's link", args[0])
			}
			text := strings.TrimSpace(args[1])
			if text == "" {
				return errors.New("a follow-up needs some text")
			}
			if !r.jsonOut {
				r.printf("Send to %s session %s:\n  %s\n", cloud, id, text)
			}
			if !r.confirm("Send it? It starts a turn in the cloud.") {
				if !r.interactive() && !r.yes {
					return errors.New("not confirmed: run interactively or pass --yes")
				}
				return errors.New("cancelled")
			}
			ctx, cancel := ctxTimeout(10)
			defer cancel()
			cs, err := r.app.FollowUp(ctx, cloud, id, text)
			if err != nil {
				return err
			}
			if r.jsonOut {
				return r.emitJSON(cs)
			}
			r.printf("✓ Sent. %s\n", cs.URL)
			return nil
		},
	}
	cmd.Flags().Bool("yes", false, "do not ask for confirmation")
	cmd.Flags().Bool("json", false, "output JSON")
	return cmd
}

// capital is s with its first letter in upper case.
func capital(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// hop hands a cloud session on to another cloud through this machine: the plan of both
// legs, then the bring-back (the driver's terminal command runs here) and the hand-off.
func (r *run) hop(cmd *cobra.Command, from string, id agent.SessionID, to string, opt move.Options) error {
	if id == "" {
		return fmt.Errorf("name the %s session: %s:<id> or its link", from, from)
	}
	via := ""
	if f := cmd.Flags().Lookup("in"); f != nil {
		via = f.Value.String()
	}
	if f := cmd.Flags().Lookup("to-dir"); f != nil && f.Value.String() != "" {
		opt.TargetDir = expandHome(f.Value.String())
	} else if wd, err := os.Getwd(); err == nil && isCheckout(wd) {
		opt.TargetDir = wd
	}
	inv := r.scanWith(cmd, "", app.ScanOptions{Hosts: []string{app.LocalName(), from, to}, GitFor: r.app.GitFor(app.Ref{Query: string(id)})})
	defer inv.Close()
	e, err := inv.CloudEntry(r.app, from, id)
	if err != nil {
		return err
	}
	ctx, cancel := ctxTimeout(30)
	defer cancel()
	p, err := r.app.PlanHop(ctx, inv, e, to, agent.ID(via), opt)
	if err != nil {
		return err
	}
	dry, _ := cmd.Flags().GetBool("dry-run")
	if r.jsonOut && dry {
		return r.emitJSON(p)
	}
	if !r.jsonOut {
		r.renderHopPlan(p)
	}
	if len(p.Blockers) > 0 {
		return fmt.Errorf("cannot hand on: %s", strings.Join(p.Blockers, "; "))
	}
	if dry {
		return nil
	}
	if !r.confirm("Hand it on?") {
		if !r.interactive() && !r.yes {
			return errors.New("not confirmed: run interactively or pass --yes (hopsesh plan … --to <cloud> shows the plan only)")
		}
		return errors.New("cancelled")
	}
	progress := func(s string) { r.printf("  • %s\n", s) }
	if r.jsonOut {
		progress = nil
	}
	res, applyErr := r.app.Apply(ctx, p, move.Input{}, progress)
	if res != nil && res.Hop != nil && res.Hop.Remembered {
		if err := config.Save(r.app.Cfg); err != nil && !r.jsonOut {
			r.printf("  ! Could not remember the environment: %v\n", err)
		}
	}
	if r.jsonOut {
		out := map[string]any{"plan": p, "result": res}
		if res != nil {
			out["hop"], out["handoff"] = res.Hop, res.Handoff
		}
		if applyErr != nil {
			out["error"] = applyErr.Error()
		}
		if err := r.emitJSON(out); err != nil {
			return err
		}
		return applyErr
	}
	if res != nil {
		r.renderHop(res)
	}
	return applyErr
}

func (r *run) renderHopPlan(p *move.Plan) {
	hp := p.Hop
	r.printf("Hand %q on from %s to %s, through %s\n", p.Title, hp.FromTitle, hp.ToTitle, hp.Via)
	for i, l := range hp.Legs {
		r.printf("  %d. %-10s %s → %s (%s): %s\n", i+1, l.Verb, nonEmpty(l.FromTitle, l.From), nonEmpty(l.ToTitle, l.To), l.Fidelity, l.Words)
	}
	r.printf("  carries   %s\n", hp.Conversation)
	r.printf("  code      %s\n", hp.Code)
	if fp := hp.Bring.Fetch; fp != nil {
		if fp.Worktree != "" {
			r.printf("  here      %s, a new worktree of %s\n", fp.Worktree, nonEmpty(fp.Checkout, fp.Repo))
		}
		if fp.Command != "" {
			r.printf("  runs      %s\n", fp.Command)
		}
	}
	if hp.Terminal != "" {
		r.printf("  ! %s\n", hp.Terminal)
	}
	if t := hp.Then; t != nil {
		if t.EnvNeeded {
			if t.Env != "" {
				r.printf("  env       %s\n", t.EnvName)
			} else {
				for _, e := range t.Envs {
					r.printf("  env?      --env %s   %s\n", e.Value, e.Label)
				}
			}
		}
		if p.Mark == move.MarkNow {
			r.printf("  mark      the copy here becomes %q\n", t.MarkTitle)
		}
		if t.Terminal != "" {
			r.printf("  terminal  %s\n", t.Terminal)
		}
	}
	checks := append([]move.Check(nil), hp.Bring.Fetch.Checks...)
	if hp.Then != nil {
		checks = append(checks, hp.Then.Checks...)
	}
	for _, c := range checks {
		mark := map[string]string{"ok": "✓", "warn": "!", "err": "✗"}[c.State]
		r.printf("  %s %s\n", mark, c.Text)
	}
	for _, l := range hp.Loss {
		r.printf("  · %s\n", l)
	}
	if hp.Then != nil {
		r.printf("  · %s\n", hp.Then.Usage)
	}
}

func (r *run) renderHop(res *move.Result) {
	h := res.Hop
	if h == nil {
		return
	}
	switch h.State {
	case move.HopWaiting:
		r.printf("\n%s\n  Run it in your terminal:\n\n  %s\n\n  Then: hopsesh clouds continue %s\n", h.Message, h.Command, res.Journal)
		return
	case move.HopFailed:
		if res.Handoff != nil {
			r.renderHandedOff(res)
		}
		r.printf("\n✗ %s\n", h.Message)
		return
	}
	if res.Fetch != nil {
		r.printf("\n✓ Here: %s (%s)\n", nonEmpty(h.Key, res.Fetch.Key), res.Fetch.Worktree)
	}
	if res.Handoff != nil {
		r.renderHandedOff(res)
	}
	r.printf("  Undo both legs with: hopsesh undo %s\n", res.Journal)
}
