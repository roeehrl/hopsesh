package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/app"
)

// Changing a session's folder: `hopsesh cd [<machine>:]<session> <dir>` continues a session
// in another folder on its machine and keeps it the same session. On another machine,
// hopsesh there does it (it must run there and receive sessions).

func cdCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cd [<machine>:][<agent>/]<id-or-title> <dir>",
		Short: "Continue a session in another folder on its machine, keeping the session",
		Long: `Moves a session to another working directory on its machine and keeps it the same
session: its id, every message, tool call and reasoning block stay as the agent wrote
them. Each agent does it its own way:

  Claude Code  what its own /cd does: the transcript and its side folder move to the new
               folder's project folder, and a relocated record is added.
  Codex        Codex resumes the thread in the new folder (as codex resume -C does, with
               no turn) and records the folder itself. Codex's own /cd is not used: it
               starts a new thread.

The folder must be a checkout of the session's repository (another worktree or clone),
unless you pass --other-repo. The agent loads the new folder's instructions and settings
when the session resumes, and asks to trust the folder if it is new to it.

When the session is open and its agent can change its folder from inside it (Claude
Code's /cd), hopsesh tells you what to type there and waits until the session has moved.
Otherwise quit it first, or pass --stop-local and hopsesh quits it.

On another machine (studio:<session>), <dir> is a folder there, and hopsesh there plans and
makes the change: it must be set up there (hopsesh hosts setup) and receive sessions.

hopsesh undo reverses it, there too: Claude Code's files move back; Codex is asked to change
the folder back.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error { return changeDir(cmd, args[0], args[1]) },
	}
	f := cmd.Flags()
	f.Bool("other-repo", false, "allow a folder that is not a checkout of the session's repository")
	f.Bool("stop-local", false, "if the session is open, quit it first")
	f.Duration("wait", 10*time.Minute, "how long to wait for the change typed in the open session (with --json, waits only when given)")
	f.Bool("dry-run", false, "show the plan only")
	f.Bool("yes", false, "do not ask for confirmation")
	f.Bool("json", false, "output JSON")
	return cmd
}

func changeDir(cmd *cobra.Command, refArg, dir string) error {
	r, err := newRun(cmd)
	if err != nil {
		return err
	}
	defer r.app.Catalog.Close()
	ref := app.ParseRef(refArg)
	if ref.Machine != "" && ref.Machine != "local" && ref.Machine != app.LocalName() {
		return r.changeDirOn(cmd, ref, dir)
	}
	ref.Machine = ""
	inv := r.scanFor(cmd, "local", ref)
	defer inv.Close()
	e, err := inv.Find(ref)
	if err != nil {
		return explainMissing(err, inv)
	}
	f := cmd.Flags()
	var o app.DirOptions
	o.OtherRepo, _ = f.GetBool("other-repo")
	o.StopLocal, _ = f.GetBool("stop-local")
	ctx, cancel := ctxTimeout(2)
	defer cancel()
	c, err := r.app.PlanChangeDir(ctx, inv, e, expandHome(dir), o)
	if err != nil {
		return err
	}
	wait, _ := f.GetDuration("wait")
	return r.finishDir(ctx, cmd, inv, c, wait, f.Changed("wait"))
}

// finishDir shows a planned folder change on this machine, then makes it (or, for an open
// session its agent moves itself, waits for that), as cd and a same-machine pull --to do.
func (r *run) finishDir(ctx context.Context, cmd *cobra.Command, inv *app.Inventory, c *app.DirChange, wait time.Duration, waitAsked bool) error {
	dry, _ := cmd.Flags().GetBool("dry-run")
	if r.jsonOut && (dry || len(c.Plan.Blockers) > 0) {
		return r.emitJSON(c)
	}
	if !r.jsonOut {
		r.printDirPlan(c)
	}
	if len(c.Plan.Blockers) > 0 {
		return fmt.Errorf("cannot change the folder: %s", strings.Join(c.Plan.Blockers, "; "))
	}
	if dry {
		return nil
	}
	if c.Open && !c.StopFirst {
		if r.jsonOut && !waitAsked {
			// An agent reading JSON tells the user what to type; it waits only when asked.
			return r.emitJSON(c)
		}
		return r.waitInSession(c, wait)
	}
	if !r.confirm("Change the folder?") {
		if !r.interactive() && !r.yes {
			return errors.New("not confirmed: run interactively or pass --yes (--dry-run shows the plan only)")
		}
		return errors.New("cancelled")
	}
	id, err := r.app.ChangeDir(ctx, c)
	if err != nil {
		return err
	}
	return r.reportDir(inv, c, id)
}

func (r *run) printDirPlan(c *app.DirChange) {
	p := c.Plan
	r.printf("Change the folder of %q (%s)\n  %s\n→ %s\n", c.Entry.Session.Title, c.Entry.AgentName, p.From, p.To)
	if len(p.Moves) > 0 {
		r.printf("  moves %d file(s): %s → %s\n", len(p.Moves), p.Moves[0].From, p.Moves[0].To)
	}
	for _, x := range p.Records {
		r.printf("  adds %s\n", x)
	}
	for _, x := range p.Notes {
		r.printf("  note: %s\n", x)
	}
	for _, x := range p.Warnings {
		r.printf("  warning: %s\n", x)
	}
	for _, x := range p.Blockers {
		r.printf("  blocked: %s\n", x)
	}
}

// waitInSession tells the user what to type in the open session and waits for the agent
// to move it.
func (r *run) waitInSession(c *app.DirChange, wait time.Duration) error {
	if !r.jsonOut {
		r.printf("\nThe session is open. In it, type:\n\n  %s\n\nWaiting for %s to move it (up to %s; Ctrl-C to stop waiting)…\n", c.InSession, c.Entry.AgentName, wait)
	}
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	if err := r.app.WaitChangedDir(ctx, c, 2*time.Second); err != nil {
		return fmt.Errorf("%w; type %s in the session, then run this again to check", err, c.InSession)
	}
	if r.jsonOut {
		return r.emitJSON(map[string]any{"session": c.Entry.Session.Key.String(), "cwd": c.Plan.To, "method": "in-session"})
	}
	r.printf("Done: %s moved %q to %s. It is the same session (%s).\n", c.Entry.AgentName, c.Entry.Session.Title, c.Plan.To, c.Entry.Session.Key.Session)
	return nil
}

func (r *run) reportDir(inv *app.Inventory, c *app.DirChange, journalID string) error {
	e := c.Entry
	e.Session.CWD, e.Session.Path = c.Plan.To, c.Plan.Main
	if r.jsonOut {
		return r.emitJSON(map[string]any{"session": e.Session.Key.String(), "cwd": c.Plan.To, "journal": journalID, "method": c.Plan.Method})
	}
	r.printf("Done: %q is in %s now, the same session (%s). Undo: hopsesh undo %s\n", e.Session.Title, c.Plan.To, e.Session.Key.Session, journalID)
	if line, err := r.app.ResumeShell(inv, e); err == nil && line != "" {
		r.printf("Resume it:\n  %s\n", line)
	}
	return nil
}

// changeDirOn changes the folder of a session on another machine, through hopsesh there.
func (r *run) changeDirOn(cmd *cobra.Command, ref app.Ref, dir string) error {
	inv := r.scanFor(cmd, ref.Machine, ref)
	defer inv.Close()
	e, err := inv.Find(ref)
	if err != nil {
		return explainMissing(err, inv)
	}
	f := cmd.Flags()
	var o app.DirOptions
	o.OtherRepo, _ = f.GetBool("other-repo")
	o.StopLocal, _ = f.GetBool("stop-local")
	wait, _ := f.GetDuration("wait")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute+wait)
	defer cancel()
	rd, err := r.app.PlanChangeDirOn(ctx, e.Machine, e.Session.Key, dir, o)
	if err != nil {
		return err
	}
	defer rd.Close()
	c := &rd.Change
	dry, _ := f.GetBool("dry-run")
	if r.jsonOut && (dry || len(c.Plan.Blockers) > 0) {
		return r.emitJSON(c)
	}
	if !r.jsonOut {
		r.printf("On %s:\n", rd.Machine)
		r.printDirPlan(c)
	}
	if len(c.Plan.Blockers) > 0 {
		return fmt.Errorf("cannot change the folder: %s", strings.Join(c.Plan.Blockers, "; "))
	}
	if dry {
		return nil
	}
	if c.Open && !c.StopFirst {
		if r.jsonOut && !f.Changed("wait") {
			return r.emitJSON(c)
		}
		if !r.jsonOut {
			r.printf("\nThe session is open on %s. In it, type:\n\n  %s\n\nWaiting for %s to move it (up to %s; Ctrl-C to stop waiting)…\n", rd.Machine, c.InSession, c.Entry.AgentName, wait)
		}
		if err := rd.Wait(ctx, wait); err != nil {
			return fmt.Errorf("%w; type %s in the session on %s, then run this again to check", err, c.InSession, rd.Machine)
		}
		if r.jsonOut {
			return r.emitJSON(map[string]any{"session": c.Entry.Session.Key.String(), "machine": rd.Machine, "cwd": c.Plan.To, "method": "in-session"})
		}
		r.printf("Done: %s moved %q to %s on %s. It is the same session (%s).\n", c.Entry.AgentName, c.Entry.Session.Title, c.Plan.To, rd.Machine, c.Entry.Session.Key.Session)
		return nil
	}
	if !r.confirm("Change the folder?") {
		if !r.interactive() && !r.yes {
			return errors.New("not confirmed: run interactively or pass --yes (--dry-run shows the plan only)")
		}
		return errors.New("cancelled")
	}
	id, err := rd.Apply(ctx)
	if err != nil {
		return err
	}
	if r.jsonOut {
		return r.emitJSON(map[string]any{"session": c.Entry.Session.Key.String(), "machine": rd.Machine, "cwd": c.Plan.To, "journal": id, "method": c.Plan.Method})
	}
	r.printf("Done: %q is in %s on %s now, the same session (%s). Undo: hopsesh undo %s\n", c.Entry.Session.Title, c.Plan.To, rd.Machine, c.Entry.Session.Key.Session, id)
	r.printf("Resume it there, from here:\n  hopsesh terminals run %s --resume %s\n", rd.Machine, c.Entry.Session.Key.Session)
	return nil
}
