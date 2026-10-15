package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/peer"
	"github.com/roeehrl/hopsesh/sdk/agent"

	"github.com/roeehrl/hopsesh/internal/core/loginsvc"
	"github.com/roeehrl/hopsesh/internal/core/proc"
	"github.com/roeehrl/hopsesh/internal/core/serve"
)

func serveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run hopsesh in your login session, for your other machines",
		Long: `hopsesh serve runs in your login session (started at each login once installed with
hopsesh serve install, which hopsesh on another machine does when you set this machine up
from there). Your other machines reach it through SSH and hopsesh peer, as before; it
answers from where your sign-ins and apps are (on a Mac, the Keychain an SSH login cannot
read), and runs the sessions you continue here from another machine in terminals it
keeps, so they outlive the connection that shows them. It opens no network port.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			defer r.app.Catalog.Close()
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return r.app.RunServe(ctx)
		},
	}
	service := func(r *run) (loginsvc.Service, error) {
		home, err := os.UserHomeDir()
		if err != nil {
			return loginsvc.Service{}, err
		}
		exe, err := os.Executable()
		if err != nil {
			return loginsvc.Service{}, err
		}
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		return loginsvc.Service{Home: home, Program: exe, Args: []string{"serve"}, Log: filepath.Join(serve.Dir(home), "serve.log"),
			Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				return proc.CommandContext(ctx, name, args...).CombinedOutput()
			}}, nil
	}
	report := func(r *run, st loginsvc.Status) error {
		if r.jsonOut {
			return r.emitJSON(st)
		}
		switch {
		case st.Running:
			r.printf("hopsesh runs in your login session (%s); it starts at each login.\n", st.Program)
		case st.Registered && st.NoSession:
			r.printf("hopsesh starts at the next login here (%s); nobody is logged in now.\n", st.Program)
		case st.Registered:
			r.printf("hopsesh is set to start at login (%s), but is not running.\n", st.Program)
		default:
			r.printf("hopsesh does not start at login here.\n")
		}
		return nil
	}
	install := &cobra.Command{Use: "install", Short: "Start hopsesh serve at each login, and now", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		r, err := newRun(cmd)
		if err != nil {
			return err
		}
		defer r.app.Catalog.Close()
		s, err := service(r)
		if err != nil {
			return err
		}
		ctx, cancel := ctxTimeout(1)
		defer cancel()
		st, err := s.Install(ctx)
		if err != nil {
			return err
		}
		return report(r, st)
	}}
	uninstall := &cobra.Command{Use: "uninstall", Short: "Stop hopsesh serve and no longer start it at login", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		r, err := newRun(cmd)
		if err != nil {
			return err
		}
		defer r.app.Catalog.Close()
		s, err := service(r)
		if err != nil {
			return err
		}
		ctx, cancel := ctxTimeout(1)
		defer cancel()
		if err := s.Uninstall(ctx); err != nil {
			return err
		}
		r.printf("hopsesh no longer starts at login here.\n")
		return nil
	}}
	status := &cobra.Command{Use: "status", Short: "Show whether hopsesh serve starts at login and runs", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		r, err := newRun(cmd)
		if err != nil {
			return err
		}
		defer r.app.Catalog.Close()
		s, err := service(r)
		if err != nil {
			return err
		}
		ctx, cancel := ctxTimeout(1)
		defer cancel()
		return report(r, s.Check(ctx))
	}}
	relay := &cobra.Command{Use: "relay <mode>", Short: "Connect standard input and output to hopsesh serve (started over SSH)", Hidden: true, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		if args[0] != serve.ModeTerminal {
			return fmt.Errorf("unknown relay %q", args[0])
		}
		c, br, _, err := serve.Dial(context.Background(), home, args[0])
		if err != nil {
			return err
		}
		return serve.Proxy(c, br, os.Stdin, os.Stdout)
	}}
	for _, c := range []*cobra.Command{install, uninstall, status} {
		c.Flags().Bool("json", false, "print JSON")
	}
	cmd.AddCommand(install, uninstall, status, relay)
	return cmd
}

// handToServe hands this hopsesh peer conversation to hopsesh serve on this machine, when
// it runs: it answers from the login session. false when it does not run.
func handToServe() (bool, error) {
	if os.Getenv("HOPSESH_PEER_HERE") != "" {
		return false, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false, nil
	}
	c, br, _, err := serve.Dial(context.Background(), home, serve.ModePeer)
	if errors.Is(err, serve.ErrNotRunning) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, serve.Proxy(c, br, os.Stdin, os.Stdout)
}

func terminalAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach <machine> <terminal-id>",
		Short: "Show a terminal hopsesh runs on another machine here",
		Long: `Shows, in this terminal, a session hopsesh runs on another machine (hopsesh serve there):
