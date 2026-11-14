package opencode

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/roeehrl/hopsesh/internal/testkit/fakeopencode"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
)

// Test machine: a fake machine whose OpenCode data root holds a database built from the
// 2.3.0 sandbox capture (no binary database in git), and a fake `opencode` program.

const (
	home    = "/home/u"
	dataDir = home + "/.local/share/opencode"
	dbPath  = dataDir + "/opencode.db"
	fixture = "testdata/2.3.0"
)

// Sessions of the fixture.
const (
	sesRenamed  = "ses_e29aa1ec8ffes7x6RCUekHyBbf"
	sesShell    = "ses_e29a8e62bffehroVT7xbmzgU1q"
	sesFiles    = "ses_e29a88c86ffeoU3BQ4jmST24T2"
	sesSubagent = "ses_e29a8385effeUhBGvRVeVFrDLo"
	sesChild    = "ses_e29a832f5ffebHEWUP5fBLspvU"
	sesLong     = "ses_e29a7bdf0ffefEVrCCay4aaALo"
	sesForkAll  = "ses_e29a69ba5ffeH6q8sHMV3Anqbz"
	sesForkPre  = "ses_e29a68618ffeNZMsiCtTC4JoKV"
	sesArchived = "ses_e29a5d62effecLvjFnmTVDXAQ5"
	sesParts    = "ses_e29a5465fffek5VRD43HVvlx4P"
	sesImported = "ses_e29a40000ffeHandMadeImport"
	sesImpChild = "ses_e29a3ffffffeHandMadeChild0"
	sesMixed    = "ses_e29a20000ffeSyntheticMix00"
	sesLegacy   = "ses_e29a10000ffeLegacyShape000"
)

// fakeOpenCode answers like OpenCode 2.3.0's program, from the fixture. It records every
// command it was given.
type fakeOpenCode struct {
	mu       sync.Mutex
	fh       *agenttest.FakeHost
	status   string            // what `service status` prints ("stopped" or a URL)
	version  string            // the running service's version (GET /api/info)
	active   []string          // sessions GET /api/session/active lists
	auth     string            // `auth list --format json` output
	exports  map[string]string // session id → export file
	onCall   func(argv []string) (agent.Result, bool)
	calls    [][]string
	envs     [][]string
	interupt func(sid string) bool
	tmp      string // the stand-in's home folder on this machine
}

// standIn plays a command with the stand-in OpenCode (internal/testkit/fakeopencode) over
// the fake machine's database and files: the commands that change sessions, and exports of
// sessions with no fixture export.
func (f *fakeOpenCode) standIn(args []string) agent.Result {
	file, ok := f.fh.SQLFile(dbPath)
	if !ok {
		return agent.Result{Code: 1, Stderr: []byte("Error: no database")}
	}
	env := map[string]string{"HOME": f.tmp, "OPENCODE_DB": file}
	if f.status != "stopped" {
		env["FAKE_OPENCODE_SERVICE"] = f.status
		env["FAKE_OPENCODE_SERVICE_VERSION"] = f.version
	}
	var out, errb bytes.Buffer
	code := fakeopencode.Main(fakeopencode.Proc{Args: args, Dir: home, Stdout: &out, Stderr: &errb,
		Getenv: func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		ReadFile: func(p string) ([]byte, error) {
			if b, ok := f.fh.Get(p); ok {
				return b, nil
			}
			return nil, fs.ErrNotExist
		},
		Stat: func(p string) (os.FileInfo, error) { return f.fh.FS().Stat(p) }})
	return agent.Result{Code: code, Stdout: out.Bytes(), Stderr: errb.Bytes()}
}

