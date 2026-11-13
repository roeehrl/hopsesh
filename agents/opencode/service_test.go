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
	if _, err := m.Live(ctx, h, in, []agent.SessionID{s.Key.Session}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := exportSession(ctx, h, probeService(ctx, h), sesRenamed); err != nil {
		t.Fatal(err)
	}
	cmds := f.commands()
	if len(cmds) < 5 {
		t.Fatalf("commands %v", cmds)
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
	if got := summaryFor(t, l, sesFiles); got.Title != "Fix the greeting" || got.TitleSource != "generated" {
		t.Errorf("after the rename: %q (%s)", got.Title, got.TitleSource)
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

func TestLive(t *testing.T) {
	fh, f := newMachine(t, setup{})
	m, h, in := open(t, fh)
	setRow(t, fh, sesFiles, "time_busy = time_idle + 10")
	setRow(t, fh, sesShell, "time_suspended = 1794597600000")
	setRow(t, fh, sesLong, "time_compacting = 1794597600000")
	ids := []agent.SessionID{sesFiles, sesShell, sesLong, sesRenamed, sesSubagent, "ses_missing"}
	live, err := m.Live(ctx, h, in, ids)
	if err != nil {
		t.Fatal(err)
	}
	want := map[agent.SessionID]agent.LiveInfo{
		sesFiles:      {State: agent.Live, Status: statusWorking},
		sesShell:      {State: agent.Live, Status: statusSuspended},
		sesLong:       {State: agent.Live, Status: statusWorking},
		sesRenamed:    {State: agent.Ended},
		sesSubagent:   {State: agent.Ended},
		"ses_missing": {State: agent.Unknown},
	}
	for sid, w := range want {
		if live[sid].State != w.State || live[sid].Status != w.Status {
			t.Errorf("%s: %+v, want %+v", sid, live[sid], w)
		}
	}
	// The running service's active sessions are open too.
	f.status, f.active = serviceURL, []string{sesRenamed}
	live, _ = m.Live(ctx, h, in, []agent.SessionID{sesRenamed})
	if live[sesRenamed].State != agent.Live {
		t.Errorf("an active session: %+v", live[sesRenamed])
	}
}

func TestStopInterruptsThroughTheService(t *testing.T) {
	fh, f := newMachine(t, setup{})
	m, h, in := open(t, fh)
	setRow(t, fh, sesFiles, "time_busy = time_idle + 10")
	f.status = serviceURL
	f.interupt = func(sid string) bool {
		setRow(t, fh, sid, "time_idle = time_busy + 5")
		return true
	}
	fh.PIDs[4242] = true
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
	// Working with the service stopped: another process with its own server runs it.
	setRow(t, fh, sesFiles, "time_busy = time_idle + 10")
	if err := m.Stop(ctx, h, in, s, time.Second); !errors.Is(err, ErrOwnServer) {
		t.Errorf("a standalone turn: %v", err)
	}
	// Suspended with the service stopped: it waits for the service, which hopsesh never starts.
	setRow(t, fh, sesFiles, "time_busy = NULL, time_suspended = 1794597600000")
	if err := m.Stop(ctx, h, in, s, time.Second); !errors.Is(err, ErrSuspended) {
		t.Errorf("a suspended turn: %v", err)
	}
	// The service runs but did not interrupt, and the row still says working.
	setRow(t, fh, sesFiles, "time_suspended = NULL, time_busy = time_idle + 10")
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
	for _, s := range l.Sessions {
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
