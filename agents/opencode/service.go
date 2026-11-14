package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// OpenCode runs one shared background service (opencode serve --service) that the
// terminal, the TUI and the desktop app all use. Every subcommand except run starts it
// when it is stopped, and the TUI replaces one of another version. hopsesh never starts,
// stops or restarts it: it asks `opencode service status` first and passes --standalone
// (a private server that ends with the command) when the service is not running.

type serviceState string

const (
	serviceRunning serviceState = "running"
	serviceStopped serviceState = "stopped"
	// serviceUnknown: the probe failed. Reads where SQL is unavailable run their own
	// server, as with the service stopped; a change (writeService), an export a move
	// carries and a resume, which could run beside or replace a running service, do not
	// run.
	serviceUnknown serviceState = "unknown"
)

// service is what the probe found.
type service struct {
	State   serviceState
	URL     string
	Version string // the running service's version, when asked (serviceVersion)
	err     error  // why the probe failed (serviceUnknown)
}

// standalone reports whether commands must run a private server: whenever the service
// is not known to run, so no command starts it.
func (s service) standalone() bool { return s.State != serviceRunning }

// commandTimeout bounds one OpenCode command (a standalone server starts first).
const commandTimeout = 90 * time.Second

// runEnv is the environment of every OpenCode command hopsesh runs: no self-update.
var runEnv = []string{"OPENCODE_DISABLE_AUTOUPDATE=1"}

// run runs OpenCode's program with argv after it (the confined Host runs the binary
// Detect found). A program that could not run at all (none there, or a machine where
// nothing runs: a peer's package) is an execError.
func run(ctx context.Context, h agent.Host, argv []string, o agent.RunOptions) (agent.Result, error) {
	o.Env = append(append(append([]string{}, runEnv...), pinnedEnv(h)...), o.Env...)
	if o.Timeout == 0 {
		o.Timeout = commandTimeout
	}
	res, err := h.Exec().Run(ctx, append([]string{binName}, argv...), o)
	if err != nil {
		return res, &execError{err}
	}
	if res.Code == 126 || res.Code == 127 {
		// The shell could not run it (over SSH a missing program is a result, not an error).
		return res, &execError{fmt.Errorf("exit status %d: %s", res.Code, strings.TrimSpace(firstLine(res.Stderr)))}
	}
	return res, nil
}

// pinnedEnv names the data and state folders the module found OpenCode's in (from the
// machine's variables and home, as Detect resolved its roots), so the program reads and
// writes the database hopsesh read, and asks for the service registered there, even when
// hopsesh runs with other variables than the person's shell (started from Finder, or a
// program over SSH). An account profile's own variables come after these.
func pinnedEnv(h agent.Host) []string {
	f := h.Facts()
	if f.Home == "" {
		return nil
	}
	var env []string
	for _, v := range []struct {
		name string
		def  []string
	}{{"XDG_DATA_HOME", []string{".local", "share"}}, {"XDG_STATE_HOME", []string{".local", "state"}}} {
		val := f.Env[v.name]
		if val == "" {
			val = h.Path().Join(append([]string{f.Home}, v.def...)...)
		}
		env = append(env, v.name+"="+val)
	}
	return env
}

// execError is OpenCode's program not running at all.
type execError struct{ err error }

func (e *execError) Error() string { return "opencode did not run: " + e.err.Error() }
func (e *execError) Unwrap() error { return e.err }

// notRun reports whether err is OpenCode's program not running at all.
func notRun(err error) bool {
	var e *execError
	return errors.As(err, &e)
}

// probeService asks OpenCode whether its background service runs. `service status`
// prints its URL or "stopped" and starts nothing.
func probeService(ctx context.Context, h agent.Host) service {
	res, err := run(ctx, h, []string{"service", "status"}, agent.RunOptions{Timeout: 20 * time.Second})
	if err != nil {
		return service{State: serviceUnknown, err: err}
	}
	if res.Code != 0 {
		return service{State: serviceUnknown, err: commandError("service status", res)}
	}
	line := strings.TrimSpace(firstLine(res.Stdout))
	switch {
	case line == "stopped":
		return service{State: serviceStopped}
	case strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://"):
		return service{State: serviceRunning, URL: line}
	}
	return service{State: serviceUnknown, err: fmt.Errorf("opencode service status printed %q", line)}
}