// changes reports the commands the stand-in plays.
func changes(words []string) bool {
	switch {
	case len(words) >= 2 && words[0] == "session" && (words[1] == "import" || words[1] == "delete"):
		return true
	case len(words) >= 3 && words[0] == "api" && words[1] == "DELETE":
		return true
	case len(words) >= 3 && words[0] == "api" && words[1] == "POST" && (strings.HasSuffix(words[2], "/move") || strings.HasSuffix(words[2], "/fork")):
		return true
	case len(words) >= 3 && words[0] == "api" && words[1] == "GET" && strings.Contains(words[2], "/message"):
		return true
	}
	return false
}

func (f *fakeOpenCode) run(argv []string, o agent.RunOptions) agent.Result {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), argv...))
	f.envs = append(f.envs, append([]string(nil), o.Env...))
	f.mu.Unlock()
	args := argv[1:]
	if f.onCall != nil {
		if res, ok := f.onCall(args); ok {
			return res
		}
	}
	words := slices.DeleteFunc(append([]string(nil), args...), func(s string) bool { return s == "--standalone" })
	ok := func(s string) agent.Result { return agent.Result{Stdout: []byte(s)} }
	fail := func(code int, s string) agent.Result { return agent.Result{Code: code, Stderr: []byte(s)} }
	if changes(words) {
		return f.standIn(args)
	}
	switch {
	case len(words) == 2 && words[0] == "service" && words[1] == "status":
		return ok(f.status + "\n")
	case len(words) >= 2 && words[0] == "session" && words[1] == "export":
		if _, db := f.fh.SQLFile(dbPath); db {
			return f.standIn(args) // its export of a captured session is the capture's
		}
		file, found := f.exports[words[2]]
		if !found {
			return fail(1, "Session not found: "+words[2])
		}
		b, err := os.ReadFile(filepath.Join(fixture, file))
		if err != nil {
			return fail(1, err.Error())
		}
		return ok(string(b))
	case len(words) >= 2 && words[0] == "auth" && words[1] == "list":
		return ok(f.auth)
	case len(words) >= 3 && words[0] == "api":
		method, path := words[1], words[2]
		switch {
		case method == "GET" && path == "/api/info":
			return ok(fmt.Sprintf(`{"version":%q,"pid":1,"urls":["http://127.0.0.1:49374"],"paths":{"tmp":"/tmp/opencode"}}`, f.version))
		case method == "GET" && path == "/api/session/active":
			m := map[string]any{}
			for _, s := range f.active {
				m[s] = map[string]string{"type": "running"}
			}
			b, _ := json.Marshal(map[string]any{"data": m})
			return ok(string(b))
		case method == "GET" && strings.HasPrefix(path, "/api/session?"):
			return f.sessionPage(path)
		case method == "PATCH" && strings.HasPrefix(path, "/api/session/"):
			var body struct {
				Title string `json:"title"`
			}
			if len(words) < 5 || words[3] != "-d" || json.Unmarshal([]byte(words[4]), &body) != nil {
				return fail(1, "HTTP 400 Bad Request")
			}
			sid := strings.TrimPrefix(path, "/api/session/")
			if strings.Contains(sid, "NotThere") { // the tests' id of a session OpenCode does not have
				return agent.Result{Code: 1, Stdout: []byte(`{"name":"SessionNotFoundError"}`), Stderr: []byte("HTTP 404 Not Found")}
			}
			if f.fh != nil {
				_ = f.fh.ExecSQL(dbPath, "UPDATE session_v2 SET title = ?, time_updated = time_updated + 1 WHERE id = ?", body.Title, sid)
			}
			return agent.Result{}
		case method == "POST" && strings.HasSuffix(path, "/interrupt"):
			sid := strings.TrimSuffix(strings.TrimPrefix(path, "/api/session/"), "/interrupt")
			done := f.interupt != nil && f.interupt(sid)
			return ok(fmt.Sprintf(`{"interrupted":%t}`, done))
		}
		return fail(1, "HTTP 404 Not Found")
	}
	return fail(1, "unknown command "+strings.Join(args, " "))
}

