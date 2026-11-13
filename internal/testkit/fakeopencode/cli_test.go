package fakeopencode

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/testkit/fakecloud"
)

// machine is a home with the stand-in's environment: XDG and OpenCode variables unset.
type machine struct {
	t    *testing.T
	home string
	env  map[string]string
	dir  string
}

func newMachine(t *testing.T) *machine {
	h := t.TempDir()
	return &machine{t: t, home: h, env: map[string]string{"HOME": h, "USERPROFILE": h, "FAKE_AGENT_LOG": filepath.Join(h, "calls.log")}, dir: h}
}

type result struct {
	code           int
	stdout, stderr string
}

func (m *machine) run(args ...string) result {
	var out, errOut bytes.Buffer
	code := Main(Proc{Args: args, Dir: m.dir, Stdout: &out, Stderr: &errOut, Getenv: func(k string) (string, bool) {
		v, ok := m.env[k]
		return v, ok
	}})
	return result{code, out.String(), errOut.String()}
}

func (m *machine) must(args ...string) string {
	m.t.Helper()
	r := m.run(args...)
	if r.code != 0 {
		m.t.Fatalf("opencode %s: %d %s%s", strings.Join(args, " "), r.code, r.stdout, r.stderr)
	}
	return r.stdout
}

func (m *machine) log() string {
	b, _ := os.ReadFile(m.env["FAKE_AGENT_LOG"])
	return string(b)
}

// fixtureFile writes a fixture export, changed by edit, and returns its path.
func (m *machine) fixtureFile(name string, edit func(map[string]any)) string {
	m.t.Helper()
	b, err := os.ReadFile(filepath.Join(fixture, name))
	if err != nil {
		m.t.Fatal(err)
	}
	if edit != nil {
		var d map[string]any
		if err := json.Unmarshal(b, &d); err != nil {
			m.t.Fatal(err)
		}
		edit(d)
		b, _ = json.Marshal(d)
	}
	p := filepath.Join(m.t.TempDir(), name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		m.t.Fatal(err)
	}
	return p
}

func TestVersion(t *testing.T) {
	m := newMachine(t)
	if got := strings.TrimSpace(m.must("--version")); got != fakecloud.OpenCodeVersionLine || got != VersionLine(Version) {
		t.Errorf("--version prints %q; fakecloud says %q", got, fakecloud.OpenCodeVersionLine)
	}
	m.env["FAKE_OPENCODE_VERSION"] = "2.2.7"
	if got := strings.TrimSpace(m.must("--version")); got != "opencode v2.2.7" {
		t.Errorf("--version prints %q", got)
	}
}

// The folders follow OpenCode's rules: an empty XDG variable is as unset, an empty
// OPENCODE_DB is set (the data folder itself, which no database opens).
func TestPaths(t *testing.T) {
	m := newMachine(t)
	m.env["XDG_DATA_HOME"] = ""
	if got, want := strings.TrimSpace(m.must("debug", "paths", "db")), filepath.Join(m.home, ".local", "share", "opencode", "opencode.db"); got != want {
		t.Errorf("db %s, want %s", got, want)
	}
	m.env["XDG_DATA_HOME"] = filepath.Join(m.home, "xdg")
	m.env["OPENCODE_DB"] = "other.db"
	if got, want := strings.TrimSpace(m.must("debug", "paths", "db")), filepath.Join(m.home, "xdg", "opencode", "other.db"); got != want {
		t.Errorf("db %s, want %s", got, want)
	}
	if !strings.Contains(m.must("debug", "paths"), "state      "+filepath.Join(m.home, ".local", "state", "opencode")) {
		t.Error("debug paths does not list the state folder")
	}
	m.env["OPENCODE_DB"] = ""
	if r := m.run("session", "list", "--standalone"); r.code == 0 || !strings.Contains(r.stderr, "is a folder") {
		t.Errorf("an empty OPENCODE_DB: %+v", r)
	}
}