type in it here; it keeps running there when this ends, and attaching again shows it.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			defer r.app.Catalog.Close()
			return attachHere(r, args[0], args[1])
		},
	}
}

// exitCode ends the command with a program's exit code.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("the program ended with code %d", int(e)) }

// ExitStatus is the exit code an error from the command line stands for (a remote
// terminal's program's own), when it is one.
func ExitStatus(err error) (int, bool) {
	var e exitCode
	if errors.As(err, &e) {
		return int(e), true
	}
	return 0, false
}

func hostsSetupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup <machine>",
		Short: "Set hopsesh up on a machine you added: install it, and start it at each login there",
		Long: `Copies this hopsesh to the machine over SSH (into ~/.local/bin; the same release,
verified, when the machine is of another kind), lets it receive sessions, and has it start
in your login session there at each login (no administrator rights). Then this machine sees
that machine's sign-ins, protects originals left there, and can run sessions there.
Nothing is installed anywhere unless you run this.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			defer r.app.Catalog.Close()
			ctx, cancel := ctxTimeout(5)
			defer cancel()
			out, err := r.app.SetUpMachine(ctx, args[0], func(s app.SetupStep) {
				if !r.jsonOut {
					r.printf("  %-8s %s: %s\n", s.State, s.Name, s.Detail)
				}
			})
			if r.jsonOut && out != nil {
				_ = r.emitJSON(out)
			}
			if err != nil {
				return err
			}
			if !r.jsonOut {
				if out.Login {
					r.printf("hopsesh %s runs in the login session on %s.\n", out.Version, args[0])
				} else {
					r.printf("hopsesh %s is set up on %s; it starts when someone logs in there.\n", out.Version, args[0])
				}
			}
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "print JSON")
	return cmd
}

func terminalRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <machine>",
		Short: "Run a session on another machine and work in it here",
		Long: `Runs a session on another machine, in your login session there (where its sign-in
works), and shows it in this terminal: --resume continues a session that is on that
machine, otherwise a new one starts in --dir. --profile picks the account folder (its id,
from hopsesh accounts list). It keeps running there when this terminal closes;
hopsesh terminals attach shows it again. Needs hopsesh set up there (hopsesh hosts setup).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			defer r.app.Catalog.Close()
			f := cmd.Flags()
			ag, _ := f.GetString("agent")
			req := peer.TerminalRequest{Kind: peer.TerminalNew, Agent: agent.ID(ag)}
			req.Profile, _ = f.GetString("profile")
			req.Dir, _ = f.GetString("dir")
			if s, _ := f.GetString("resume"); s != "" {
				req.Kind, req.Session = peer.TerminalResume, s
			}
			if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
				req.Cols, req.Rows = w, h
			}
			ctx, cancel := ctxTimeout(2)
			t, err := r.app.RemoteTerminal(ctx, args[0], req)
			cancel()
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "hopsesh: %s on %s (terminal %s)\r\n", t.Title, args[0], t.ID)
			return attachHere(r, args[0], t.ID)
		},
	}
	f := cmd.Flags()
	f.String("agent", "claude", "the agent: claude or codex")
	f.String("resume", "", "the id of a session on that machine to continue")
	f.String("dir", "", "the folder there a new session starts in (its home folder by default)")
	f.String("profile", "", "the account folder's id (the agent's default folder when empty)")
	return cmd
}

// attachHere shows a terminal of hopsesh on machine in this terminal until it ends.
func attachHere(r *run, machine, id string) error {
	sizes := make(chan [2]int, 4)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		old, err := term.MakeRaw(fd)
		if err == nil {
			defer func() { _ = term.Restore(fd, old) }()
		}
		go watchSize(int(os.Stdout.Fd()), sizes)
	}
	code, err := r.app.AttachTerminal(context.Background(), machine, id, os.Stdin, os.Stdout, sizes, func(msg string) {
		fmt.Fprintf(os.Stderr, "\r\nhopsesh: %s\r\n", msg)
	})
	if err != nil {
		return err
	}
	if code != 0 {
		return exitCode(code)
	}
	return nil
}
