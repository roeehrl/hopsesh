// Package fakeopencode is the stand-in OpenCode 2.3.0 the tests (and the test bundle) run
// in place of the real one. It keeps sessions in a SQLite database with OpenCode's own
// schema, where OpenCode keeps it, and answers what hopsesh asks OpenCode: --version,
// debug paths, service status, service get|set|unset port, session
// list|export|import|delete, auth list and the HTTP
// API calls hopsesh makes through `opencode api`, the way OpenCode 2.3.0 does
// (agents/opencode/testdata/2.3.0 holds what the real one printed).
//
// It has no model and no server. Its background service is make-believe: stopped unless
// $FAKE_OPENCODE_SERVICE is "running" (or a URL), or a command started it, which
// OpenCode does for any command but `run`, `service`, `debug paths` and --version when no
// --standalone is given; the stand-in then records the start in its state folder and in
// $FAKE_AGENT_LOG ("opencode: started the background service"), so a test can check that
// hopsesh never starts it. A port `service set port` set (in {config}/service.json, as
// OpenCode keeps it) is the started service's; setting or unsetting it stops the service
// registered in the state folder, which the stand-in logs ("opencode: stopped the
// background service").
//
// An import decodes the transfer with OpenCode's schemas (decode.go): a wrong shape is a
// Bad Request, and members the schemas do not declare are dropped.
//
// Every call is appended to $FAKE_AGENT_LOG as "opencode <args>". Other variables:
// $FAKE_OPENCODE_VERSION (the program's version, default 2.3.0),
// $FAKE_OPENCODE_SERVICE_VERSION (the running service's, default the program's),
// $FAKE_OPENCODE_ACTIVE (session ids with a turn running in the background service,
// comma-separated; their rows claim the turn, and a private server lists none), and
// $FAKE_OPENCODE_AUTH (what `auth list --format json` prints, default []),
// $FAKE_OPENCODE_TOOL (the shell command a `run -s <id>` turn's model calls: the turn plays
// hopsesh's plugin, plugin.go), and $FAKE_OPENCODE_FAIL ("session import", for example: that command fails, changing
// nothing, as a program that died would).
package fakeopencode

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Version is the OpenCode version the stand-in plays.
const Version = "2.3.0"

// VersionLine is what `opencode --version` prints for a version.
func VersionLine(v string) string { return "opencode v" + v }

// servicePort is the background service's port (OpenCode's default outside Windows).
const servicePort = 49374

// Proc is one run of the stand-in.
type Proc struct {
	Args   []string
	Dir    string                      // the working folder
	Getenv func(string) (string, bool) // its environment (nil: the process's)
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Now    func() time.Time // nil: the clock
	// ReadFile and Stat reach the files a command names (an import's file, a folder):
	// nil, this machine's. A test of a fake machine passes its own.
	ReadFile func(string) ([]byte, error)
	Stat     func(string) (os.FileInfo, error)
}

func (p Proc) readFile(f string) ([]byte, error) {
	if p.ReadFile != nil {
		return p.ReadFile(f)
	}
	return os.ReadFile(f)
}

// isDir reports whether dir is a folder.
func (p Proc) isDir(dir string) (exists, isDir bool) {
	stat := os.Stat
	if p.Stat != nil {
		stat = p.Stat
	}
	fi, err := stat(dir)
	if err != nil {
		return false, false
	}
	return true, fi.IsDir()
}

func (p Proc) env(k string) (string, bool) {
	if p.Getenv != nil {
		return p.Getenv(k)
	}
	return os.LookupEnv(k)
}

func (p Proc) getenv(k string) string {
	v, _ := p.env(k)
	return v
}

func (p Proc) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p Proc) log(line string) {
	if f := p.getenv("FAKE_AGENT_LOG"); f != "" {
		if w, err := os.OpenFile(f, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintln(w, line)
			w.Close()
		}
	}
}