// The background service is stopped until a command without --standalone starts it, as
// OpenCode does; the stand-in logs that.
func TestService(t *testing.T) {
	m := newMachine(t)
	if got := m.must("service", "status"); got != "stopped\n" {
		t.Errorf("status %q", got)
	}
	m.must("session", "list", "--standalone")
	if strings.Contains(m.log(), "started the background service") || m.must("service", "status") != "stopped\n" {
		t.Fatal("--standalone started the service")
	}
	m.must("session", "list")
	if !strings.Contains(m.log(), "opencode: started the background service") || m.must("service", "status") != "http://127.0.0.1:49374\n" {
		t.Error("a command without --standalone did not start the service")
	}
	m = newMachine(t)
	m.env["FAKE_OPENCODE_SERVICE"] = "running"
	m.env["FAKE_OPENCODE_SERVICE_VERSION"] = "2.2.7"
	if got := m.must("service", "status"); got != "http://127.0.0.1:49374\n" {
		t.Errorf("status %q", got)
	}
	if got := m.must("api", "GET", "/api/info"); !strings.Contains(got, `"version":"2.2.7"`) {
		t.Errorf("info %s", got)
	}
	if got := m.must("api", "--standalone", "GET", "/api/info"); !strings.Contains(got, `"version":"2.3.0"`) {
		t.Errorf("a private server's info %s", got)
	}
	if r := m.run("service", "restart"); r.code == 0 {
		t.Error("service restart is played")
	}
}

// Import answers as OpenCode 2.3.0's does (the sandbox's F11 findings).
func TestImport(t *testing.T) {
	m := newMachine(t)
	repo := filepath.Join(m.home, "demo")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	parent, child := m.fixtureFile("export-13.json", nil), m.fixtureFile("export-14.json", nil)
	if r := m.run("session", "import", child, "--directory", repo, "--standalone"); r.code != 1 || r.stderr != "Error: Failed to import session: Not Found\n" {
		t.Errorf("a child before its parent: %+v", r)
	}
	if got := m.must("session", "import", parent, "--directory", repo, "--standalone"); got != "Imported session: ses_e29a40000ffeHandMadeImport\n" {
		t.Errorf("import printed %q", got)
	}
	if r := m.run("session", "import", parent, "--directory", repo, "--standalone"); r.code != 0 || r.stdout != "" || r.stderr != "Session already exists\n" {
		t.Errorf("a second import: %+v", r)
	}
	m.must("session", "import", child, "--directory", repo, "--standalone")
	noProject := m.fixtureFile("export-12.json", func(d map[string]any) { delete(d["info"].(map[string]any), "projectID") })
	if r := m.run("session", "import", noProject, "--directory", repo, "--standalone"); r.code != 1 || r.stderr != "Error: Failed to import session: Bad Request\n" {
		t.Errorf("no projectID: %+v", r)
	}
	taken := m.fixtureFile("export-13.json", func(d map[string]any) { d["info"].(map[string]any)["id"] = "ses_e29a40000ffeAnotherCopy000" })
	if r := m.run("session", "import", taken, "--directory", repo, "--standalone"); r.code != 1 || r.stderr != "Error: Failed to import session: Internal Server Error\n" {
		t.Errorf("message ids taken: %+v", r)
	}
	if r := m.run("session", "import", parent, "--directory", filepath.Join(m.home, "gone"), "--standalone"); r.code != 1 || !strings.HasPrefix(r.stderr, "LocationNotFoundError: Location not found: ") {
		t.Errorf("a folder that is not there: %+v", r)
	}
	// Delete takes the children with it.
	if got := m.must("session", "delete", "ses_e29a40000ffeHandMadeImport", "--standalone"); got != "Session ses_e29a40000ffeHandMadeImport deleted\n" {
		t.Errorf("delete printed %q", got)
	}
	if r := m.run("session", "export", "ses_e29a3ffffffeHandMadeChild0", "--standalone"); r.code != 1 || r.stderr != "Session not found: ses_e29a3ffffffeHandMadeChild0\n" {
		t.Errorf("the child after its parent's delete: %+v", r)
	}
}

