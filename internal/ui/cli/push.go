package cli

import (
	"context"
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

func pushCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push [<agent>/]<id-or-title> <machine>",
		Short: "Send a session from this machine to another machine's hopsesh, in its own agent or (--in) another",
		Long: `Sends a session on this machine to another machine where hopsesh is installed and set to
receive sessions ([peer] receive = true in its configuration). hopsesh there finds or clones
the repository, installs or converts the session with its own agents, and keeps its own
undo record; this machine marks its copy and records where the work went.

Only the session's own files are sent: the other machine cannot read or run anything here.
Nothing changes until you confirm (or pass --yes). hopsesh undo <id> here undoes both sides.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error { return push(cmd, args[0], args[1]) },
	}
	f := cmd.Flags()
	f.String("operation-id", "", "idempotency key for retrying the same transfer")
	f.String("target-session", "", "explicit destination session when a branch has several replicas there")
	f.String("target-profile", "", "exact account profile ID on the destination machine")
	f.Bool("new-session", false, "create a new native session without selecting or modifying an existing destination copy")
	f.Bool("bounded", false, "create a bounded continuation on the same branch; preserve the original session and portable archive")
	f.String("in", "", "continue in this agent there ("+strings.Join(writerIDs(), ", ")+"; default: the session's own)")
	f.String("fidelity", "history", "for another agent: history (the conversation as text) or note (a briefing only)")
	f.Bool("native", false, "for another agent that can: replay exact tool calls as its own (experimental)")
	f.String("note-file", "", "a handoff note for the other agent's briefing")
	f.Bool("go", false, "start the continued session with \"Continue.\"")
	f.String("via", "", "for another agent: import (its own importer converts the session, where it has one; hopsesh adds its briefing) or hopsesh (hopsesh converts it; the default unless that agent is set to import)")
	f.Bool("carry-rules", false, "for another agent: add your instructions for every project of the session's agent to the briefing")
	f.String("to", "", "continue in this directory on the other machine instead of matching the repository")
	f.Bool("clone", false, "clone the repository there if it is missing")
	f.String("worktree", "auto", "auto, create or main (as for pull)")
	f.Bool("fork", false, "keep this session running (both continue) instead of handing off")
	f.Bool("rc", false, "turn the agent's remote control on there, where it has one")
	f.Bool("redact", false, "redact likely secrets in the copy")
	f.Bool("no-mark", false, "do not mark the copy here")
	f.Bool("notify", false, "record a durable movement notice (default from config; --notify=false disables it)")
	f.Bool("no-sync", false, "do not fetch or fast-forward the checkout there")
	f.Bool("push", false, "first push the session branch's unpushed commits from here")
	f.Bool("replace", false, "when the copy there changed too, replace it anyway")
	f.Bool("keep-both", false, "when both copies changed, keep both")
	f.Bool("dry-run", false, "show the plan and stop")
	f.Bool("yes", false, "do not ask for confirmation")
	f.Bool("json", false, "output JSON")
	return cmd
}

func push(cmd *cobra.Command, refArg, machine string) error {
	r, err := newRun(cmd)
	if err != nil {
		return err
	}
	to := r.app.Cfg.FindHost(machine)
	if to == nil || !to.Allowed {
		return fmt.Errorf("%s is not an allowed machine (hopsesh hosts allow %s)", machine, machine)
	}
	opt, err := r.pullOptions(cmd)
	if err != nil {
		return err
	}
	in, _ := cmd.Flags().GetString("in")
	ref := app.ParseRef(refArg)
	if ref.Machine != "" && ref.Machine != app.LocalName() && ref.Machine != "local" {
		return errors.New("push sends a session from this machine; to bring one here from elsewhere, use pull")
	}
	ref.Machine = ""
	inv := r.scanFor(cmd, "local", ref)
	defer inv.Close()
	e, err := inv.Find(ref)
	if err != nil {
		return err
	}
	ctx, cancel := ctxTimeout(30)
	defer cancel()
	p, err := r.app.StartPush(ctx, inv, e, *to, agent.ID(in), opt)
	if err != nil {
		return err
	}
	defer p.Close()
	dry, _ := cmd.Flags().GetBool("dry-run")
	if r.jsonOut && dry {
		return r.emitJSON(p.Plan)
	}
	if !r.jsonOut {
		if p.Pushed != "" {
			r.printf("Pushed %s first: %s\n", e.Git.Branch, p.Pushed)
		}
		r.renderPlan(p.Plan)
	}
	if len(p.Plan.Blockers) > 0 {
		return fmt.Errorf("cannot continue: %s", strings.Join(p.Plan.Blockers, "; "))
	}
	if dry {
		return nil
	}
	if !r.confirm("Proceed?") {
		if !r.interactive() && !r.yes {
			return errors.New("not confirmed: run interactively or pass --yes")
		}
		return errors.New("cancelled")
	}
	res, err := p.Commit(ctx)
	if err != nil {
		return err
	}
	if r.jsonOut {
		return r.emitJSON(map[string]any{"plan": p.Plan, "result": res})
	}
	r.renderPushResult(p.Plan, res)
	return nil
}

func (r *run) renderPushResult(p *move.Plan, pr *app.PushResult) {
	res := pr.Result
	if p.NoWork {
		r.printf("\n✓ %q already synchronized on %s: 0 new messages, 0 transfers.\n", p.Title, pr.Machine)
	} else if p.Kind == move.KindContinue {
		r.printf("\n✓ %q is prepared for %s on %s.\n", p.Title, p.Agent, pr.Machine)
	} else {
		r.printf("\n✓ %q is on %s: %d file(s), %s.\n", p.Title, pr.Machine, res.Files, move.Human(res.Bytes))
	}
	switch res.Mark {
	case "done":
		r.printf("  The copy here is marked.\n")
	case "pending":
		r.printf("  The copy here is still open; it is marked once it ends (on a later scan).\n")
	case "failed":
		r.printf("  ! Could not mark the copy here: %s\n", res.MarkError)
	}
	if res.SyncNote != "" {
		r.printf("  Code there: %s.\n", res.SyncNote)
	}
	for _, w := range res.Warnings {
		r.printf("  ! %s\n", w)
	}
	r.printf("  Undo both sides with: hopsesh undo %s\n\n", pr.Journal)
	r.printf("On %s, continue it with:\n\n  %s\n", pr.Machine, res.Command)
}

func peerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "peer",
		Short:  "Work with hopsesh on another machine over standard input and output (started by it over SSH)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if stdio, _ := cmd.Flags().GetBool("stdio"); !stdio {
				return errors.New("hopsesh peer is started by hopsesh on another machine, with --stdio")
			}
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			return r.app.Serve(context.Background(), os.Stdin, os.Stdout)
		},
	}
	cmd.Flags().Bool("stdio", false, "speak the peer protocol on standard input and output")
	return cmd
}

func receiveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "receive [on|off]",
		Short: "Show or set whether hopsesh on your other machines may send sessions here (hopsesh push)",
		Long: `When on, hopsesh on another machine that can reach this one over SSH may send sessions
here with hopsesh push. Only that session's files arrive; hopsesh here installs them with its
own agents and keeps its own undo record. Off by default.`,
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"on", "off"},
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				switch args[0] {
				case "on", "off":
					r.app.Cfg.Peer.Receive = args[0] == "on"
					if err := config.Save(r.app.Cfg); err != nil {
						return err
					}
				default:
					return fmt.Errorf("receive takes on or off, not %q", args[0])
				}
			}
			if r.app.Cfg.Peer.Receive {
				r.printf("Receiving sessions: on. Your other machines can send sessions here with hopsesh push.\n")
			} else {
				r.printf("Receiving sessions: off. Turn it on with: hopsesh receive on\n")
			}
			return nil
		},
	}
}
