package opencode

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

const serviceURL = "http://127.0.0.1:49374"

func TestServiceProbe(t *testing.T) {
	fh, f := newMachine(t, setup{})
	_, h, _ := open(t, fh)
	for out, want := range map[string]serviceState{"stopped": serviceStopped, serviceURL: serviceRunning, "something else": serviceUnknown} {
		f.status = out
		got := probeService(ctx, h)
		if got.State != want || got.standalone() != (want != serviceRunning) {
			t.Errorf("%q: %+v", out, got)
		}
	}
	// A probe that fails is treated as stopped: nothing may start the service.
	f.onCall = func(argv []string) (agent.Result, bool) { return agent.Result{Code: 1}, argv[0] == "service" }
	if got := probeService(ctx, h); !got.standalone() {
		t.Errorf("a failed probe: %+v", got)
	}
}

// With the service stopped, every command hopsesh runs brings its own server, so none
// starts OpenCode's background service; none stops or restarts it; none updates OpenCode.
func TestCommandsNeverStartTheService(t *testing.T) {
	fh, f := newMachine(t, setup{secrets: true})
	m, h, in := open(t, fh)
	l, _ := m.List(ctx, h, in)
	s := summaryFor(t, l, sesRenamed)
	if err := m.Rename(ctx, h, in, s, "a new title"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Account(ctx, h, in); err != nil {
		t.Fatal(err)
	}
	// A turn in flight: Live and Stop ask OpenCode.
	setRow(t, fh, sesRenamed, "time_busy = time_idle + 10, time_suspended = 1794597600000")
	if _, err := m.Live(ctx, h, in, []agent.SessionID{s.Key.Session}); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(ctx, h, in, s, time.Second); !errors.Is(err, ErrSuspended) {
		t.Fatal(err)
	}
	if _, err := m.ResumeOn(ctx, h, in, s.Key, agent.Placement{CWD: s.CWD}, agent.ResumeOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := exportSession(ctx, h, probeService(ctx, h), sesRenamed); err != nil {
		t.Fatal(err)
	}
	// Where the database cannot be read: the API listing, children and export.
	api := agent.Confine(noSQL{fh}, m.Spec(), in)
	al, err := m.List(ctx, api, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Bundle(ctx, api, in, summaryFor(t, al, sesSubagent)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Live(ctx, api, in, []agent.SessionID{s.Key.Session}); err != nil {
		t.Fatal(err)
	}
	cmds := f.commands()
	for _, want := range []string{"auth list", "api --standalone PATCH", "session export", "api --standalone GET /api/session?"} {
		if !slices.ContainsFunc(cmds, func(c string) bool { return strings.HasPrefix(c, want) }) {
			t.Errorf("no %q among %v", want, cmds)
		}
	}
	for i, c := range cmds {
		switch {
		case c == "service status":
		case strings.Contains(c, "service start") || strings.Contains(c, "service stop") || strings.Contains(c, "service restart"):
			t.Errorf("hopsesh ran %q", c)
		case !strings.Contains(c, "--standalone"):
			t.Errorf("%q would start the background service", c)
		}
		if !slices.Contains(f.envs[i], "OPENCODE_DISABLE_AUTOUPDATE=1") {
			t.Errorf("%q may update OpenCode", c)
		}
	}
	// With the service running, commands use it.
	f.status = serviceURL
	f.calls = nil
	if err := m.Rename(ctx, h, in, s, "another title"); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.commands() {
		if strings.Contains(c, "--standalone") {
			t.Errorf("%q bypasses the running service", c)
		}
	}
}

func TestRename(t *testing.T) {
	fh, f := newMachine(t, setup{})
	m, h, in := open(t, fh)
	l, _ := m.List(ctx, h, in)
	s := summaryFor(t, l, sesFiles)
	if err := m.Rename(ctx, h, in, s, "  Fix the greeting  "); err != nil {
		t.Fatal(err)
	}
	cmds := f.commands()
	want := `api --standalone PATCH /api/session/` + sesFiles + ` -d {"title":"Fix the greeting"}`
	if cmds[len(cmds)-1] != want {
		t.Errorf("rename ran %q, want %q", cmds[len(cmds)-1], want)
	}
	l, _ = m.List(ctx, h, in)
	// hopsesh recorded the title it gave: custom while the session keeps it.
	if got := summaryFor(t, l, sesFiles); got.Title != "Fix the greeting" || got.TitleSource != "custom" {
		t.Errorf("after the rename: %q (%s)", got.Title, got.TitleSource)
	}
	if err := fh.ExecSQL(dbPath, "UPDATE session_v2 SET title = 'Renamed in OpenCode', time_updated = time_updated + 1 WHERE id = ?", sesFiles); err != nil {
		t.Fatal(err)
	}
	l, _ = m.List(ctx, h, in)
	if got := summaryFor(t, l, sesFiles); got.TitleSource != "generated" {
		t.Errorf("renamed since in OpenCode: %q (%s)", got.Title, got.TitleSource)
	}
	if err := m.Rename(ctx, h, in, s, " "); err == nil {
		t.Error("an empty title was accepted")
	}
	f.onCall = func(argv []string) (agent.Result, bool) {
		return agent.Result{Code: 1, Stderr: []byte("HTTP 500 Internal Server Error")}, argv[0] == "api"
	}
	if err := m.Rename(ctx, h, in, s, "x"); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("a failed rename: %v", err)
	}
}

// A private server of an OpenCode older than one that wrote the database changes nothing.
func TestRenameRefusesAnOlderStandaloneBinary(t *testing.T) {
	fh, f := newMachine(t, setup{})
	fh.AddBinary(binName, "opencode v2.2.0")
	m, h, in := open(t, fh)
	err := m.Rename(ctx, h, in, agent.Summary{Key: sessionKey(in, sesFiles)}, "x")
	if err == nil || !strings.Contains(err.Error(), "older") {
		t.Fatalf("rename through an older binary: %v", err)
	}
	for _, c := range f.commands() {
		if strings.HasPrefix(c, "api") {
			t.Errorf("ran %q", c)
		}
	}
	// With the service running, its own version applies.
	f.status = serviceURL
	if err := m.Rename(ctx, h, in, agent.Summary{Key: sessionKey(in, sesFiles)}, "x"); err != nil {
		t.Fatalf("rename through the service: %v", err)
	}
}

// A private server of an OpenCode older than 2.1.0 changes nothing, even where the
// database cannot be read to compare versions.
func TestRenameRefusesABinaryBelowTheFloor(t *testing.T) {
	fh, f := newMachine(t, setup{})
	fh.AddBinary(binName, "opencode v2.0.30")
	m, _, in := open(t, fh)
	h := agent.Confine(noSQL{fh}, m.Spec(), in)
	err := m.Rename(ctx, h, in, agent.Summary{Key: sessionKey(in, sesFiles)}, "x")
	if !errors.Is(err, agent.ErrUnsupported) || !strings.Contains(err.Error(), minVersion) {
		t.Fatalf("rename through a 2.0 binary: %v", err)
	}
	for _, c := range f.commands() {
		if strings.HasPrefix(c, "api") {
			t.Errorf("ran %q", c)
		}
	}
}

func TestResumeCommand(t *testing.T) {
	in := agent.Install{Agent: id}
	c := New().Resume(in, sessionKey(in, sesFiles), agent.Placement{CWD: "/home/u/git/demo"}, agent.ResumeOptions{Prompt: "go on", Fork: true})
	if !slices.Equal(c.Argv, []string{"opencode", "-s", sesFiles, "--prompt", "go on"}) || c.Dir != "/home/u/git/demo" || !slices.Contains(c.Unset, "OPENCODE_SESSION_ID") {
		t.Fatalf("resume %+v", c)
	}
}

func resumeOn(t *testing.T, fh agent.Host, sid string) (agent.Command, error) {
	t.Helper()
	m := New()
	in, err := m.Detect(ctx, fh)
	if err != nil {
		t.Fatal(err)
	}
	h := agent.Confine(fh, m.Spec(), in)
	return m.ResumeOn(ctx, h, in, sessionKey(in, sid), agent.Placement{CWD: "/home/u/git/demo"}, agent.ResumeOptions{})
}

func TestResumeProgramMatchesTheService(t *testing.T) {
	stagedHost := func(t *testing.T) (*osHost, *fakeOpenCode) {
		fh, f := newMachine(t, setup{})
		f.status, f.version = serviceURL, "2.2.7"
		return &osHost{fh, "darwin"}, f
	}
	t.Run("stopped", func(t *testing.T) {
		fh, _ := newMachine(t, setup{})
		c, err := resumeOn(t, fh, sesFiles)
		if err != nil || c.Argv[0] != "opencode" {
			t.Fatalf("%v %v", c.Argv, err)
		}
	})
	t.Run("same version on PATH", func(t *testing.T) {
		fh, f := newMachine(t, setup{})
		f.status = serviceURL
		c, err := resumeOn(t, fh, sesFiles)
		if err != nil || c.Argv[0] != "opencode" {
			t.Fatalf("%v %v", c.Argv, err)
		}
	})
	t.Run("desktop bundled CLI", func(t *testing.T) {
		h, _ := stagedHost(t)
		h.Put("/Applications/OpenCode.app/Contents/Resources/opencode-cli", []byte("bin"), timeZero)
		h.Put("/Applications/OpenCode.app/Contents/Resources/opencode-cli.version", []byte("2.2.7\n"), timeZero)
		c, err := resumeOn(t, h, sesFiles)
		if err != nil || c.Argv[0] != "/Applications/OpenCode.app/Contents/Resources/opencode-cli" {
			t.Fatalf("%v %v", c.Argv, err)
		}
	})
	t.Run("desktop staged CLI", func(t *testing.T) {
		h, _ := stagedHost(t)
		dir := home + "/Library/Application Support/ai.opencode.desktop/cli"
		h.Put(dir+"/2.2.7-x64/opencode-cli", []byte("bin"), timeZero) // another architecture
		h.Put(dir+"/2.2.6-arm64/opencode-cli", []byte("bin"), timeZero)
		h.Put(dir+"/2.2.7-arm64/opencode-cli", []byte("bin"), timeZero)
		c, err := resumeOn(t, h, sesFiles)
		if err != nil || c.Argv[0] != dir+"/2.2.7-arm64/opencode-cli" {
			t.Fatalf("%v %v", c.Argv, err)
		}
	})
	t.Run("none: would restart the service", func(t *testing.T) {
		h, _ := stagedHost(t)
		_, err := resumeOn(t, h, sesFiles)
		var re *restartError
		if !errors.As(err, &re) || re.Service != "2.2.7" || !strings.Contains(err.Error(), "restart") {
			t.Fatalf("%v", err)
		}
	})
	t.Run("probe failed", func(t *testing.T) {
		// The service may run (here 2.2.7, the PATH opencode 2.3.0): the TUI could replace it.
		h, f := stagedHost(t)
		f.onCall = func(argv []string) (agent.Result, bool) {
			return agent.Result{Code: 1, Stderr: []byte("unexpected")}, argv[0] == "service"
		}
		c, err := resumeOn(t, h, sesFiles)
		if err == nil || !strings.Contains(err.Error(), "restart") {
			t.Fatalf("resumed with %v after a failed probe: %v", c.Argv, err)
		}
	})
	t.Run("service older than 2.1.0", func(t *testing.T) {
		h, f := stagedHost(t)
		f.version = "2.0.30"
		h.Put("/Applications/OpenCode.app/Contents/Resources/opencode-cli", []byte("bin"), timeZero)
		h.Put("/Applications/OpenCode.app/Contents/Resources/opencode-cli.version", []byte("2.0.30\n"), timeZero)
		if _, err := resumeOn(t, h, sesFiles); !errors.Is(err, agent.ErrUnsupported) {
			t.Fatalf("resume with a 2.0 service: %v", err)
		}
	})
	t.Run("service version unknown", func(t *testing.T) {
		fh, f := newMachine(t, setup{})
		f.status = serviceURL
		f.onCall = func(argv []string) (agent.Result, bool) {
			return agent.Result{Code: 1}, len(argv) > 2 && argv[2] == "/api/info"
		}
		if _, err := resumeOn(t, fh, sesFiles); err == nil {
			t.Fatal("resumed with the service's version unknown")
		}
	})
}

// OpenCode's -s makes an empty session for an id it does not have: no command is given
// for a session that is not there.
func TestResumeOnlyAnExistingSession(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	if _, err := resumeOn(t, fh, "ses_e29a00000ffeNotThereAtAll0"); !errors.Is(err, agent.ErrNotFound) {
		t.Fatalf("resume of a missing session: %v", err)
	}
}

func setRow(t *testing.T, fh *agenttest.FakeHost, sid, set string, args ...any) {
	t.Helper()
	if err := fh.ExecSQL(dbPath, "UPDATE session_v2 SET "+set+" WHERE id = ?", append(args, sid)...); err != nil {
		t.Fatal(err)
	}
}

// Rows as OpenCode writes them: a turn claims its session (time_suspended) and sets
// time_busy when it starts; it releases the claim and sets time_idle when it ends; a
// service that shuts down mid-turn keeps the claim and sets no idle.
func TestLive(t *testing.T) {
	fh, f := newMachine(t, setup{})
	m, h, in := open(t, fh)
	ids := []agent.SessionID{sesFiles, sesShell, sesRenamed, sesSubagent, "ses_missing"}
	// No turn in flight: the rows answer, nothing is run.
	live, err := m.Live(ctx, h, in, ids)
	if err != nil {
		t.Fatal(err)
	}
	for sid, w := range map[agent.SessionID]agent.Liveness{sesFiles: agent.Ended, sesRenamed: agent.Ended, "ses_missing": agent.Unknown} {
		if live[sid].State != w {
			t.Errorf("%s: %+v, want %s", sid, live[sid], w)
		}
	}
	if c := f.commands(); len(c) != 0 {
		t.Errorf("Live with nothing in flight ran %v", c)
	}
	inFlight := "time_busy = time_idle + 10, time_suspended = 1794597600000"
	setRow(t, fh, sesFiles, inFlight)
	setRow(t, fh, sesShell, inFlight)
	// The service is stopped: a claimed turn runs in a window with its own server, or is
	// suspended until the service starts.
	live, _ = m.Live(ctx, h, in, ids)
	for _, sid := range []agent.SessionID{sesFiles, sesShell} {
		if live[sid].State != agent.Live || live[sid].Status != statusSuspended {
			t.Errorf("%s with the service stopped: %+v", sid, live[sid])
		}
	}
	// The service runs one of them; the other runs in a window with its own server.
	f.status, f.active = serviceURL, []string{sesFiles}
	live, _ = m.Live(ctx, h, in, ids)
	for _, sid := range []agent.SessionID{sesFiles, sesShell} {
		if live[sid].State != agent.Live || live[sid].Status != statusWorking {
			t.Errorf("%s with the service running: %+v", sid, live[sid])
		}
	}
	if live[sesRenamed].State != agent.Ended || live[sesSubagent].State != agent.Ended {
		t.Errorf("idle sessions: %+v %+v", live[sesRenamed], live[sesSubagent])
	}
	// Before OpenCode 2.2.2 rows have no time_busy: the claim alone says a turn is in flight.
	f.status, f.active = "stopped", nil
	setRow(t, fh, sesFiles, "time_busy = NULL")
	if live, _ = m.Live(ctx, h, in, []agent.SessionID{sesFiles}); live[sesFiles].Status != statusSuspended {
		t.Errorf("a claim without time_busy: %+v", live[sesFiles])
	}
	// An ended turn: idle after busy, claim released.
	setRow(t, fh, sesFiles, "time_busy = time_idle - 10, time_suspended = NULL")
	if live, _ = m.Live(ctx, h, in, []agent.SessionID{sesFiles}); live[sesFiles].State != agent.Ended {
		t.Errorf("an ended turn: %+v", live[sesFiles])
	}
}

// Where the database cannot be read, the running service says which sessions it runs.
func TestLiveThroughTheAPI(t *testing.T) {
	fh, f := newMachine(t, setup{})
	m, _, in := open(t, fh)
	h := agent.Confine(noSQL{fh}, m.Spec(), in)
	ids := []agent.SessionID{sesFiles, sesRenamed}
	live, err := m.Live(ctx, h, in, ids)
	if err != nil || live[sesFiles].State != agent.Unknown || live[sesRenamed].State != agent.Unknown {
		t.Fatalf("service stopped, no database: %+v %v", live, err)
	}
	f.status, f.active = serviceURL, []string{sesFiles}
	live, err = m.Live(ctx, h, in, ids)
	if err != nil || live[sesFiles].State != agent.Live || live[sesRenamed].State != agent.Ended {
		t.Fatalf("service running, no database: %+v %v", live, err)
	}
}

func TestStopInterruptsThroughTheService(t *testing.T) {
	fh, f := newMachine(t, setup{})
	m, h, in := open(t, fh)
	setRow(t, fh, sesFiles, "time_busy = time_idle + 10, time_suspended = 1794597600000")
	f.status, f.active = serviceURL, []string{sesFiles}
	f.interupt = func(sid string) bool {
		setRow(t, fh, sid, "time_idle = time_busy + 5, time_suspended = NULL")
		f.active = nil
		return true
	}
	// An OpenCode process holds the database; Stop must not signal it.
	fh.PIDs[4242] = true
	fh.PIDNames[4242] = "opencode"
	fh.LocksHeld[dbPath] = true
	fh.LockHolders[dbPath] = []int{4242}
	if err := m.Stop(ctx, h, in, agent.Summary{Key: sessionKey(in, sesFiles)}, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.commands(), "api POST /api/session/"+sesFiles+"/interrupt") {
		t.Errorf("commands %v", f.commands())
	}
	if !fh.PIDs[4242] {
		t.Error("a process was signalled")
	}
}

func TestStopCases(t *testing.T) {
	fh, f := newMachine(t, setup{})
	m, h, in := open(t, fh)
	s := agent.Summary{Key: sessionKey(in, sesFiles)}
	// Not working: nothing to stop, nothing run but probes.
	if err := m.Stop(ctx, h, in, s, time.Second); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.commands() {
		if strings.Contains(c, "interrupt") {
			t.Errorf("ran %q", c)
		}
	}
	// A claimed turn with the service stopped (as a shutdown leaves it, and as a window
	// with its own server runs it): it says both.
	setRow(t, fh, sesFiles, "time_busy = time_idle + 10, time_suspended = 1794597600000")
	err := m.Stop(ctx, h, in, s, time.Second)
	if !errors.Is(err, ErrSuspended) || !strings.Contains(err.Error(), "own server") || !strings.Contains(err.Error(), "starts again") {
		t.Errorf("a claimed turn: %v", err)
	}
	// Busy without a claim, with the service stopped: a process with its own server.
	setRow(t, fh, sesFiles, "time_suspended = NULL")
	if err := m.Stop(ctx, h, in, s, time.Second); !errors.Is(err, ErrOwnServer) {
		t.Errorf("an unclaimed busy turn: %v", err)
	}
	// The service runs but did not interrupt, and the row still says working.
	setRow(t, fh, sesFiles, "time_suspended = 1794597600000")
	f.status = serviceURL
	if err := m.Stop(ctx, h, in, s, time.Second); !errors.Is(err, ErrOwnServer) {
		t.Errorf("a turn the service does not run: %v", err)
	}
	// Interrupted, but the turn does not end in time.
	f.interupt = func(string) bool { return true }
	if err := m.Stop(ctx, h, in, s, 300*time.Millisecond); !errors.Is(err, ErrStillRunning) {
		t.Errorf("a turn that does not end: %v", err)
	}
}

func TestAccount(t *testing.T) {
	fh, f := newMachine(t, setup{})
	m, h, in := open(t, fh)
	a, err := m.Account(ctx, h, in)
	if err != nil || a.LoggedIn || a.Key != "" || a.Confidence != "limited" {
		t.Fatalf("no sign-ins: %+v %v", a, err)
	}
	f.auth = `[{"id":"openai","name":"OpenAI","connections":[{"type":"credential","id":"cred_1","label":"work","method":"api","status":"active"}]},
{"id":"anthropic","name":"Anthropic","connections":[{"type":"env","name":"ANTHROPIC_API_KEY"}]},
{"id":"groq","name":"Groq","connections":[]}]`
	a, err = m.Account(ctx, h, in)
	if err != nil || !a.LoggedIn || a.Key != "" || a.Person != "" || a.Label != "Anthropic, OpenAI" || a.Observation != "anthropic,openai" {
		t.Fatalf("sign-ins: %+v %v", a, err)
	}
	if c := f.commands(); c[len(c)-1] != "auth list --format json --standalone" {
		t.Errorf("ran %q", c[len(c)-1])
	}
	f.auth = "not json"
	if _, err := m.Account(ctx, h, in); err == nil {
		t.Error("garbage accepted")
	}
}

// No read of the module, whatever it does, reaches the credential, account, permission
// or workspace tables (the fake rows are there to be found).
func TestDeniedTablesAreNeverRead(t *testing.T) {
	fh, f := newMachine(t, setup{synthetic: true, secrets: true})
	f.status = serviceURL
	f.auth = `[{"id":"openai","name":"OpenAI","connections":[{"type":"credential"}]}]`
	m, h, in := open(t, fh)
	l, err := m.List(ctx, h, in)
	if err != nil {
		t.Fatal(err)
	}
	var ids []agent.SessionID
	for i, s := range l.Sessions {
		if i == 0 {
			if err := m.Rename(ctx, h, in, s, "renamed"); err != nil {
				t.Fatal(err)
			}
			if _, err := m.ResumeOn(ctx, h, in, s.Key, agent.Placement{CWD: s.CWD}, agent.ResumeOptions{}); err != nil && !errors.As(err, new(*restartError)) {
				t.Fatal(err)
			}
		}
		ids = append(ids, s.Key.Session)
		if _, err := m.Read(ctx, h, in, s, ir.Cursor{}); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Preview(ctx, h, in, s, 3); err != nil {
			t.Fatal(err)
		}
		if _, err := m.ContextCapacity(ctx, h, in, &s); err != nil {
			t.Fatal(err)
		}
		b, err := m.Bundle(ctx, h, in, s)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range b.Files {
			if _, err := m.Materialize(ctx, h, in, s, file); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := m.SessionStamp(ctx, h, in, s.Key.Session); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Live(ctx, h, in, ids); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Account(ctx, h, in); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ContextCapacity(ctx, h, in, nil); err != nil {
		t.Fatal(err)
	}
	reads := fh.SQLReads()
	if len(reads) == 0 {
		t.Fatal("the spy saw no reads")
	}
	for _, table := range reads {
		if slices.Contains(sqlDenied, table) || !slices.Contains(sqlTables, table) && !slices.Contains(agent.SQLTableFuncs(), table) {
			t.Errorf("read the table %s", table)
		}
	}
}

// Decision D2: with no program of the running service's version here, a resume is
// refused as one that restarts the service (agent.ErrRestartsService); once the person
// agrees, it runs the PATH opencode.
func TestResumeRestartingTheService(t *testing.T) {
	fh, f := newMachine(t, setup{})
	f.status, f.version = serviceURL, "2.2.7"
	m, h, in := open(t, fh)
	key := sessionKey(in, sesFiles)
	pl := agent.Placement{CWD: "/home/u/git/demo"}
	_, err := m.ResumeOn(ctx, h, in, key, pl, agent.ResumeOptions{})
	if !errors.Is(err, agent.ErrRestartsService) || !strings.Contains(err.Error(), "--restart-service") {
		t.Fatalf("not asked: %v", err)
	}
	c, err := m.ResumeOn(ctx, h, in, key, pl, agent.ResumeOptions{RestartService: true})
	if err != nil || c.Argv[0] != "opencode" && !strings.HasSuffix(c.Argv[0], "/opencode") {
		t.Fatalf("agreed: %v %v", c.Argv, err)
	}
	// Agreeing changes nothing when the service is stopped or of the program's version.
	f.version = "2.3.0"
	if c, err = m.ResumeOn(ctx, h, in, key, pl, agent.ResumeOptions{RestartService: true}); err != nil || !strings.HasSuffix(c.Argv[0], "opencode") {
		t.Fatalf("same version: %v %v", c.Argv, err)
	}
}
