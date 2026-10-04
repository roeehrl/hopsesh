package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/proc"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func cloudsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clouds",
		Short: "List the agents' clouds, and which ones hopsesh may use",
		Long: `Lists the vendor clouds the installed agents reach (Claude Code cloud, Codex cloud), whether
you allowed hopsesh to use each, and what a read-only look found. hopsesh reaches a cloud
only through that agent's own command, signed in as you, and never one you have not allowed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			inv := r.scanClouds(cmd)
			defer inv.Close()
			if r.jsonOut {
				return r.emitJSON(inv.Clouds)
			}
			tw := tabwriter.NewWriter(r.out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "CLOUD\tTITLE\tALLOWED\tSTATUS\tDRIVER\tSESSIONS")
			for _, c := range inv.Clouds {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", c.Name, c.Title, map[bool]string{true: "yes", false: "no"}[c.Allowed], cloudWords(c),
					strings.TrimSpace(c.Driver+" "+c.Version), cloudCount(c))
			}
			tw.Flush()
			for _, c := range inv.Clouds {
				if h := cloudHint(c); h != "" {
					r.printf("\n%s: %s", c.Name, h)
				}
			}
			r.printf("\n\nAllow one with: hopsesh clouds allow <cloud>   Check one, read-only: hopsesh clouds test <cloud>\n")
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "output JSON")
	cmd.AddCommand(cloudsAllowCmd(true), cloudsAllowCmd(false), cloudsTestCmd())
	return cmd
}

// scanClouds reads this machine and the clouds (no other machine).
func (r *run) scanClouds(_ *cobra.Command) *app.Inventory {
	ctx, cancel := ctxTimeout(1)
	defer cancel()
	hosts := []string{app.LocalName()}
	for _, m := range r.app.Modules() {
		for _, c := range m.Spec().Clouds {
			hosts = append(hosts, c.Name)
		}
	}
	return r.app.Scan(ctx, app.ScanOptions{Hosts: hosts, SkipGit: true})
}

// cloudWords is a cloud's status in words.
func cloudWords(c *app.Cloud) string {
	switch c.Status {
	case app.CloudReady:
		if !c.Listable && !c.Fetchable {
			return "hopsesh does not reach it yet"
		}
		return "ready"
	case app.CloudNotAllowed:
		return "off"
	case app.CloudSignedOut:
		return "sign in"
	case app.CloudCLIMissing:
		return "not installed"
	case app.CloudCLIOld:
		return "older than tested"
	case app.CloudNotEligible:
		return "not available"
	}
	return c.Status
}

func cloudCount(c *app.Cloud) string {
	if c.Status != app.CloudReady && c.Status != app.CloudCLIOld || !c.Listable {
		return "–"
	}
	n := fmt.Sprint(c.Sessions)
	if c.Mirrors > 0 {
		n += fmt.Sprintf(" (%d mirrored)", c.Mirrors)
	}
	return n
}

func cloudHint(c *app.Cloud) string {
	switch {
	case c.Status == app.CloudReady && c.Partial:
		return fmt.Sprintf("%s has no list command, so these are the sessions hopsesh started or brought here, and Remote Control mirrors; `%s --teleport` shows the rest, and hopsesh pull <link> brings one", c.AgentName, c.Driver)
	case c.Hint != "":
		return c.Hint
	}
	return c.Error
}

func cloudsAllowCmd(allow bool) *cobra.Command {
	use, short := "allow <cloud>...", "Allow hopsesh to run an agent's cloud commands (listing is read-only)"
	if !allow {
		use, short = "deny <cloud>...", "Stop hopsesh from using a cloud"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			for _, name := range args {
				if !r.app.IsCloud(name) {
					return fmt.Errorf("unknown cloud %q (see hopsesh clouds)", name)
				}
				r.app.Cfg.SetCloudAllowed(name, allow)
				r.printf("%s: %s\n", name, map[bool]string{true: "allowed", false: "denied"}[allow])
			}
			return config.Save(r.app.Cfg)
		},
	}
}

func cloudsTestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test [<cloud>...]",
		Short: "Check a cloud through its agent's command, read-only: the login and the flags hopsesh uses",
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				for _, m := range r.app.Modules() {
					if _, ok := m.(agent.CloudTester); ok {
						for _, c := range m.Spec().Clouds {
							args = append(args, c.Name)
						}
					}
				}
			}
			ctx, cancel := ctxTimeout(1)
			defer cancel()
			type result struct {
				Cloud string `json:"cloud"`
				agent.CloudTest
				OK    bool   `json:"ok"`
				Error string `json:"error,omitempty"`
			}
			var out []result
			failed := false
			for _, name := range args {
				t, err := r.app.TestCloud(ctx, name)
				res := result{Cloud: name, CloudTest: t, OK: err == nil}
				for _, c := range t.Checks {
					res.OK = res.OK && c.OK
				}
				if err != nil {
					res.Error = err.Error()
				}
				failed = failed || !res.OK
				out = append(out, res)
			}
			if r.jsonOut {
				if err := r.emitJSON(out); err != nil {
					return err
				}
			} else {
				for _, res := range out {
					r.printf("%s\n", res.Cloud)
					for _, c := range res.Checks {
						r.printf("  %s %s\n", map[bool]string{true: "✓", false: "✗"}[c.OK], c.Text)
					}
					if res.Error != "" && len(res.Checks) == 0 {
						r.printf("  ✗ %s\n", res.Error)
					}
				}
			}
			if failed {
				return errors.New("a cloud check failed")
			}
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "output JSON")
	return cmd
}

// cloudRef reads a pull's argument as a cloud session: "<cloud>:<id>" ("<cloud>:" leaves
// the choice to the vendor's picker), or a link or id the modules recognise.
func (r *run) cloudRef(arg string) (cloud string, id agent.SessionID, ok bool) {
	ref := app.ParseRef(arg)
	if ref.Machine != "" && r.app.IsCloud(ref.Machine) {
		if ref.Query == "" {
			return ref.Machine, "", true
		}
		if _, cl, pid, ok := r.app.ParseCloudLink(ref.Query); ok && cl == ref.Machine {
			return cl, pid, true
		}
		return ref.Machine, agent.SessionID(ref.Query), true
	}
	if _, cl, pid, ok := r.app.ParseCloudLink(arg); ok {
		return cl, pid, true
	}
	return "", "", false
}

// pullCloud brings a cloud session here: the plan, a worktree, and the driver's command
// for the user's terminal (run here with --run, then adopted and checked).
func (r *run) pullCloud(cmd *cobra.Command, cloud string, id agent.SessionID, opt move.Options) error {
	in, _ := cmd.Flags().GetString("in")
	opt.CodeOnly, _ = cmd.Flags().GetBool("code-only")
	opt.AppendOriginal, _ = cmd.Flags().GetBool("append")
	if opt.TargetDir == "" {
		if wd, err := os.Getwd(); err == nil && isCheckout(wd) {
			opt.TargetDir = wd
		}
	}
	inv := r.scan(cmd, cloud, false)
	defer inv.Close()
	e, err := inv.CloudEntry(r.app, cloud, id)
	if err != nil {
		return err
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
		r.renderFetchPlan(p)
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
		return err
	}
	var b app.Brought
	if f, err := r.app.Adopt(ctx, res.Journal, false); err == nil {
		b = r.app.Brought(f)
	}
	run, _ := cmd.Flags().GetBool("run")
	if run && res.Fetch.Outcome == move.FetchWaiting {
		c := proc.Command(p.Fetch.Run.Argv[0], p.Fetch.Run.Argv[1:]...)
		c.Dir, c.Env = p.Fetch.Run.Dir, host.Without(os.Environ(), p.Fetch.Run.Unset)
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		if r.jsonOut {
			c.Stdout = os.Stderr // standard output is the JSON
		} else {
			r.printf("\nRunning %s here; hopsesh checks what it brought when it ends.\n\n", strings.Join(p.Fetch.Run.Argv, " "))
		}
		runErr := c.Run()
		f, err := r.app.Adopt(context.Background(), res.Journal, true)
		if err != nil {
			return err
		}
		b = r.app.Brought(f)
		if b.Outcome == move.FetchWaiting && runErr != nil {
			return fmt.Errorf("%s ended without bringing the session (%v); undo the worktree with: hopsesh undo %s", p.Fetch.Run.Argv[0], runErr, res.Journal)
		}
	}
	if !r.jsonOut {
		r.renderBrought(b, res.Journal)
	}
	out := map[string]any{"plan": p, "result": res, "brought": b}
	if run && b.Continue != "" && (b.Outcome == move.FetchComplete || b.Outcome == move.FetchPartial) {
		cp, cres, err := r.continueBrought(cmd, b, opt)
		if err != nil {
			return err
		}
		out["continued"] = map[string]any{"plan": cp, "result": cres}
	}
	if r.jsonOut {
		return r.emitJSON(out)
	}
	return nil
}

// continueBrought continues a copy just brought from a cloud in another agent, as pull
// --in does for any session here.
func (r *run) continueBrought(cmd *cobra.Command, b app.Brought, opt move.Options) (*move.Plan, *move.Result, error) {
	if !r.jsonOut {
		r.printf("\n")
	}
	inv := r.scan(cmd, "local", false)
	defer inv.Close()
	k, err := agent.ParseKey(b.Key)
	if err != nil {
		return nil, nil, err
	}
	var e *app.Entry
	for i := range inv.Entries {
		if inv.Entries[i].Session.Key == k && inv.Entries[i].Machine == app.LocalName() {
			e = &inv.Entries[i]
		}
	}
	if e == nil {
		return nil, nil, fmt.Errorf("the copy %s is not listed here yet; continue it with: hopsesh pull %s --in %s", b.Key, b.Key, b.Continue)
	}
	opt.TargetDir, opt.CodeOnly, opt.AppendOriginal = "", false, false
	ctx, cancel := ctxTimeout(30)
	defer cancel()
	p, input, err := r.app.Plan(ctx, inv, *e, agent.ID(b.Continue), opt)
	if err != nil {
		return nil, nil, err
	}
	if !r.jsonOut {
		r.renderPlan(p)
	}
	if len(p.Blockers) > 0 {
		return p, nil, fmt.Errorf("cannot continue: %s", strings.Join(p.Blockers, "; "))
	}
	if !r.confirm("Continue it in " + p.Agent + "?") {
		return p, nil, nil
	}
	progress := func(s string) { r.printf("  • %s\n", s) }
	if r.jsonOut {
		progress = nil
	}
	res, err := r.app.Apply(ctx, p, input, progress)
	if err != nil {
		return p, nil, err
	}
	if !r.jsonOut {
		r.renderResult(p, res)
	}
	return p, res, nil
}

// isCheckout reports whether dir is in a git checkout.
func isCheckout(dir string) bool {
	_, err := proc.Command("git", "-C", dir, "rev-parse", "--git-dir").Output()
	return err == nil
}

func (r *run) renderFetchPlan(p *move.Plan) {
	fp := p.Fetch
	verb := "Bring"
	if fp.CodeOnly {
		verb = "Bring the code of"
	}
	r.printf("%s %q here from %s", verb, p.Title, fp.CloudTitle)
	if fp.ContinueName != "" {
		r.printf(" and continue it in %s", fp.ContinueName)
	}
	r.printf("\n")
	if fp.Session != "" {
		r.printf("  session   %s\n", fp.Session)
	} else {
		r.printf("  session   chosen in %s's own picker\n", p.Agent)
	}
	r.printf("  carries   %s\n", fp.Conversation)
	if fp.Checkout != "" {
		r.printf("  repo      %s at %s\n", fp.Repo, fp.Checkout)
	}
	switch fp.BranchState {
	case move.BranchPushed:
		r.printf("  branch    %s, fetched into %s\n", fp.CloudBranch, fp.Ref)
	case move.BranchMissing:
		r.printf("  branch    %s is not on origin\n", fp.CloudBranch)
	default:
		if !fp.CodeOnly {
			r.printf("  branch    the session's own; %s fetches and checks it out\n", p.Agent)
		}
	}
	if fp.Worktree != "" {
		r.printf("  worktree  %s (a new one, at %s)\n", fp.Worktree, short7(fp.Base))
	}
	switch {
	case fp.FastForward:
		r.printf("  local     %s, here already: it moves forward to the cloud's work\n", fp.LocalBranch)
	case fp.LocalBranch != "" && fp.LocalBranch != fp.CloudBranch:
		r.printf("  local     %s, renamed from %s\n", fp.LocalBranch, fp.CloudBranch)
	case fp.Rename && !fp.CodeOnly:
		r.printf("  local     a claude/… branch is renamed under hopsesh/from/%s/\n", fp.Cloud)
	}
	if fp.Command != "" {
		r.printf("  runs      %s\n", fp.Command)
	}
	if fp.CanAppend && !fp.Append {
		r.printf("  also      --append adds the cloud's work to %q instead\n", fp.Original.Title)
	}
	for _, c := range fp.Checks {
		mark := map[string]string{"ok": "✓", "warn": "!", "err": "✗"}[c.State]
		r.printf("  %s %s\n", mark, c.Text)
	}
	for _, l := range fp.Loss {
		r.printf("  · %s\n", l)
	}
}

// renderBrought prints a fetch's result: the command for the user's terminal while it
// waits, then what came back.
func (r *run) renderBrought(b app.Brought, journal string) {
	switch b.Outcome {
	case move.FetchWaiting:
		r.printf("\n✓ The worktree is ready: %s\n", b.Worktree)
		r.printf("\nBring it here in your terminal:\n\n  %s\n\n", b.Command)
		r.printf("hopsesh picks up the copy when it appears (or on its next look, hopsesh ls), checks its\nmessage count, and renames the cloud's branch. Undo with: hopsesh undo %s\n", journal)
		return
	case move.FetchCode:
		r.printf("\n✓ The code of %q is in %s on %s.\n  Undo with: hopsesh undo %s\n", b.Title, b.Worktree, b.Branch, journal)
		return
	}
	mark := map[string]string{move.FetchComplete: "✓", move.FetchPartial: "!", move.FetchEmpty: "✗"}[b.Outcome]
	r.printf("\n%s %s\n", mark, b.Message)
	if b.Outcome != move.FetchComplete && b.Issue != "" {
		r.printf("  %s\n", b.Issue)
	}
	switch {
	case b.NoBranch:
		r.printf("  The cloud session never pushed its work, so there is no code to bring; the worktree is %s.\n", b.Worktree)
	case b.Renamed != "":
		r.printf("  The code is in %s on %s (renamed from %s).\n", b.Worktree, b.Branch, b.Renamed)
	case b.Branch != "":
		r.printf("  The code is in %s on %s.\n", b.Worktree, b.Branch)
	}
	if b.Outcome == move.FetchEmpty && b.MirrorOf != "" {
		machine, _, _ := strings.Cut(b.MirrorOf, ":")
		r.printf("  The session itself runs on %s: hop it here machine to machine instead (hopsesh pull %s).\n", machine, b.MirrorOf)
	}
	for _, w := range b.Warnings {
		r.printf("  ! %s\n", w)
	}
	r.printf("  Undo with: hopsesh undo %s\n", journal)
	if b.Outcome != move.FetchEmpty {
		r.printf("\nContinue it:\n\n  %s\n", b.Command)
		if b.ContinueName != "" {
			r.printf("\nOr in %s: hopsesh pull %s --in %s\n", b.ContinueName, b.Key, b.Continue)
		}
	}
}

func short7(c string) string {
	if len(c) > 7 {
		return c[:7]
	}
	return c
}
