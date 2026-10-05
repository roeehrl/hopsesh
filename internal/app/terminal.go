package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/integrate"
	"github.com/roeehrl/hopsesh/internal/core/termapp"
	"github.com/roeehrl/hopsesh/internal/core/winshim"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Terminal apps: launches in the user's own terminal (iTerm2, Terminal, Windows
// Terminal), by ticket. The app or the command line writes a ticket (the agent's command,
// its folder, the session and the tab's labels) and asks the terminal app to run only
// `<hopsesh> terminal-open <id>`. That verb, in the new tab, prints the labels, records
// the tab's terminal device and the agent's process while it runs, and starts the
// agent's command; when the agent ends it says so and keeps the tab open. "Show" finds a
// running session's tab from the agent's own process id (or that record) and the
// process table, and brings it forward. Nothing types into a tab or reads what one shows.

// ticketAge is how long a written ticket may wait for its terminal.
const ticketAge = time.Hour

// Launch is a command to open in a terminal app.
type Launch struct {
	Kind   termapp.Kind
	Run    agent.Command
	Key    agent.SessionKey // the session a KindSession launch resumes
	Labels termapp.Labels
}

// terminals is the set of terminal apps (the App's, or this system's).
func (a *App) terminals() termapp.Set {
	if len(a.Terminals.All()) == 0 {
		return termapp.System().WithExits(a.launchExit)
	}
	return a.Terminals
}

// launchExit is a launch's exit code as hopsesh's verb recorded it: a ticket's
// (terminal-open) or a step's outcome (terminal-step).
func (a *App) launchExit(h termapp.Handle) (int, bool) {
	switch h.Verb {
	case "terminal-open":
		return a.termStore().ExitOf(h.Ticket)
	case "terminal-step":
		if o, err := a.StepOutcomeOf(h.Ticket); err == nil && o != nil {
			return o.Code, true
		}
	}
	return 0, false
}

// WatchLaunch reports a launch's exit code once it ends (see termapp.Watcher): from
// hopsesh's own records, and -1 when its tab was closed first. It errors when the launch's
// terminal cannot watch (only iTerm2 with its Python API turned on can); callers then do
// without.
func (a *App) WatchLaunch(ctx context.Context, h termapp.Handle) (<-chan int, error) {
	t, err := a.TerminalByID(h.Terminal)
	if err != nil {
		return nil, err
	}
	w, ok := t.(termapp.Watcher)
	if !ok || !termapp.CapsOf(t).Watch {
		return nil, fmt.Errorf("%s cannot report when a tab ends", t.Name())
	}
	return w.Exited(ctx, h)
}

func (a *App) procs() termapp.Procs {
	if a.Procs == nil {
		return termapp.SystemProcs()
	}
	return a.Procs
}

func (a *App) termStore() termapp.Store {
	return termapp.Store{Dir: filepath.Join(a.StateDir, "terminal")}
}

// TerminalApps lists the terminal apps hopsesh can open launches in here, and whether
// each is installed.
func (a *App) TerminalApps(ctx context.Context) []termapp.Info { return a.terminals().Detect(ctx) }

// MyTerminal is the terminal app launches open in: the configured one when installed,
// else the best installed one.
func (a *App) MyTerminal(ctx context.Context) termapp.Terminal {
	return a.terminals().Choose(ctx, a.Cfg.Terminal.App)
}

// TerminalByID is one of the terminal apps by its id.
func (a *App) TerminalByID(id string) (termapp.Terminal, error) {
	t, ok := a.terminals().ByID(id)
	if !ok {
		var ids []string
		for _, t := range a.terminals().All() {
			ids = append(ids, t.ID())
		}
		return nil, fmt.Errorf("no terminal app %q here (%s)", id, strings.Join(ids, ", "))
	}
	return t, nil
}

// NewTicket writes the ticket for a launch. The agent's program is resolved here, where
// the login shell's PATH is known, so the tab finds it whatever its own PATH is.
func (a *App) NewTicket(l Launch) (string, error) {
	if len(l.Run.Argv) == 0 {
		return "", errors.New("no command to open")
	}
	argv := append([]string{}, l.Run.Argv...)
	argv[0] = resolveProgram(argv[0])
	t := termapp.Ticket{Kind: l.Kind, Argv: argv, Dir: l.Run.Dir, Env: l.Run.Env, Unset: l.Run.Unset,
		LoginEnv: a.loginVars(), Labels: l.Labels}
	if l.Kind == termapp.KindSession && l.Key.Session != "" {
		t.Key = l.Key.String()
	}
	return a.termStore().Save(t)
}

