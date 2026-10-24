package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/installs"
	"github.com/roeehrl/hopsesh/internal/core/loginsvc"
	"github.com/roeehrl/hopsesh/internal/core/peer"
	"github.com/roeehrl/hopsesh/internal/core/pty"
	"github.com/roeehrl/hopsesh/internal/core/serve"
	"github.com/roeehrl/hopsesh/internal/core/transport"
	"github.com/roeehrl/hopsesh/internal/update"
	"github.com/roeehrl/hopsesh/internal/version"
)

// allowedHost is a configured machine the user allowed hopsesh to connect to.
func (a *App) allowedHost(name string) (config.Host, error) {
	h := a.Cfg.FindHost(name)
	if h == nil {
		return config.Host{}, fmt.Errorf("no machine %q; add it with hopsesh hosts add", name)
	}
	if !h.Allowed {
		return config.Host{}, fmt.Errorf("%s is not allowed yet; allow it in Machines (hopsesh hosts trust %s)", name, name)
	}
	return *h, nil
}

// remoteHopsesh is the shell line that runs hopsesh with args on a Unix-like machine,
// wherever it is installed (a non-interactive SSH session often lacks ~/.local/bin on its
// PATH). The copy hopsesh set up itself, in ~/.hopsesh/bin, comes first.
func remoteHopsesh(args string) string {
	return `for c in "$HOME/.hopsesh/bin/hopsesh" hopsesh "$HOME/.local/bin/hopsesh" /opt/homebrew/bin/hopsesh /usr/local/bin/hopsesh /Applications/hopsesh.app/Contents/Resources/bin/hopsesh; do
  if command -v "$c" >/dev/null 2>&1; then exec "$c" ` + args + `; fi
done
echo "hopsesh is not installed" >&2
exit 127`
}

// remoteLine is the command line running hopsesh with args on m.
func (a *App) remoteLine(ctx context.Context, m *host.Machine, args string) (string, error) {
	if m.Facts.OS != "windows" {
		return "sh -c " + transport.ShQuote(remoteHopsesh(args)), nil
	}
	out, err := m.Conn.RunPowerShell(ctx, peerFindWindows)
	if err != nil {
		var re *transport.RemoteError
		if errors.As(err, &re) && re.Code == 127 {
			return "", fmt.Errorf("hopsesh is not installed on %s", m.Name)
		}
		return "", err
	}
	f := strings.SplitN(strings.ReplaceAll(strings.TrimSpace(string(out)), "\r", ""), "\n", 2)
	shell := ""
	if len(f) == 2 {
		shell = strings.TrimSpace(f[1])
	}
	return windowsLine(strings.TrimSpace(f[0]), shell, args), nil
}

// ----- hopsesh in the login session there -----

// loginPeer says hello to hopsesh on a machine and requires it to answer from the login
// session there (hopsesh serve).
func (a *App) loginPeer(ctx context.Context, machine string) (*peer.Client, peer.HelloReply, func(), error) {
	h, err := a.allowedHost(machine)
	if err != nil {
		return nil, peer.HelloReply{}, nil, err
	}
	c, hr, closeFn, err := a.dialPeer(ctx, h)
	if err != nil {
		return nil, hr, nil, err
	}
	if hr.Protocol < 6 {
		closeFn()
		return nil, hr, nil, fmt.Errorf("hopsesh on %s (%s) is older than this one; update it there (Machines › Update hopsesh)", machine, hr.Version)
	}
	if !hr.Login {
		closeFn()
		return nil, hr, nil, peer.NoLogin(machine)
	}
	return c, hr, closeFn, nil
}

// RemoteTerminal has hopsesh on machine run req in a terminal it owns there.
func (a *App) RemoteTerminal(ctx context.Context, machine string, req peer.TerminalRequest) (peer.Terminal, error) {
	c, _, done, err := a.loginPeer(ctx, machine)
	if err != nil {
		return peer.Terminal{}, err
	}
	defer done()
	var t peer.Terminal
	err = c.Call(ctx, peer.MethodTerminalOpen, req, &t)
	return t, err
}

// RemoteTerminals lists the terminals hopsesh runs on machine.
func (a *App) RemoteTerminals(ctx context.Context, machine string) ([]peer.Terminal, error) {
	c, _, done, err := a.loginPeer(ctx, machine)
	if err != nil {
		return nil, err
	}
	defer done()
	var ts []peer.Terminal
	err = c.Call(ctx, peer.MethodTerminalList, struct{}{}, &ts)
	return ts, err
}

// CloseRemoteTerminal ends one of them.
func (a *App) CloseRemoteTerminal(ctx context.Context, machine, id string) error {
	c, _, done, err := a.loginPeer(ctx, machine)
	if err != nil {
		return err
	}
	defer done()
	return c.Call(ctx, peer.MethodTerminalClose, peer.TerminalID{ID: id}, nil)
}

