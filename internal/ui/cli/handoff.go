package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/app"
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
}

func handoffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "handoff [<machine>:][<agent>/]<id-or-title> --to <cloud>",
		Short: "Hand a session off to an agent's cloud: a briefing and the code, never the conversation",
		Long: `Starts a session in an agent's cloud (claude-cloud: Claude Code on the web) that continues this one.
No cloud takes a conversation, so the cloud agent gets a briefing (about 2,000 tokens, secrets
masked) as its first prompt, and the code on a branch: the session's own branch when it is
clean and already on GitHub, otherwise a new hopsesh/handoff/… branch with a snapshot of
the unpushed commits and changed files. Your checkout, index and branch are not touched.
Untracked files go only when you name them (--untracked), and files that look like
credentials (.env, *.pem, *.key, …) never go.

The session here is marked "continued in … on <cloud>" (--no-mark to skip). Nothing changes
until you confirm (or pass --yes). hopsesh undo deletes the branch and the mark; the cloud
session itself stays in the cloud until you archive it there. Allow the cloud first:
hopsesh clouds allow <cloud>. The cloud session uses your plan's allowance.`,
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
	ref := app.ParseRef(refArg)
	inv := r.scan(cmd, ref.Machine, false)
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
	for _, c := range hp.Checks {
		mark := map[string]string{"ok": "✓", "warn": "!", "err": "✗"}[c.State]
		r.printf("  %s %s\n", mark, c.Text)
	}
	if hp.BundleOffer != "" && hp.Code != agent.ViaBundle {
		r.printf("  → hand it off as an upload: add --bundle\n")
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
	r.printf("  Session %s is running.\n\n  %s\n\n", hr.Session, hr.URL)
	if hr.Branch != "" && hr.Code == string(agent.ViaBranch) {
		r.printf("  Branch %s on %s\n", hr.Branch, hr.Repo)
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
	r.printf("  Send it a message: hopsesh followup %s:%s \"…\"\n", hr.Cloud, hr.Session)
	r.printf("  Undo with: hopsesh undo %s (%s)\n", res.Journal, hr.Manual)
}

func followupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "followup <cloud>:<id> | <cloud link> <text>",
		Short: "Send a message to a cloud session (it starts a turn there, on your plan)",
		Long: `Queues one message in a cloud session through its agent's own command (claude -p … --cloud <id>)
and returns at once. The message starts a model turn in the cloud, which uses your plan's
allowance. Nothing is sent until you confirm (or pass --yes).`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
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