// DropTicket removes a ticket whose terminal did not open.
func (a *App) DropTicket(id string) { a.termStore().Drop(id) }

// OpenInTerminal writes a launch's ticket and opens `<prog> terminal-open <ticket>` in t
// (nil: MyTerminal), falling back to another installed terminal when t is missing or
// macOS denies hopsesh control of it. When nothing opens, the error wraps
// termapp.ErrNoTerminal (copy the command instead).
func (a *App) OpenInTerminal(ctx context.Context, prog string, t termapp.Terminal, l Launch) (termapp.Opened, error) {
	id, err := a.NewTicket(l)
	if err != nil {
		return termapp.Opened{}, err
	}
	if t == nil {
		t = a.MyTerminal(ctx)
	}
	o, err := a.terminals().Open(ctx, t, termapp.Launch{Program: prog, Args: []string{"terminal-open", id}, Dir: l.Run.Dir, Kind: l.Kind, Where: termapp.NewTab})
	if err != nil {
		a.DropTicket(id)
		return o, err
	}
	a.Audit.Write(audit.Entry{Action: "terminal.open", Detail: map[string]any{"kind": string(l.Kind), "terminal": o.Terminal, "fellBack": o.FellBack}})
	return o, nil
}

// OpenStepInTerminal opens `<prog> terminal-step <id>` (a hand-off's step the app wrote)
// in t (nil: MyTerminal), with the same fallback: beside the session the user is in where
// the terminal can (iTerm2 with its Python API on), else as a tab.
func (a *App) OpenStepInTerminal(ctx context.Context, prog string, t termapp.Terminal, stepID, dir string) (termapp.Opened, error) {
	if t == nil {
		t = a.MyTerminal(ctx)
	}
	return a.terminals().Open(ctx, t, termapp.Launch{Program: prog, Args: []string{"terminal-step", stepID}, Dir: dir, Kind: termapp.KindStep, Where: termapp.Beside})
}