// Main runs the stand-in and returns its exit code.
func Main(p Proc) int {
	p.log("opencode " + strings.Join(p.Args, " "))
	r := &run{Proc: p}
	args := r.flags(p.Args)
	if r.version {
		fmt.Fprintln(p.Stdout, VersionLine(r.binVersion()))
		return 0
	}
	if len(args) == 0 {
		return r.tui(args)
	}
	switch args[0] {
	case "debug":
		if len(args) >= 2 && args[1] == "paths" {
			return r.debugPaths(args[2:])
		}
	case "service":
		if len(args) == 2 && args[1] == "status" {
			fmt.Fprintln(p.Stdout, r.serviceStatus())
			return 0
		}
		if len(args) >= 3 && slices.Contains([]string{"set", "unset", "get"}, args[1]) && args[2] == "port" {
			return r.servicePortCmd(args[1], args[3:])
		}
		if len(args) >= 2 && slices.Contains([]string{"start", "stop", "restart", "set", "unset", "get"}, args[1]) {
			return r.fail("the OpenCode stand-in does not play `opencode service %s`", args[1])
		}
	case "session":
		if len(args) >= 2 {
			return r.session(args[1], args[2:])
		}
	case "api":
		return r.api(args[1:])
	case "auth":
		if len(args) >= 2 && args[1] == "list" {
			return r.authList()
		}
	case "run", "mini":
		return r.runPrompt()
	default:
		if !strings.HasPrefix(args[0], "-") && len(args) == 1 {
			return r.tui(args) // opencode <dir>
		}
	}
	return r.fail("the OpenCode stand-in does not play `opencode %s`", strings.Join(args, " "))
}

// run is one command, with OpenCode's global flags read.
type run struct {
	Proc
	version    bool
	standalone bool
	format     string
	data       string // api -d
	dir        string // session import --directory
	maxCount   int
	resume     string // -s
}

// flags takes OpenCode's flags out of args and returns the words left.
func (r *run) flags(args []string) []string {
	var words []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		name, val, hasVal := strings.Cut(a, "=")
		take := func() string {
			if hasVal {
				return val
			}
			return next()
		}
		switch name {
		case "--version", "-v":
			r.version = true
		case "--standalone":
			r.standalone = true
		case "--format":
			r.format = take()
		case "--data", "-d":
			r.data = take()
		case "--directory":
			r.dir = take()
		case "--max-count", "-n":
			_, _ = fmt.Sscan(take(), &r.maxCount) // not a number: the default
		case "--session", "-s":
			r.resume = take()
		case "--print-logs", "--continue", "-c":
		case "--log-level", "--prompt", "--model", "-m", "--agent", "--header", "-H", "--param":
			take()
		default:
			words = append(words, a)
		}
	}
	return words
}

func (r *run) fail(f string, a ...any) int {
	fmt.Fprintf(r.Stderr, f+"\n", a...)
	return 1
}

func (r *run) binVersion() string {
	if v := r.getenv("FAKE_OPENCODE_VERSION"); v != "" {
		return v
	}
	return Version
}

// tui plays opening a session in the terminal: OpenCode's TUI needs a model, so the
// stand-in only says what it would open. The TUI talks to the background service: it
// starts it, or replaces a running one of another version (as 2.3.0 does), and the
// stand-in logs either.
func (r *run) tui(args []string) int {
	if running := r.serviceURL() != ""; running {
		if v := r.getenv("FAKE_OPENCODE_SERVICE_VERSION"); v != "" && v != r.binVersion() {
			r.log("opencode: replaced the background service " + v + " with " + r.binVersion())
		}
	} else {
		r.server()
	}
	if r.resume != "" {
		fmt.Fprintf(r.Stdout, "opencode (stand-in): would open %s\n", r.resume)
	}
	return 0
}