// sessionPage answers GET /api/session from the fixture's listing, a page of ?limit=
// sessions from ?cursor= (an offset here; OpenCode's are opaque). Like OpenCode it gives
// a next cursor even on the last page.
func (f *fakeOpenCode) sessionPage(path string) agent.Result {
	b, err := os.ReadFile(filepath.Join(fixture, "api-session-list.json"))
	if err != nil {
		return agent.Result{Code: 1, Stderr: []byte(err.Error())}
	}
	var all struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(b, &all); err != nil {
		return agent.Result{Code: 1, Stderr: []byte(err.Error())}
	}
	u, err := url.Parse(path)
	if err != nil {
		return agent.Result{Code: 1, Stderr: []byte("HTTP 400 Bad Request")}
	}
	limit, _ := strconv.Atoi(u.Query().Get("limit"))
	from := 0
	if c := u.Query().Get("cursor"); c != "" {
		from, _ = strconv.Atoi(strings.TrimPrefix(c, "at"))
	}
	to := min(len(all.Data), from+limit)
	page := map[string]any{"data": all.Data[from:to], "cursor": map[string]string{"next": fmt.Sprintf("at%d", to)}}
	out, _ := json.Marshal(page)
	return agent.Result{Stdout: out}
}

// commands are the commands run so far, each as one line.
func (f *fakeOpenCode) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, strings.Join(c[1:], " "))
	}
	return out
}

// exportIndex maps session ids to their export fixture.
func exportIndex(t testing.TB) map[string]string {
	t.Helper()
	f, err := os.Open(filepath.Join(fixture, "export-index.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		cols := strings.Split(sc.Text(), "\t")
		if len(cols) >= 2 {
			out[cols[1]] = cols[0]
		}
	}
	return out
}

type setup struct {
	synthetic bool // load the synthetic sessions
	legacy    bool // load the session in OpenCode's pre-2.1 shape
	secrets   bool // load the fake credential rows
	noDB      bool
}

// newMachine is a fake machine with OpenCode's data root, its database and the fake
// program (service stopped unless a test changes it).
func newMachine(t testing.TB, s setup) (*agenttest.FakeHost, *fakeOpenCode) {
	t.Helper()
	fh := agenttest.NewFakeHost(home)
	fh.AddBinary(binName, "opencode v2.3.0")
	f := &fakeOpenCode{fh: fh, status: "stopped", version: "2.3.0", auth: "[]\n", exports: exportIndex(t), tmp: t.TempDir()}
	fh.Programs[binName] = f.run
	for _, d := range []string{"demo", "other", "plugdir", "elsewhere", "demo/sub"} {
		fh.Put(home+"/git/"+d+"/.keep", nil, timeZero)
	}
	if s.noDB {
		fh.Put(dataDir+"/log/opencode.log", []byte("x"), timeZero)
		return fh, f
	}
	files := []string{filepath.Join(fixture, "schema.sql"), filepath.Join(fixture, "data.sql")}
	if s.synthetic {
		files = append(files, "testdata/synthetic/mixed.sql")
	}
	if s.legacy {
		files = append(files, "testdata/synthetic/legacy.sql")
	}
	if s.secrets {
		files = append(files, "testdata/synthetic/secrets.sql")
	}
	fh.LoadSQL(t, dbPath, files...)
	return fh, f
}

// open detects OpenCode on a fake machine and confines a host to it, as the core does.
func open(t testing.TB, fh *agenttest.FakeHost) (*Module, agent.Host, agent.Install) {
	t.Helper()
	m := New()
	in, err := m.Detect(ctx, fh)
	if err != nil {
		t.Fatal(err)
	}
	return m, agent.Confine(fh, m.Spec(), in), in
}

func summaryFor(t testing.TB, l agent.Listing, sid string) agent.Summary {
	t.Helper()
	for _, s := range l.Sessions {
		if string(s.Key.Session) == sid {
			return s
		}
	}
	t.Fatalf("%s is not listed", sid)
	return agent.Summary{}
}