// resolveProgram finds a bare program name on this process's PATH, else on the login
// shell's.
func resolveProgram(name string) string {
	if filepath.IsAbs(name) || strings.ContainsAny(name, `/\`) {
		return name
	}
	if p, err := exec.LookPath(name); err == nil && filepath.IsAbs(p) {
		return p
	}
	if p := integrate.LookLoginPath(name); p != "" {
		return p
	}
	return name
}

// loginVars are the agents' folder variables as this process has them (the app adopted
// them from the login shell).
func (a *App) loginVars() map[string]string {
	if a.Reg == nil {
		return nil
	}
	out := map[string]string{}
	for _, k := range a.Reg.LoginEnv() {
		if v := os.Getenv(k); v != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// TerminalIO is the terminal hopsesh's verb runs in.
type TerminalIO struct {
	In       io.Reader
	Out, Err io.Writer
	// Labels: Out is a terminal (not a pipe, not JSON), so labels may be printed.
	Labels bool
	// Hold keeps the tab open once the agent ends, as a shell (a terminal whose tab
	// closes with its command).
	Hold   bool
	Getenv func(string) string
}

func (t TerminalIO) getenv(k string) string {
	if t.Getenv != nil {
		return t.Getenv(k)
	}
	return os.Getenv(k)
}

// RunTicket runs a ticket in this terminal (the hidden `hopsesh terminal-open <id>`):
// labels, the agent's command, a record while it runs, and its exit code after.
func (a *App) RunTicket(id string, tio TerminalIO) error {
	t, err := a.termStore().Take(id, ticketAge)
	if err == nil {
		err = a.checkTicket(t)
	}
	if err != nil {
		if t.Kind == termapp.KindSession || t.Kind == termapp.KindStep {
			_ = a.termStore().Exit(id, -1)
		}
		if tio.Hold {
			fmt.Fprintf(tio.Err, "hopsesh could not open this: %v\n", err)
			return holdTab(tio, "")
		}
		return fmt.Errorf("hopsesh could not open this: %w", err)
	}
	code, err := a.runLaunch(id, t, tio)
	if t.Kind == termapp.KindSession || t.Kind == termapp.KindStep {
		// For a watcher: the agent's exit, which a held tab outlives (sign-ins and shells
		// leave no record).
		ec := code
		if err != nil {
			ec = -1
		}
		_ = a.termStore().Exit(id, ec)
	}
	name := strings.TrimSuffix(filepath.Base(t.Argv[0]), ".exe")
	switch {
	case err != nil:
		fmt.Fprintf(tio.Out, "\nhopsesh could not start %s: %v\n", name, err)
	case code >= 0:
		fmt.Fprintf(tio.Out, "\n[%s exited with code %d]\n", name, code)
	default:
		fmt.Fprintf(tio.Out, "\n[%s was stopped]\n", name)
	}
	if tio.Hold {
		return holdTab(tio, t.Dir)
	}
	return nil
}

// holdTab keeps a tab whose command has ended open, as the user's shell.
func holdTab(tio TerminalIO, dir string) error {
	fmt.Fprintln(tio.Out, "This tab stays open as a shell; close it when you are done.")
	return termapp.ExecShell(dir)
}

// RunInThisTerminal runs a launch in this terminal (the command line's and the terminal
// UI's --run): labels when Out is a terminal, the record while a session runs, and the
// agent's exit code.
func (a *App) RunInThisTerminal(l Launch, tio TerminalIO) (int, error) {
	if len(l.Run.Argv) == 0 {
		return -1, errors.New("no command to run")
	}
	t := termapp.Ticket{Kind: l.Kind, Argv: l.Run.Argv, Dir: l.Run.Dir, Env: l.Run.Env, Unset: l.Run.Unset, Labels: l.Labels}
	if l.Kind == termapp.KindSession && l.Key.Session != "" {
		t.Key = l.Key.String()
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return -1, err
	}
	return a.runLaunch(hex.EncodeToString(b), t, tio)
}

// checkTicket refuses a ticket that would run anything but an enabled agent's own
// program, or in a folder that is not there (a ticket is hopsesh's own, in its private
// state folder; this is a second check).
func (a *App) checkTicket(t termapp.Ticket) error {
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(t.Argv[0])), ".exe")
	ok := false
	for _, s := range a.Specs() {
		for _, b := range s.Binaries {
			ok = ok || strings.EqualFold(b.Name, base)
		}
	}
	if !ok {
		return fmt.Errorf("it runs %s, which is not an agent's program", t.Argv[0])
	}
	if fi, err := os.Stat(t.Dir); err != nil || !fi.IsDir() {
		return fmt.Errorf("its folder %s is not there", t.Dir)
	}
	if t.Key != "" {
		if _, err := agent.ParseKey(t.Key); err != nil {
			return err
		}
	}
	return nil
}

// runLaunch prints a launch's labels, runs its command attached to this terminal (no
// relay and no capture: the user's terminal is the agent's), records a session's tab and
// process while it runs, and returns its exit code (-1: stopped by a signal).
func (a *App) runLaunch(id string, t termapp.Ticket, tio TerminalIO) (int, error) {
	labels := t.Labels.For(t.Kind)
	if tio.Labels {
		_, _ = tio.Out.Write(termapp.Sequences(labels, tio.getenv))
		defer func() { _, _ = tio.Out.Write(termapp.ClearSequences(tio.getenv)) }()
	}
	if t.Kind == termapp.KindSession && labels.Title != "" {
		fmt.Fprintf(tio.Out, "hopsesh: resuming “%s”", labels.Title)
		if labels.Agent != "" {
			fmt.Fprintf(tio.Out, " in %s", labels.Agent)
		}
		fmt.Fprintln(tio.Out, ".")
	}
	argv := append([]string{}, t.Argv...)
	argv[0] = resolveProgram(argv[0])
	c := exec.Command(argv[0], argv[1:]...) //nolint:gosec // an agent's own program, checked against the modules' binaries
	c.Dir = t.Dir
	env := host.Without(os.Environ(), t.Unset)
	for k, v := range t.LoginEnv {
		if os.Getenv(k) == "" {
			env = append(env, k+"="+v)
		}
	}
	c.Env = append(env, t.Env...)
	c.Stdin, c.Stdout, c.Stderr = tio.In, tio.Out, tio.Err
	// Ctrl+C reaches the agent (the terminal's foreground group); hopsesh stays to say how
	// it ended. A caught signal, unlike an ignored one, is not passed on to the agent.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	if err := c.Start(); err != nil {
		return -1, err
	}
	if t.Kind == termapp.KindSession && t.Key != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		tty := a.procs().TTY(ctx, os.Getpid())
		cancel()
		r := termapp.Record{Ticket: id, Key: t.Key, TTY: tty, PID: c.Process.Pid, Program: termapp.Sanitize(tio.getenv("TERM_PROGRAM"), 40)}
		if err := a.termStore().Record(r); err == nil {
			defer a.termStore().Forget(id)
		}
	}
	err := c.Wait()
	code := 0
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		code = ee.ExitCode()
	case err != nil:
		return -1, err
	}
	a.Audit.Write(audit.Entry{Action: "terminal.run", Detail: map[string]any{"kind": string(t.Kind), "code": code}})
	return code, nil
}

// SessionTab finds the tab a session on this machine runs in: from the agent's own
// process id (livePID, from its registry; 0 when it has none) and hopsesh's records of
// the launches it started. False with a nil error: no tab hopsesh can show.
func (a *App) SessionTab(ctx context.Context, key agent.SessionKey, livePID int) (termapp.Found, bool, error) {
	var cands []termapp.Candidate
	if livePID > 0 {
		cands = append(cands, termapp.Candidate{PID: livePID})
	}
	for _, r := range a.termStore().Running(key.String(), a.procs().Alive) {
		if r.TTY != "" {
			cands = append(cands, termapp.Candidate{PID: r.PID, TTY: r.TTY})
		}
	}
	if len(cands) == 0 {
		return termapp.Found{}, false, nil
	}
	return a.terminals().FindTab(ctx, a.procs(), a.MyTerminal(ctx), cands)
}

// Running reports whether hopsesh started a session that still runs (a record whose
// process is alive): a session the agent's own files may not show as open (Codex).
func (a *App) Running(key agent.SessionKey) bool {
	return len(a.termStore().Running(key.String(), a.procs().Alive)) > 0
}

// ShowTab brings a session's found tab forward, after checking its process is still on
// that tab.
func (a *App) ShowTab(ctx context.Context, f termapp.Found) error {
	err := termapp.Show(ctx, a.procs(), f)
	a.Audit.Write(audit.Entry{Action: "terminal.show", Detail: map[string]any{"terminal": f.Terminal.ID(), "ok": err == nil}})
	return err
}

// ErrOpenElsewhere means a session already runs in a tab: show that one instead of a
// second copy writing to the same conversation.
var ErrOpenElsewhere = errors.New("it is already open")

// OpenElsewhere says where a session already runs, when it does: in a tab hopsesh can
// show (found), or in a terminal it cannot (live).
func (a *App) OpenElsewhere(ctx context.Context, key agent.SessionKey, live agent.LiveInfo) error {
	f, found, _ := a.SessionTab(ctx, key, live.PID)
	switch {
	case found:
		return fmt.Errorf("%w in %s: show that tab instead", ErrOpenElsewhere, f.Name())
	case live.State == agent.Live || a.Running(key):
		return fmt.Errorf("%w in another terminal; quit it there first", ErrOpenElsewhere)
	}
	return nil
}

// TabArgv is a command's argument list as the app's terminal tabs run it: the program
// found on this process's PATH, else on the login shell's, and on Windows a command shim
// (npm's codex.cmd) replaced by the program it starts, since a tab never runs a batch file
// (see internal/core/winshim).
func TabArgv(argv []string) ([]string, error) {
	if len(argv) == 0 {
		return nil, errors.New("no command to run")
	}
	out := append([]string{}, argv...)
	out[0] = resolveProgram(out[0])
	if runtime.GOOS == "windows" {
		return winshim.Program(out, winshim.System())
	}
	return out, nil
}