// AttachTerminal shows one of hopsesh's terminals on machine in this terminal (in, out),
// until its program ends (its exit code) or the connection does (an error; it keeps
// running there).
func (a *App) AttachTerminal(ctx context.Context, machine, id string, in io.Reader, out io.Writer, sizes <-chan [2]int, problems func(string)) (int, error) {
	h, err := a.allowedHost(machine)
	if err != nil {
		return -1, err
	}
	m, err := a.Connect(ctx, h)
	if err != nil {
		return -1, err
	}
	defer m.Close()
	line, err := a.remoteLine(ctx, m, "serve relay terminal")
	if err != nil {
		return -1, err
	}
	pipe, err := m.Conn.StartPipe(ctx, line)
	if err != nil {
		return -1, err
	}
	defer pipe.Close()
	code, err := pty.Attach(serve.NewFrameConn(ctx, pipe.Out, pipe.In, pipe.Close), id, in, out, sizes, problems)
	if err != nil {
		if s := strings.TrimSpace(pipe.Stderr()); s != "" {
			err = fmt.Errorf("%w (%s)", err, strings.SplitN(s, "\n", 2)[0])
		}
	}
	return code, err
}

// ----- setting hopsesh up there -----

// SetupStep is one step of setting hopsesh up on a machine.
type SetupStep struct {
	Name   string `json:"name"`
	State  string `json:"state"` // done, skipped, waiting, failed
	Detail string `json:"detail,omitempty"`
}

// MachineSetup is what setting hopsesh up on a machine did.
type MachineSetup struct {
	Machine string           `json:"machine"`
	Version string           `json:"version"`
	Program string           `json:"program"`
	Service loginsvc.Status  `json:"service"`
	Login   bool             `json:"login"` // hopsesh answers from the login session there now
	Steps   []SetupStep      `json:"steps"`
	Hello   *peer.HelloReply `json:"hello,omitempty"`
}