// unknownService is why nothing changes through OpenCode when the probe failed: a command
// without --standalone could start the service, and a private server could run beside
// it. OpenCode's program not running at all stays that (notRun).
func unknownService(svc service) error {
	if notRun(svc.err) {
		return svc.err
	}
	return fmt.Errorf("hopsesh could not tell whether OpenCode's background service is running (%v), so it does not change OpenCode's sessions now; check `opencode service status`, then try again", svc.err)
}

func firstLine(b []byte) string {
	s, _, _ := strings.Cut(string(b), "\n")
	return s
}

// api calls OpenCode's HTTP API through its program (which handles the service's
// password): `opencode api [--standalone] METHOD path [-d body]`.
func api(ctx context.Context, h agent.Host, svc service, method, path string, body []byte) (agent.Result, error) {
	argv := []string{"api"}
	if svc.standalone() {
		argv = append(argv, "--standalone")
	}
	argv = append(argv, method, path)
	if body != nil {
		argv = append(argv, "-d", string(body))
	}
	return run(ctx, h, argv, agent.RunOptions{})
}

// apiJSON calls the API and decodes its JSON answer into v.
func apiJSON(ctx context.Context, h agent.Host, svc service, method, path string, body []byte, v any) error {
	res, err := api(ctx, h, svc, method, path, body)
	if err != nil {
		return err
	}
	if res.Code != 0 {
		return apiError(method, path, res)
	}
	if err := json.Unmarshal(bytes.TrimSpace(res.Stdout), v); err != nil {
		return fmt.Errorf("OpenCode's answer to %s %s: %w", method, path, err)
	}
	return nil
}

// apiError describes a failed API call: `opencode api` prints the answer's body on
// stdout and "HTTP <status>" on stderr. A 404 for a session is agent.ErrNotFound.
func apiError(method, path string, res agent.Result) error {
	err := commandError("api "+method+" "+path, res)
	if strings.HasPrefix(strings.TrimSpace(string(res.Stderr)), "HTTP 404") && strings.HasPrefix(path, "/api/session/") {
		return fmt.Errorf("%w (%v)", agent.ErrNotFound, err)
	}
	return err
}

// commandError describes a failed OpenCode command by its exit code and the end of
// what it printed (stderr, and the answer's body an API call prints on stdout).
func commandError(what string, res agent.Result) error {
	msg := strings.TrimSpace(string(res.Stderr))
	if body := strings.TrimSpace(string(res.Stdout)); body != "" {
		msg = strings.TrimSpace(msg + " " + body)
	}
	if len(msg) > 400 {
		msg = "…" + msg[len(msg)-400:]
	}
	return fmt.Errorf("opencode %s exited with %d: %s", what, res.Code, msg)
}

// serviceVersion asks the running service its version (GET /api/info).
func serviceVersion(ctx context.Context, h agent.Host, svc service) (string, error) {
	if svc.State != serviceRunning {
		return "", errors.New("OpenCode's background service is not running")
	}
	var info struct {
		Version string `json:"version"`
	}
	if err := apiJSON(ctx, h, svc, "GET", "/api/info", nil, &info); err != nil {
		return "", err
	}
	if info.Version == "" {
		return "", errors.New("OpenCode's background service did not say its version")
	}
	return info.Version, nil
}

// supported refuses an OpenCode older than the module reads (its database tables, its
// commands and API came in 2.1.0).
func supported(what, version string) error {
	if !agent.VersionAtLeast(version, minVersion) {
		return fmt.Errorf("%w: %s", agent.ErrUnsupported, agent.NeedsVersion(what, version, minVersion))
	}
	return nil
}

// checkStandalone refuses a change made through a private server whose OpenCode is
// older than one that wrote the database: an older OpenCode may not read its rows.
func checkStandalone(svc service, binary, newest string) error {
	if !svc.standalone() {
		return nil
	}
	if binary == "" {
		return errors.New("hopsesh cannot tell the version of the opencode program, so it does not change the database through it")
	}
	if err := supported("the opencode program", binary); err != nil {
		return err
	}
	if newest == "" {
		return nil
	}
	if agent.VersionLess(binary, newest) {
		return fmt.Errorf("OpenCode's background service is not running, and the opencode program (%s) is older than the OpenCode that last wrote its sessions (%s); update opencode or start OpenCode, then try again", binary, newest)
	}
	return nil
}