// runPrompt plays `opencode run` and `mini`, which need a model: it says so and exits 0.
// With $FAKE_OPENCODE_TOOL, `run -s <id>` plays a turn whose model makes that one shell
// call (plugin.go).
func (r *run) runPrompt() int {
	if tool := r.getenv("FAKE_OPENCODE_TOOL"); tool != "" && r.resume != "" {
		return r.turn(tool)
	}
	if r.resume != "" {
		fmt.Fprintf(r.Stdout, "opencode (stand-in): would open %s\n", r.resume)
	}
	return 0
}

// paths are OpenCode's folders (packages/util/src/global-roots.ts): each XDG variable,
// when set and not empty, else under the home folder, on every OS.
type paths struct {
	home, data, config, cache, state, db string
}

func (r *run) paths() paths {
	home := r.getenv("HOME")
	if runtime.GOOS == "windows" && r.getenv("USERPROFILE") != "" {
		home = r.getenv("USERPROFILE")
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	root := func(env string, def ...string) string {
		if v := r.getenv(env); v != "" {
			return filepath.Join(v, "opencode")
		}
		return filepath.Join(append(append([]string{home}, def...), "opencode")...)
	}
	p := paths{home: home, data: root("XDG_DATA_HOME", ".local", "share"), cache: root("XDG_CACHE_HOME", ".cache"),
		config: root("XDG_CONFIG_HOME", ".config"), state: root("XDG_STATE_HOME", ".local", "state")}
	// OPENCODE_CONFIG_DIR replaces the config folder when it is not empty (the CLI and
	// server test it for truth: packages/cli/src/index.ts, server-process.ts).
	if v := r.getenv("OPENCODE_CONFIG_DIR"); v != "" {
		p.config = v
	}
	// OPENCODE_DB names the database, relative to the data folder, when it is set, even
	// empty (`??`: the data folder itself then, which no database can open).
	p.db = filepath.Join(p.data, "opencode.db")
	if v, ok := r.env("OPENCODE_DB"); ok {
		if v == ":memory:" || filepath.IsAbs(v) {
			p.db = v
		} else {
			p.db = filepath.Join(p.data, v)
		}
	}
	return p
}

func (r *run) debugPaths(args []string) int {
	p := r.paths()
	tmp := filepath.Join(tempDir(), "opencode")
	rows := [][2]string{{"home", p.home}, {"data", p.data}, {"cache", p.cache}, {"config", p.config}, {"state", p.state},
		{"tmp", tmp}, {"bin", filepath.Join(p.cache, "bin")}, {"log", filepath.Join(p.data, "log")},
		{"repos", filepath.Join(p.data, "repos")}, {"db", p.db}}
	if len(args) == 1 {
		for _, row := range rows {
			if row[0] == args[0] {
				fmt.Fprintln(r.Stdout, row[1])
				return 0
			}
		}
		return r.fail("ERROR\n  Invalid value for argument <name>: %q. Expected: \"db\" | \"home\" | \"data\" | \"config\" | \"cache\" | \"state\" | \"tmp\" | \"bin\" | \"log\" | \"repos\"", args[0])
	}
	for _, row := range rows {
		fmt.Fprintf(r.Stdout, "%-10s %s\n", row[0], row[1])
	}
	return 0
}

// serviceFile is where the stand-in keeps its make-believe service's address.
func (r *run) serviceFile() string { return filepath.Join(r.paths().state, "fake-service") }

// serviceConfig is the service's settings file, {config}/service.json, which `service set`
// writes (OpenCode's service-config.ts): the stand-in plays its port only.
func (r *run) serviceConfig() string { return filepath.Join(r.paths().config, "service.json") }

// configuredPort is the port `service set port` set (0: OpenCode's default).
func (r *run) configuredPort() int {
	b, err := os.ReadFile(r.serviceConfig())
	if err != nil {
		return 0
	}
	var c struct {
		Port int `json:"port"`
	}
	_ = json.Unmarshal(b, &c)
	return c.Port
}

// servicePortCmd plays `service get|set|unset port`: get prints the port set, or an empty
// line; set and unset stop the service registered in this state folder first (OpenCode
// does, so the next start takes the new port), which the stand-in logs, then write the
// settings file. None of them starts the service.
func (r *run) servicePortCmd(verb string, rest []string) int {
	if r.getenv("FAKE_OPENCODE_FAIL") == "service "+verb {
		return r.fail("Error: the stand-in was told to fail `opencode service %s`", verb)
	}
	if verb == "get" {
		if p := r.configuredPort(); p != 0 {
			fmt.Fprintln(r.Stdout, p)
		} else {
			fmt.Fprintln(r.Stdout)
		}
		return 0
	}
	settings := map[string]any{}
	if b, err := os.ReadFile(r.serviceConfig()); err == nil {
		_ = json.Unmarshal(b, &settings)
	}
	switch verb {
	case "set":
		if len(rest) != 1 {
			return r.fail("Usage: opencode service set port <value>")
		}
		port, err := strconv.Atoi(rest[0])
		if err != nil || port < 1 || port > 65535 {
			return r.fail("Error: Port must be between 1 and 65535")
		}
		settings["port"] = port
	case "unset":
		delete(settings, "port")
	}
	if r.serviceURL() != "" {
		_ = os.Remove(r.serviceFile())
		r.log("opencode: stopped the background service")
	}
	b, _ := json.MarshalIndent(settings, "", "  ")
	_ = os.MkdirAll(filepath.Dir(r.serviceConfig()), 0o700)
	if err := os.WriteFile(r.serviceConfig(), append(b, '\n'), 0o600); err != nil {
		return r.fail("Error: %v", err)
	}
	return 0
}

// port is the port the background service listens on: the one set, else the default.
func (r *run) port() int {
	if p := r.configuredPort(); p != 0 {
		return p
	}
	return servicePort
}

// serviceURL is the running service's address ("" when it is stopped).
func (r *run) serviceURL() string {
	switch v := r.getenv("FAKE_OPENCODE_SERVICE"); {
	case v == "running":
		return fmt.Sprintf("http://127.0.0.1:%d", servicePort)
	case strings.HasPrefix(v, "http"):
		return v
	}
	if b, err := os.ReadFile(r.serviceFile()); err == nil {
		return strings.TrimSpace(string(b))
	}
	return ""
}

func (r *run) serviceStatus() string {
	if u := r.serviceURL(); u != "" {
		return u
	}
	return "stopped"
}

// server is the server a command talks to: a private one with --standalone, else the
// background service, which OpenCode starts when it is stopped.
func (r *run) server() {
	if r.standalone || r.serviceURL() != "" {
		return
	}
	f := r.serviceFile()
	_ = os.MkdirAll(filepath.Dir(f), 0o700)
	_ = os.WriteFile(f, []byte(fmt.Sprintf("http://127.0.0.1:%d\n", r.port())), 0o600)
	r.log("opencode: started the background service")
}

// serverVersion is the version of the server a command talks to.
func (r *run) serverVersion() string {
	if !r.standalone {
		if v := r.getenv("FAKE_OPENCODE_SERVICE_VERSION"); v != "" {
			return v
		}
	}
	return r.binVersion()
}

func (r *run) authList() int {
	r.server()
	if _, err := r.open(); err != nil {
		return r.fail("Error: %v", err)
	}
	out := r.getenv("FAKE_OPENCODE_AUTH")
	if out == "" {
		out = "[]"
	}
	if r.format != "json" {
		fmt.Fprintln(r.Stdout, "The OpenCode stand-in lists credentials only with --format json.")
		return 0
	}
	fmt.Fprintln(r.Stdout, out)
	return 0
}

// workdir is the folder the command runs in.
func (r *run) workdir() string {
	if r.Dir != "" {
		return r.Dir
	}
	d, _ := os.Getwd()
	return d
}

// tempDir is the temporary folder with its links resolved, as OpenCode reports it
// (macOS's /var/folders is /private/var/folders).
func tempDir() string {
	t := strings.TrimRight(os.TempDir(), "/\\")
	if real, err := filepath.EvalSymlinks(t); err == nil {
		return real
	}
	return t
}
