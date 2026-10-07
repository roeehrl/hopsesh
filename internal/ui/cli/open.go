package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/launch"
	"github.com/roeehrl/hopsesh/internal/core/proc"
	"github.com/roeehrl/hopsesh/internal/core/termapp"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Opening a session in a terminal app: `hopsesh open <session>` resumes a session that
// is on this machine in a new tab of your terminal app (iTerm2, Terminal, Windows
// Terminal), or here with --here. A session that runs already is shown instead: hopsesh
// brings its tab forward, never starting a second copy on the same conversation.
// `hopsesh terminals` lists the terminal apps and sets which one hopsesh uses.

func openCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "open [<agent>/]<id-or-title>",
		Short: "Resume a session on this machine in a new tab of your terminal app, or show the tab it runs in",
		Long: `Resumes a session that is on this machine. By default it opens in a new tab of your
terminal app (hopsesh terminals shows which; iTerm2 opens a tab in its front window,
Terminal a new window); --here runs it in this terminal; --terminal picks the app.

When the session is already running, hopsesh shows its tab instead of starting a second
copy: it finds the tab from the agent's own process (or the one hopsesh started) and the
terminal it runs on, never from anything the tab shows. A session running in a terminal
hopsesh cannot show is reported, and nothing is opened.

On macOS, the first time hopsesh opens or shows a tab in iTerm2 or Terminal, macOS asks
whether hopsesh may control that app. If you say no, hopsesh opens Terminal instead, or
prints the command to copy.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return openSession(cmd, args[0]) },
	}
	f := cmd.Flags()
	f.String("terminal", "", "the terminal app to open it in (see hopsesh terminals)")
	f.Bool("app", false, "open this exact conversation in its installed desktop app")
	f.Bool("here", false, "run it in this terminal instead")
	f.Bool("json", false, "output JSON")
	return cmd
}

func openSession(cmd *cobra.Command, refArg string) error {
	inApp, _ := cmd.Flags().GetBool("app")
	if inApp && (cmd.Flags().Changed("here") || cmd.Flags().Changed("terminal")) {
		return errors.New("--app cannot be combined with --here or --terminal")
	}
	r, err := newRun(cmd)
	if err != nil {
		return err
	}
	ref := app.ParseRef(refArg)
	if ref.Machine != "" && ref.Machine != "local" && ref.Machine != app.LocalName() {
		return fmt.Errorf("open resumes sessions on this machine; bring it here first: hopsesh pull %s", refArg)
	}
	ref.Machine = ""
	inv := r.scanFor(cmd, "local", ref)
	defer inv.Close()
	e, err := inv.Find(ref)
	if err != nil {
		return explainMissing(err, inv)
	}
	if m := inv.Machine(e.Machine); m == nil || !m.Local {
		return fmt.Errorf("the session is on %s; bring it here first: hopsesh pull %s:%s", e.Machine, e.Machine, e.Session.Key)
	}
	ctx, cancel := ctxTimeout(1)
	defer cancel()
	key := e.Session.Key
	pid := 0
	if e.Live.State == agent.Live {
		pid = e.Live.PID
	}
	f, found, ferr := r.app.SessionTab(ctx, key, pid)
	if found && !inApp {
		if err := r.app.ShowTab(ctx, f); err != nil {
			return fmt.Errorf("it is open in %s, but hopsesh could not show its tab: %w", f.Name(), err)
		}
		if r.jsonOut {
			return r.emitJSON(map[string]any{"shown": f.Name(), "session": key.String()})
		}
		r.printf("%q is already open in %s; hopsesh brought its tab forward.\n", e.Session.Title, f.Name())
		return nil
	}
	if !inApp && (e.Live.State == agent.Live || r.app.Running(key)) {
		msg := fmt.Sprintf("%q is already running in a terminal hopsesh cannot show; quit it there first", e.Session.Title)
		if ferr != nil {
			msg += " (" + ferr.Error() + ")"
		}
		return errors.New(msg)
	}
	c, err := r.app.Resume(inv, e, agent.ResumeOptions{App: inApp})
	if err != nil {
		return err
	}
	if inApp {
		if r.jsonOut {
			return r.emitJSON(c)
		}
		if len(c.Argv) == 0 {
			return errors.New("desktop command unavailable")
		}
		command := proc.CommandContext(ctx, c.Argv[0], c.Argv[1:]...)
		command.Dir = c.Dir
		command.Env = append(host.Without(os.Environ(), c.Unset), c.Env...)
		return command.Run()
	}
	l := app.Launch{Kind: termapp.KindSession, Run: c, Key: key,
		Labels: termapp.Labels{Title: e.Session.Title, Agent: e.AgentName, Machine: e.Machine}}
	here, _ := cmd.Flags().GetBool("here")
	name, _ := cmd.Flags().GetString("terminal")
	if here && name != "" {
		return errors.New("--here and --terminal: pick one")
	}
	var t termapp.Terminal
	switch {
	case name != "":
		if t, err = r.app.TerminalByID(name); err != nil {
			return err
		}
	case here:
	case r.app.Cfg.ResumeIn() == config.ResumeHere:
		here = true
	case r.app.Cfg.ResumeIn() == config.ResumeAsk && r.interactive() && !r.jsonOut:
		mine := r.app.MyTerminal(ctx)
		r.printf("Resume %q in %s (t) or here (h)? [t] ", e.Session.Title, mine.Name())
		line, _ := bufio.NewReader(r.in).ReadString('\n')
		here = strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "h")
	}
	if here {
		return r.runInThisTerminal(l)
	}
	prog, err := os.Executable()
	if err != nil {
		return err
	}
	o, err := r.app.OpenInTerminal(ctx, prog, t, l)
	if err != nil {
		if errors.Is(err, termapp.ErrNoTerminal) {
			return fmt.Errorf("%w. Run it yourself:\n\n  %s", err, launch.Shell(c, "", launch.DefaultShell()))
		}
		return err
	}
	if r.jsonOut {
		return r.emitJSON(map[string]any{"opened": o.Terminal, "fellBack": o.FellBack, "reason": o.Reason, "session": key.String()})
	}
	if o.FellBack {
		r.printf("Opened %q in %s instead (%s).\n", e.Session.Title, o.Terminal, o.Reason)
	} else {
		r.printf("Opened %q in %s.\n", e.Session.Title, o.Terminal)
	}
	return nil
}

func terminalsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "terminals",
		Short: "List the terminal apps hopsesh opens sessions in, and choose one",
		Long: `Lists the terminal apps hopsesh can open sessions and steps in on this machine, what