// The API calls hopsesh makes: rename, list by pages, fork, move, interrupt, delete.
func TestAPI(t *testing.T) {
	m := newMachine(t)
	repo := gitRepo(t, "https://example.com/u/demo.git")
	other := gitRepo(t, "https://example.com/u/other.git")
	m.dir = repo
	for _, f := range []string{"export-01.json", "export-03.json", "export-06.json", "export-13.json", "export-14.json"} {
		m.must("session", "import", m.fixtureFile(f, nil), "--standalone")
	}
	sid := "ses_e29aa1ec8ffes7x6RCUekHyBbf"
	if r := m.run("api", "--standalone", "PATCH", "/api/session/"+sid, "-d", `{"title":"new title"}`); r.code != 0 || r.stdout != "" {
		t.Errorf("rename: %+v", r)
	}
	if got := m.must("api", "--standalone", "GET", "/api/session/"+sid); !strings.Contains(got, `"title":"new title"`) {
		t.Errorf("after rename: %s", got)
	}
	missing := m.run("api", "--standalone", "GET", "/api/session/ses_e29a3ffe0ffeNoSuchSession0")
	if missing.code != 1 || missing.stderr != "HTTP 404 Not Found\n" || !strings.HasPrefix(missing.stdout, `{"_tag":"SessionNotFoundError","sessionID":"ses_e29a3ffe0ffeNoSuchSession0"`) {
		t.Errorf("a missing session: %+v", missing)
	}
	// Pages of two, by cursor, every session once (children included).
	seen := map[string]bool{}
	path := "/api/session?limit=2&order=desc"
	for i := 0; i < 5; i++ {
		var page struct {
			Data   []struct{ ID string } `json:"data"`
			Cursor struct{ Next string } `json:"cursor"`
		}
		if err := json.Unmarshal([]byte(m.must("api", "--standalone", "GET", path)), &page); err != nil {
			t.Fatal(err)
		}
		for _, s := range page.Data {
			if seen[s.ID] {
				t.Errorf("%s listed twice", s.ID)
			}
			seen[s.ID] = true
		}
		if len(page.Data) < 2 {
			break
		}
		path = "/api/session?limit=2&cursor=" + page.Cursor.Next
	}
	if len(seen) != 5 {
		t.Errorf("the pages list %d sessions, want 5", len(seen))
	}
	// A fork copies the messages, before one or through the last.
	var forked struct {
		Data struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Fork  struct {
				SessionID string `json:"sessionID"`
				Boundary  struct{ Type, MessageID string }
			} `json:"fork"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(m.must("api", "--standalone", "POST", "/api/session/"+sid+"/fork", "-d", `{}`)), &forked); err != nil {
		t.Fatal(err)
	}
	if forked.Data.Title != "new title (fork #1)" || forked.Data.Fork.SessionID != sid || forked.Data.Fork.Boundary.Type != "through" {
		t.Errorf("fork: %+v", forked)
	}
	var copied struct{ Messages []struct{ ID string } }
	if err := json.Unmarshal([]byte(m.must("session", "export", forked.Data.ID, "--standalone")), &copied); err != nil || len(copied.Messages) != 3 || !strings.HasSuffix(copied.Messages[2].ID, "_3") {
		t.Errorf("the fork's messages: %+v %v", copied, err)
	}
	// A move switches the folder at once, with OpenCode's location-switched message.
	if r := m.run("api", "--standalone", "POST", "/api/session/"+sid+"/move", "-d", `{"directory":"`+filepath.Join(m.home, "gone")+`"}`); r.code != 1 || r.stderr != "HTTP 400 Bad Request\n" || !strings.Contains(r.stdout, "Directory does not exist") {
		t.Errorf("a move to a missing folder: %+v", r)
	}
	b, _ := json.Marshal(map[string]string{"directory": other})
	m.must("api", "--standalone", "POST", "/api/session/"+sid+"/move", "-d", string(b))
	exp := m.must("session", "export", sid, "--standalone")
	if !strings.Contains(exp, `"type": "location-switched"`) || !strings.Contains(exp, `"directory": "`+other+`"`) {
		t.Errorf("after the move: %s", exp)
	}
	// Interrupt ends only a running turn.
	if got := m.must("api", "--standalone", "POST", "/api/session/"+sid+"/interrupt"); got != "{\"interrupted\":false}\n" {
		t.Errorf("an idle session: %s", got)
	}
	m.env["FAKE_OPENCODE_ACTIVE"] = sid
	if got := m.must("api", "--standalone", "GET", "/api/session/active"); got != `{"data":{"`+sid+`":{"type":"running"}}}`+"\n" {
		t.Errorf("active: %s", got)
	}
	if got := m.must("api", "--standalone", "POST", "/api/session/"+sid+"/interrupt"); got != "{\"interrupted\":true}\n" {
		t.Errorf("a running session: %s", got)
	}
	if got := m.must("api", "--standalone", "GET", "/api/session/active"); got != "{\"data\":{}}\n" {
		t.Errorf("active after the interrupt: %s", got)
	}
	if r := m.run("api", "--standalone", "DELETE", "/api/session/"+sid); r.code != 0 || r.stdout != "" {
		t.Errorf("delete: %+v", r)
	}
	if r := m.run("api", "--standalone", "GET", "/api/nothing"); r.code != 1 || r.stdout != "" || r.stderr != "HTTP 404 Not Found\n" {
		t.Errorf("an unknown path: %+v", r)
	}
	if strings.Contains(m.log(), "started the background service") {
		t.Error("a --standalone call started the service")
	}
}
