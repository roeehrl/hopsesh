package opencode

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

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
	switch {
	case len(words) == 2 && words[0] == "service" && words[1] == "status":
		return ok(f.status + "\n")
	case len(words) >= 2 && words[0] == "session" && words[1] == "export":
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
			b, err := os.ReadFile(filepath.Join(fixture, "api-session-list.json"))
			if err != nil {
				return fail(1, err.Error())
			}
			return ok(string(b))
		case method == "PATCH" && strings.HasPrefix(path, "/api/session/"):
			var body struct {
				Title string `json:"title"`
			}
			if len(words) < 5 || words[3] != "-d" || json.Unmarshal([]byte(words[4]), &body) != nil {
				return fail(1, "HTTP 400 Bad Request")
			}
			sid := strings.TrimPrefix(path, "/api/session/")
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
	secrets   bool // load the fake credential rows
	noDB      bool
}

// newMachine is a fake machine with OpenCode's data root, its database and the fake
// program (service stopped unless a test changes it).
func newMachine(t testing.TB, s setup) (*agenttest.FakeHost, *fakeOpenCode) {
	t.Helper()
	fh := agenttest.NewFakeHost(home)
	fh.AddBinary(binName, "opencode v2.3.0")
	f := &fakeOpenCode{fh: fh, status: "stopped", version: "2.3.0", auth: "[]\n", exports: exportIndex(t)}
	fh.Programs[binName] = f.run
	if s.noDB {
		fh.Put(dataDir+"/log/opencode.log", []byte("x"), timeZero)
		return fh, f
	}
	files := []string{filepath.Join(fixture, "schema.sql"), filepath.Join(fixture, "data.sql")}
	if s.synthetic {
		files = append(files, "testdata/synthetic/mixed.sql")
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