each can do, and the one it uses. --use picks one ("auto": the best installed one, iTerm2
before Terminal on macOS); --resume sets where the app resumes sessions: here (its own
hopsesh Terminal window, the app's default; for hopsesh open, this terminal), terminal
(your terminal app) or ask.

hopsesh never changes the terminal apps' own settings: it does not turn on iTerm2's
Python API, install its Claude Code integration, or write profiles.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := ctxTimeout(1)
			defer cancel()
			use, _ := cmd.Flags().GetString("use")
			resume, _ := cmd.Flags().GetString("resume")
			if use != "" || resume != "" {
				c := r.app.Cfg
				if use == "auto" {
					c.Terminal.App = ""
				} else if use != "" {
					if _, err := r.app.TerminalByID(use); err != nil {
						return err
					}
					c.Terminal.App = use
				}
				if resume != "" {
					c.Terminal.Resume = resume
				}
				if err := c.Check(); err != nil {
					return err
				}
				if err := config.Save(&c); err != nil {
					return err
				}
				r.app.Cfg = c
			}
			mine := r.app.MyTerminal(ctx)
			apps := r.app.TerminalApps(ctx)
			if r.jsonOut {
				return r.emitJSON(map[string]any{"apps": apps, "app": r.app.Cfg.Terminal.App, "using": mine.ID(), "resume": r.app.Cfg.ResumeIn()})
			}
			for _, a := range apps {
				state := "not installed"
				if a.Installed {
					state = "installed"
				}
				var can []string
				if a.Caps.Tabs {
					can = append(can, "tabs")
				} else {
					can = append(can, "windows")
				}
				if a.Caps.Find && a.Caps.Focus {
					can = append(can, "show a running session")
				}
				if a.Caps.Labels {
					can = append(can, "labels")
				}
				mark := "  "
				if a.ID == mine.ID() {
					mark = "▸ "
				}
				r.printf("%s%-18s %-16s %-14s %s\n", mark, a.Name, a.ID, state, strings.Join(can, ", "))
			}
			chosen := r.app.Cfg.Terminal.App
			if chosen == "" {
				chosen = "auto"
			}
			r.printf("\nUsing %s (terminal.app = %s); sessions resume: %s.\n", mine.Name(), chosen, r.app.Cfg.ResumeIn())
			return nil
		},
	}
	f := cmd.Flags()
	f.String("use", "", "the terminal app to use (an id above, or auto)")
	f.String("resume", "", "where the app resumes sessions: terminal, here or ask")
	f.Bool("json", false, "output JSON")
	return cmd
}