// SetUpMachine installs this hopsesh on a machine reached over SSH (only when the user
// asks: scanning never does), lets it receive sessions, and registers it to start in the
// login session there at each login. Nothing needs administrator rights there.
func (a *App) SetUpMachine(ctx context.Context, machine string, progress func(SetupStep)) (*MachineSetup, error) {
	h, err := a.allowedHost(machine)
	if err != nil {
		return nil, err
	}
	out := &MachineSetup{Machine: machine, Version: version.Version}
	step := func(s SetupStep) {
		out.Steps = append(out.Steps, s)
		if progress != nil {
			progress(s)
		}
	}
	fail := func(name string, err error) (*MachineSetup, error) {
		step(SetupStep{Name: name, State: "failed", Detail: err.Error()})
		return out, err
	}
	m, err := a.Connect(ctx, h)
	if err != nil {
		return fail("Connect", err)
	}
	defer m.Close()
	step(SetupStep{Name: "Connect", State: "done", Detail: m.Facts.OS + "/" + m.Facts.Arch})
	// Every hopsesh there is brought to this version (the app with its own updater) and the
	// machine's own one runs it; a copy a package manager owns is left to it.
	list, err := a.remoteInstalls(ctx, m)
	if err != nil {
		return fail("Install hopsesh", err)
	}
	steps := installs.Plan(list, version.Version, remoteProgram(m))
	// A copy that couldn't be updated is reported at the end: hopsesh still runs there.
	program, list, applyErr := applyInstalls(ctx, remoteInstaller{a, m}, list, steps, step)
	if program == "" {
		return out, nonNil(applyErr, errors.New("hopsesh could not be installed on "+machine))
	}
	for _, in := range list.Installs {
		if in.Path == program && in.Version != "" {
			out.Version = in.Version
		}
	}
	if !stepped(out.Steps) {
		step(SetupStep{Name: "Install hopsesh", State: "skipped", Detail: "hopsesh " + out.Version + " is there already, at " + installs.Tilde(program, list.Home)})
	}
	out.Program = program
	// The identity kept here while it had no hopsesh becomes its own, so the lineage
	// recorded since stays that machine's.
	if err := a.handOverIdentity(ctx, m); err != nil {
		step(SetupStep{Name: "Machine identity", State: "failed", Detail: err.Error()})
	}
	run := func(args ...string) ([]byte, error) {
		if m.Facts.OS == "windows" {
			line := `"` + program + `"`
			for _, x := range args {
				line += " " + x
			}
			return m.Conn.Run(ctx, line)
		}
		return m.Conn.RunSh(ctx, `exec "$@"`, append([]string{program}, args...)...)
	}
	if b, err := run("receive", "on"); err != nil {
		step(SetupStep{Name: "Receive sessions", State: "failed", Detail: outputOr(string(b), err)})
	} else {
		step(SetupStep{Name: "Receive sessions", State: "done", Detail: "on"})
	}
	b, err := run("serve", "install", "--json")
	if err != nil {
		return fail("Start at login", errors.New(outputOr(string(b), err)))
	}
	if err := json.Unmarshal(b, &out.Service); err != nil {
		return fail("Start at login", fmt.Errorf("unexpected answer: %s", outputOr(string(b), err)))
	}
	switch {
	case out.Service.Running:
		step(SetupStep{Name: "Start at login", State: "done", Detail: "running in the login session"})
	case out.Service.NoSession:
		step(SetupStep{Name: "Start at login", State: "waiting", Detail: "starts when someone logs in to " + machine})
	default:
		step(SetupStep{Name: "Start at login", State: "waiting", Detail: nonEmptyStr(out.Service.Detail, "registered; not running yet")})
	}
	// What answers now: give hopsesh serve a moment to listen.
	for i := 0; i < 10; i++ {
		_, hr, done, err := a.dialPeer(ctx, h)
		if err == nil {
			done()
			out.Hello = &hr
			out.Login = hr.Login
			if hr.Login || !out.Service.Running {
				break
			}
		}
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	if a.Cfg.Peer.SetUpIntegrations {
		switch _, err := a.SetUpProtectionOn(ctx, machine, false); {
		case !out.Login:
			step(SetupStep{Name: "Hooks and skill", State: "waiting", Detail: "added once hopsesh runs in the login session there (Settings › Machines)"})
		case err != nil:
			step(SetupStep{Name: "Hooks and skill", State: "failed", Detail: err.Error()})
		default:
			step(SetupStep{Name: "Hooks and skill", State: "done", Detail: "movement hooks and the skill for every agent there"})
		}
	}
	return out, applyErr
}

// remoteProgram is where hopsesh sets itself up on m: a folder of its own, so a copy the
// user installed (the app, a package manager, a link in ~/.local/bin) is never replaced.
func remoteProgram(m *host.Machine) string {
	if m.Facts.OS == "windows" {
		return m.Path().Join(m.Facts.Home, ".hopsesh", "bin", "hopsesh.exe")
	}
	return m.Path().Join(m.Facts.Home, ".hopsesh", "bin", "hopsesh")
}

// hopseshFor is this hopsesh's program for another machine: this one when it runs there
// too (a macOS program here is universal), else the same release's, downloaded and verified.
func (a *App) hopseshFor(ctx context.Context, goos, goarch string) ([]byte, error) {
	if goos == runtime.GOOS && (goarch == runtime.GOARCH || goos == "darwin") {
		if bin, err := movementHookExecutable(); err == nil {
			if goos != "darwin" || goarch == runtime.GOARCH || universal(bin) {
				return os.ReadFile(bin)
			}
		}
	}
	rel, err := update.Tagged(ctx, version.Version)
	if err != nil {
		return nil, fmt.Errorf("this hopsesh (%s) cannot set up a %s/%s machine: %w", version.Version, goos, goarch, err)
	}
	return update.CLIFor(ctx, rel, goos, goarch)
}

// universal: a macOS program with both architectures.
func universal(bin string) bool {
	f, err := os.Open(bin)
	if err != nil {
		return false
	}
	defer f.Close()
	var magic [4]byte
	_, err = io.ReadFull(f, magic[:])
	return err == nil && (magic == [4]byte{0xca, 0xfe, 0xba, 0xbe} || magic == [4]byte{0xca, 0xfe, 0xba, 0xbf})
}

// handOverIdentity writes the identity kept here for m to m, now that hopsesh is there.
func (a *App) handOverIdentity(ctx context.Context, m *host.Machine) error {
	kept, err := a.endpointKeeper(m.Name).Kept()
	if err != nil || kept == "" {
		return err
	}
	fsys, err := m.FS(ctx)
	if err != nil {
		return err
	}
	path := m.Path().Join(m.Facts.Home, ".hopsesh", "endpoint-id")
	if dir := m.Facts.Env["HOPSESH_CONFIG_DIR"]; dir != "" {
		path = m.Path().Join(dir, "endpoint-id")
	}
	if b, err := fsys.ReadFile(path, 128); err == nil {
		if strings.TrimSpace(string(b)) != kept {
			return fmt.Errorf("%s has another identity already; sessions moved before keep the one this machine kept", m.Name)
		}
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	_ = fsys.MkdirAll(m.Path().Dir(path))
	ex, ok := fsys.(interface {
		CreateExclusive(string, []byte, fs.FileMode) error
	})
	if !ok {
		return errors.New("cannot create the identity file atomically")
	}
	return ex.CreateExclusive(path, []byte(kept+"\n"), 0o600)
}

func outputOr(out string, err error) string {
	if s := strings.TrimSpace(out); s != "" {
		return strings.SplitN(s, "\n", 2)[0]
	}
	return err.Error()
}

func nonEmptyStr(s, otherwise string) string {
	if s != "" {
		return s
	}
	return otherwise
}

// stepped reports whether any step touched a hopsesh program.
func stepped(steps []SetupStep) bool {
	for _, s := range steps {
		switch s.Name {
		case "Install hopsesh", "Update hopsesh", "Update the app", "One hopsesh":
			return true
		}
	}
	return false
}

func nonNil(err, otherwise error) error {
	if err != nil {
		return err
	}
	return otherwise
}