// resumeProgram picks the program a resume command runs: OpenCode's TUI replaces a
// running service of another version (restarting it, which ends any turn it runs for
// the desktop app or another window), so it must be of the running service's version.
// The PATH opencode when it is; else the desktop app's bundled CLI, or a CLI the desktop
// app staged, of that version (found by their version files and folder names: nothing is
// run). With the service stopped, any opencode will do; when the probe failed, none.
func resumeProgram(ctx context.Context, h agent.Host, in agent.Install, svc service) (string, error) {
	switch svc.State {
	case serviceStopped:
		return binName, nil
	case serviceUnknown:
		return "", errors.New("hopsesh could not tell whether OpenCode's background service is running (`opencode service status` failed); " +
			"opening the session with another version than the service's would restart it, which the desktop app shares. Check `opencode service status`, then try again")
	}
	want, err := serviceVersion(ctx, h, svc)
	if err != nil {
		return "", fmt.Errorf("hopsesh could not tell which version OpenCode's background service runs (%v); resuming with another version would restart it", err)
	}
	if err := supported("OpenCode's background service", want); err != nil {
		return "", err
	}
	if in.Version != "" && sameVersion(in.Version, want) {
		return binName, nil
	}
	for _, c := range desktopCLIs(h) {
		if sameVersion(c.version, want) {
			return c.path, nil
		}
	}
	return "", &restartError{Service: want, Have: in.Version}
}

// restartError is a resume that would restart OpenCode's background service: no
// opencode of the service's version is on this machine (decision D2: hopsesh asks
// instead of restarting it unasked).
type restartError struct {
	Service string // the running service's version
	Have    string // the PATH opencode's
}

func (e *restartError) Error() string {
	have := "the opencode program"
	if e.Have != "" {
		have += " (" + e.Have + ")"
	}
	return fmt.Sprintf("OpenCode's background service runs %s, and %s is another version: opening the session with it would restart the service, "+
		"which the desktop app shares, and stop any turn it is running. Install opencode %s, or quit OpenCode desktop and run `opencode service stop` yourself, then try again", e.Service, have, e.Service)
}

func sameVersion(a, b string) bool {
	if c, ok := agent.CompareVersions(a, b); ok {
		return c == 0
	}
	return strings.TrimPrefix(a, "v") == strings.TrimPrefix(b, "v")
}

// desktopCLI is an opencode program OpenCode desktop ships or staged.
type desktopCLI struct {
	path    string
	version string
}

// desktopCLIs are the desktop app's bundled CLI (its version in opencode-cli.version) and
// the CLIs it staged ({desktop}/cli/<version>-<arch>/opencode-cli), on macOS.
func desktopCLIs(h agent.Host) []desktopCLI {
	if h.Facts().OS != "darwin" {
		return nil
	}
	var out []desktopCLI
	for _, app := range desktopApps {
		if !strings.HasPrefix(app, "~/") && !agent.SystemPrograms() {
			continue // a test: never this machine's real desktop app
		}
		app = agent.Expand(app, h.Facts().Home, nil, h.Path())
		res := h.Path().Join(app, "Contents", "Resources")
		b, err := h.FS().ReadFile(h.Path().Join(res, "opencode-cli.version"), 1<<10)
		if err != nil {
			continue
		}
		bin := h.Path().Join(res, "opencode-cli")
		if _, err := h.FS().Stat(bin); err == nil {
			out = append(out, desktopCLI{path: bin, version: strings.TrimSpace(string(b))})
		}
	}
	dir := desktopCLIDir(h)
	entries, err := h.FS().ReadDir(dir)
	if err != nil {
		return out
	}
	arch := h.Facts().Arch
	if arch == "amd64" {
		arch = "x64"
	}
	for _, e := range entries {
		i := strings.LastIndex(e.Name(), "-")
		if !e.IsDir() || i <= 0 {
			continue
		}
		ver, a := e.Name()[:i], e.Name()[i+1:]
		if arch != "" && a != arch {
			continue
		}
		bin := h.Path().Join(dir, e.Name(), "opencode-cli")
		if _, err := h.FS().Stat(bin); err == nil {
			out = append(out, desktopCLI{path: bin, version: ver})
		}
	}
	return out
}
