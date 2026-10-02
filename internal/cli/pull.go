package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/fsys"
	"github.com/roeehrl/hopsesh/internal/core/link"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/internal/core/sessions"
	"github.com/roeehrl/hopsesh/internal/engine"
	"github.com/roeehrl/hopsesh/internal/inventory"
)

func addTransportFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.String("to", "", "resume in this local directory instead of matching the repository")
	f.Bool("clone", false, "clone the repository if it is not on this machine")
	f.String("repos", "", "folder for clones (default from config, ~/git)")
	f.Bool("ghq", false, "clone into <repos>/<host>/<owner>/<repo>")
	f.String("worktree", "auto", "auto: recreate the worktree if the session used one; create: always use a worktree on the session's branch; main: use the main checkout")
	f.Bool("fork", false, "keep the source session running (both continue) instead of handing off")
	f.Bool("rc", false, "start the moved session with Remote Control on")
	f.Bool("notify", false, "have the moved session tell the old one where the work went (needs Remote Control on both)")
	f.Bool("redact", false, "redact likely secrets in the copy")
	f.Bool("other-account", false, "this machine uses a different Anthropic account (drops signed reasoning blocks)")
	f.Bool("memory", false, "also merge the project's auto-memory folder")
	f.Bool("dry-run", false, "show the plan and stop")
	f.Bool("run", false, "start claude in the new location when done")
	f.Bool("desktop", false, "open it in the Claude desktop app instead of the terminal (needs a claude with --desktop)")
	f.Bool("yes", false, "do not ask for confirmation")
	f.Bool("json", false, "output JSON")
}

func (a *app) transportOptions(cmd *cobra.Command) engine.Options {
	f := cmd.Flags()
	o := engine.Options{ReposDir: a.cfg.ReposDir, GHQLayout: a.cfg.Layout == "ghq", RemoteCtl: a.cfg.RemoteCtl,
		Fork: a.cfg.LivePolicy == "fork", Worktree: engine.WorktreeAuto}
	if v, _ := f.GetString("to"); v != "" {
		o.TargetDir = v
	}
	if v, _ := f.GetString("repos"); v != "" {
		o.ReposDir = expandHome(v)
	}
	if v, _ := f.GetBool("ghq"); v {
		o.GHQLayout = true
	}
	if v, _ := f.GetString("worktree"); v != "" {
		o.Worktree = engine.WorktreeMode(v)
	}
	if f.Changed("fork") {
		o.Fork, _ = f.GetBool("fork")
	}
	if f.Changed("rc") {
		o.RemoteCtl, _ = f.GetBool("rc")
	}
	o.Clone, _ = f.GetBool("clone")
	o.NotifyOld, _ = f.GetBool("notify")
	o.Redact, _ = f.GetBool("redact")
	o.DropThinking, _ = f.GetBool("other-account")
	o.CopyMemory, _ = f.GetBool("memory")
	return o
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, p[2:])
	}
	return p
}

func pullCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pull <machine>:<id-or-title>",
		Short: "Move a session to this machine and print the command to resume it",
		Long: `Moves a Claude Code session from another machine (or another folder on this one) to this
machine. hopsesh finds or clones the repository, recreates a worktree if the session used
one, copies the session, rewrites its paths, gives it a start prompt that explains the move
and asks Claude to check that nothing is missing, and prints the command to resume it.

The plan is shown first; nothing changes until you confirm (or pass --yes).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := newApp(cmd)
			if err != nil {
				return err
			}
			m, s, err := a.resolveSession(cmd, args[0])
			if m != nil {
				defer m.Close()
			}
			if err != nil {
				return err
			}
			return a.transport(cmd, m.PlanSource(context.Background()), s.Summary, s.Git, s.Live)
		},
	}
	addTransportFlags(cmd)
	return cmd
}

func importCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import <transcript.jsonl>",
		Short: "Install a session transcript file on this machine (e.g. one copied by hand)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := newApp(cmd)
			if err != nil {
				return err
			}
			file, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			sum, err := sessions.Summarize(fsys.Local{}, file)
			if err != nil {
				return err
			}
			home, _ := os.UserHomeDir()
			cfg, _ := sessions.LocalConfigDir()
			src := engine.Source{Host: inventory.LocalHostName(), OS: engine.LocalOS(), FS: fsys.Local{}, ConfigDir: cfg, Home: home}
			if v, _ := cmd.Flags().GetString("from-home"); v != "" {
				src.Home = v
				src.ConfigDir = v + "/.claude"
			}
			var git *repos.GitState
			if st, err := repos.ProbeLocal(context.Background(), []string{sum.CWD}); err == nil && len(st) == 1 && st[0].IsRepo {
				git = &st[0]
			}
			return a.transport(cmd, src, *sum, git, nil)
		},
	}
	addTransportFlags(cmd)
	cmd.Flags().String("from-home", "", "home directory of the machine the file came from (for path rewriting)")
	return cmd
}

func (a *app) transport(cmd *cobra.Command, src engine.Source, sum sessions.Summary, git *repos.GitState, live *sessions.LiveEntry) error {
	ctx, cancel := ctxTimeout(30)
	defer cancel()
	opt := a.transportOptions(cmd)
	tgt := inventory.LocalTarget(ctx)
	p, err := engine.BuildPlan(ctx, src, tgt, engine.Input{Summary: &sum, Git: git, Live: live}, opt)
	if err != nil {
		return err
	}
	desktop := func(p *engine.Plan) {
		if d, _ := cmd.Flags().GetBool("desktop"); d {
			if inventory.ClaudeSupports(ctx, tgt.ClaudePath, "--desktop") {
				p.Resume.Desktop = true
			} else {
				p.Warnings = append(p.Warnings, "the Claude Code installed here has no --desktop flag; the command opens it in the terminal")
			}
		}
	}
	desktop(p)
	dry, _ := cmd.Flags().GetBool("dry-run")
	if a.jsonOut && dry {
		return a.emitJSON(p)
	}
	if !a.jsonOut {
		a.renderPlan(p)
	}
	if len(p.Blockers) > 0 {
		if p.Repo.Action == "needs-clone" && !dry && a.interactive() && !a.yes {
			if a.confirm(fmt.Sprintf("Clone %s into %s now?", p.Repo.Identity, p.Repo.LocalPath)) {
				opt.Clone = true
				if p, err = engine.BuildPlan(ctx, src, tgt, engine.Input{Summary: &sum, Git: git, Live: live}, opt); err != nil {
					return err
				}
				desktop(p)
			}
		}
		if len(p.Blockers) > 0 {
			return fmt.Errorf("cannot continue: %s", strings.Join(p.Blockers, "; "))
		}
	}
	if dry {
		return nil
	}
	if !a.confirm("Proceed?") {
		if !a.interactive() && !a.yes {
			return errors.New("not confirmed: run interactively or pass --yes (use --dry-run to only see the plan)")
		}
		return errors.New("cancelled")
	}
	env := engine.Env{StateDir: config.StateDir(), Log: a.log}
	if !a.jsonOut {
		env.Progress = func(s string) { a.printf("  • %s\n", s) }
	}
	res, err := engine.Apply(ctx, p, src, env)
	if err != nil {
		var ge *repos.GitError
		if errors.As(err, &ge) && p.Repo.Action == "clone" {
			return fmt.Errorf("%w\nClone it yourself, then run again with --to <path> (or put it under %s)", err, opt.ReposDir)
		}
		return err
	}
	shell := link.DefaultShell()
	if a.jsonOut {
		return a.emitJSON(map[string]any{"plan": p, "result": res, "resumeCommand": p.Resume.Shell(shell), "argv": p.Resume.Argv()})
	}
	a.printf("\n✓ %q is on this machine.\n", p.Title)
	a.printf("  %d paths rewritten, %d file(s) (%s) copied", sumMap(res.Rewrite.Replacements), res.Copied, engine.Human(res.Bytes))
	if res.Secrets.Total > 0 {
		verb := "found (not redacted)"
		if p.Options.Redact {
			verb = "redacted"
		}
		a.printf(", %d likely secret(s) %s", res.Secrets.Total, verb)
	}
	a.printf("\n  Undo with: hopsesh undo %s\n\n", shortID(p.SessionID))
	a.printf("Start it (the first message explains the move and asks Claude to check nothing is missing):\n\n  %s\n", p.Resume.Shell(shell))
	if p.Options.NotifyOld && !p.Options.RemoteCtl {
		a.printf("\nTo tell the old session yourself, paste this there:\n  %s\n", link.OldSessionNotice(p.Resume.Name, p.TargetCWD, p.NewName, p.Resume.Fork))
	}
	if run, _ := cmd.Flags().GetBool("run"); run {
		argv := p.Resume.Argv()
		bin := tgt.ClaudePath
		if bin == "" {
			bin = "claude"
		}
		c := exec.Command(bin, argv[1:]...)
		c.Dir = p.TargetCWD
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		return c.Run()
	}
	return nil
}

func sumMap(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func (a *app) renderPlan(p *engine.Plan) {
	a.printf("Move %q\n", p.Title)
	a.printf("  from   %s  %s\n", p.SourceHost, p.SourceCWD)
	a.printf("  to     this machine  %s\n", p.TargetCWD)
	r := p.Repo
	switch r.Action {
	case "use":
		a.printf("  repo   %s found at %s", r.Identity, r.LocalPath)
		if r.LocalBranch != "" {
			a.printf(" (on %s)", r.LocalBranch)
		}
		a.printf("\n")
		for _, alt := range r.Alternatives {
			a.printf("         also at %s (use --to to pick it)\n", alt)
		}
	case "clone":
		a.printf("  repo   %s will be cloned into %s\n", r.Identity, r.LocalPath)
	case "needs-clone":
		a.printf("  repo   %s is not on this machine (suggested clone: %s)\n", r.Identity, r.LocalPath)
	case "dir":
		a.printf("  dir    %s (chosen with --to)\n", r.LocalPath)
	case "none":
		a.printf("  dir    not a git repository\n")
	}
	if r.SourceBranch != "" {
		where := "main folder"
		if r.ClaudeWT {
			where = "Claude worktree"
		} else if r.InWorktree {
			where = "worktree"
		}
		a.printf("  branch %s (%s on %s", r.SourceBranch, where, p.SourceHost)
		if r.InWorktree && r.MainBranch != "" {
			a.printf("; its main folder is on %s", r.MainBranch)
		}
		a.printf(")\n")
	}
	if r.Worktree != "" {
		a.printf("  worktree will be created: %s on %s\n", r.Worktree, r.SourceBranch)
	}
	a.printf("  files  %d (%s): transcript", len(p.Files), engine.Human(p.TotalBytes))
	kinds := map[string]int{}
	for _, f := range p.Files {
		kinds[f.Kind]++
	}
	for _, k := range []string{"subagent", "tool-result", "file-history", "memory", "sidecar"} {
		if kinds[k] > 0 {
			a.printf(", %d %s", kinds[k], k)
		}
	}
	a.printf("\n  paths\n")
	for _, m := range p.Mappings {
		a.printf("         %s → %s\n", m.From, m.To)
	}
	opts := []string{}
	if p.Resume.Fork {
		opts = append(opts, "fork (both continue)")
	} else if p.Live {
		opts = append(opts, "handoff")
	}
	if p.Options.RemoteCtl {
		opts = append(opts, "Remote Control as "+p.NewName)
	}
	if p.Options.NotifyOld {
		opts = append(opts, "notify old session")
	}
	if p.Options.Redact {
		opts = append(opts, "redact secrets")
	}
	if p.Options.DropThinking {
		opts = append(opts, "other account")
	}
	if len(opts) > 0 {
		a.printf("  after  %s\n", strings.Join(opts, ", "))
	}
	for _, w := range p.Warnings {
		a.printf("  ! %s\n", w)
	}
	for _, b := range p.Blockers {
		a.printf("  ✗ %s\n", b)
	}
	a.printf("\n")
}

func undoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "undo [session-id]",
		Short: "Reverse a completed pull (removes the copy, restores anything set aside)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := newApp(cmd)
			if err != nil {
				return err
			}
			list, err := engine.ListUndo(config.StateDir())
			if err != nil {
				return err
			}
			if len(args) == 0 {
				if a.jsonOut {
					return a.emitJSON(list)
				}
				if len(list) == 0 {
					a.printf("Nothing to undo.\n")
					return nil
				}
				for _, u := range list {
					a.printf("%s  %s from %s  → %s\n", u.Time.Local().Format("2 Jan 15:04"), shortID(u.SessionID), u.SourceHost, u.TargetFile)
				}
				a.printf("\nUndo one: hopsesh undo <session-id>\n")
				return nil
			}
			if !a.confirm("Undo the latest pull of " + args[0] + "?") {
				return errors.New("not confirmed (pass --yes)")
			}
			u, err := engine.Undo(config.StateDir(), args[0], a.log)
			if err != nil {
				return err
			}
			a.printf("Undone: removed %d file(s), restored %d. Clones and worktrees were kept.\n", len(u.Created), len(u.SetAside))
			return nil
		},
	}
	cmd.Flags().Bool("yes", false, "do not ask for confirmation")
	cmd.Flags().Bool("json", false, "output JSON")
	return cmd
}

func agentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "agent",
		Short:  "Print this machine's sessions as JSON (what the optional remote helper runs; read-only)",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := newApp(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := ctxTimeout(2)
			defer cancel()
			ms := a.scanner().Scan(ctx, nil, true)
			return a.emitJSON(map[string]any{"schema": "hopsesh.agent/v1", "machine": ms[0]})
		},
	}
	cmd.Flags().Bool("json", true, "output JSON (always on)")
	return cmd
}
