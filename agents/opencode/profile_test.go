package opencode

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

const acct = home + "/.hopsesh/accounts/opencode/work"

// added is an account folder hopsesh made for OpenCode (not the default one).
func added() *agent.RuntimeProfile {
	return &agent.RuntimeProfile{ID: "work", Agent: id, Name: "Work", Root: acct, Endpoint: "e1", Managed: true}
}

// An added folder holds the data, state and config folders and the database; its
// commands get the folder's variables, and never the server passwords of the shell.
func TestAccountFolderRoots(t *testing.T) {
	spec := New().Spec()
	if spec.Accounts == nil || spec.Accounts.InheritsSignIn || !slices.Equal(spec.Accounts.Login, []string{"auth", "login", "--standalone"}) {
		t.Fatalf("accounts: %+v", spec.Accounts)
	}
	for _, c := range []struct{ goos, root, data, config, state string }{
		{"darwin", acct, acct + "/data/opencode", acct + "/config", acct + "/state/opencode"},
		{"windows", `C:\Users\u\.hopsesh\accounts\opencode\work`, `C:\Users\u\.hopsesh\accounts\opencode\work\data\opencode`,
			`C:\Users\u\.hopsesh\accounts\opencode\work\config`, `C:\Users\u\.hopsesh\accounts\opencode\work\state\opencode`},
	} {
		none := func(string) string { return "" }
		got := []string{spec.ProfileLocate(rootData, c.root, c.goos, none), spec.ProfileLocate(rootConfig, c.root, c.goos, none), spec.ProfileLocate(rootState, c.root, c.goos, none)}
		if !slices.Equal(got, []string{c.data, c.config, c.state}) {
			t.Errorf("%s: roots %q", c.goos, got)
		}
		p := added()
		p.Root = c.root
		in := spec.PinMain(agent.Install{Agent: id, OS: c.goos, Accounts: spec.Accounts, Profile: p}, c.root)
		if in.Main() != c.data {
			t.Errorf("%s: main root %s", c.goos, in.Main())
		}
	}
	p := added()
	in := agent.Install{Agent: id, Accounts: spec.Accounts, Profile: p}
	cmd := in.ScopeCommand(agent.Command{Argv: []string{"opencode", "-s", "ses_x"},
		Env: []string{"XDG_DATA_HOME=/elsewhere", "OPENCODE_DB=/home/u/.local/share/opencode/opencode.db", "OPENCODE_PASSWORD=x", "TERM=xterm"}})
	want := []string{"TERM=xterm", "XDG_DATA_HOME=" + acct + "/data", "XDG_STATE_HOME=" + acct + "/state",
		"OPENCODE_CONFIG_DIR=" + acct + "/config", "OPENCODE_DB=" + acct + "/data/opencode/opencode.db"}
	if !slices.Equal(cmd.Env, want) {
		t.Errorf("env %q", cmd.Env)
	}
	for _, k := range []string{"OPENCODE_PASSWORD", "OPENCODE_SERVER_PASSWORD", "XDG_DATA_HOME", "OPENCODE_DB"} {
		if !slices.Contains(cmd.Unset, k) {
			t.Errorf("%s is not unset: %q", k, cmd.Unset)
		}
	}
	// The default folder keeps the machine's variables: nothing pins it, a caller cannot
	// point it elsewhere, and the shell's own (OPENCODE_DB included) stay.
	def := agent.Install{Agent: id, Accounts: spec.Accounts, Profile: &agent.RuntimeProfile{ID: "d", Default: true, Root: dataDir}}
	cmd = def.ScopeCommand(agent.Command{Argv: []string{"opencode"}, Env: []string{"XDG_DATA_HOME=/elsewhere", "TERM=xterm"}})
	if !slices.Equal(cmd.Env, []string{"TERM=xterm"}) || slices.Contains(cmd.Unset, "XDG_DATA_HOME") || slices.Contains(cmd.Unset, "OPENCODE_DB") {
		t.Errorf("default: env %q unset %q", cmd.Env, cmd.Unset)
	}
}

// Detected with an added folder's variables, OpenCode's roots and database are inside
// it, and its sessions are listed under that folder.
func TestAccountFolderDetectAndList(t *testing.T) {
	fh, _ := newMachine(t, setup{noDB: true})
	spec := New().Spec()
	for _, e := range spec.Accounts.EnvFor(acct, agent.PosixPath{}) {
		k, v, _ := strings.Cut(e, "=")
		fh.SetEnv(k, v)
	}
	db := acct + "/data/opencode/opencode.db"
	fh.LoadSQL(t, db, filepath.Join(fixture, "schema.sql"), filepath.Join(fixture, "data.sql"))
	m := New()
	in, err := m.Detect(ctx, fh)
	if err != nil {
		t.Fatal(err)
	}
	in.Profile = added()
	if in.Main() != acct+"/data/opencode" || in.Root(rootConfig) != acct+"/config" || in.Root(rootState) != acct+"/state/opencode" || in.Root(rootDB) != db {
		t.Fatalf("roots %v", in.Roots)
	}
	l, err := m.List(ctx, agent.Confine(fh, m.Spec(), in), in)
	if err != nil || len(l.Sessions) == 0 {
		t.Fatalf("listed %d: %v", len(l.Sessions), err)
	}
	for _, s := range l.Sessions {
		if s.Key.Profile != "work" || !strings.HasPrefix(s.Path, acct+"/data/opencode/hopsesh/opencode/sessions/") {
			t.Fatalf("%v at %s", s.Key, s.Path)
		}
	}
}

// Ports: the first one after OpenCode's default (49374; 28894 on Windows) that no other
// folder was given and nothing listens on.
func TestPickPort(t *testing.T) {
	busy := func(ps ...int) func(int) bool { return func(p int) bool { return !slices.Contains(ps, p) } }
	for _, c := range []struct {
		def  int
		o    agent.AccountPrep
		want int
	}{
		{defaultPort("darwin"), agent.AccountPrep{}, 49375},
		{defaultPort("linux"), agent.AccountPrep{Taken: []int{49375, 49376}}, 49377},
		{defaultPort("darwin"), agent.AccountPrep{Taken: []int{49375}, Free: busy(49376)}, 49377},
		{defaultPort("windows"), agent.AccountPrep{}, 28895},
		{defaultPort("darwin"), agent.AccountPrep{Free: func(int) bool { return false }}, 0},
		{65535, agent.AccountPrep{}, 0},
	} {
		if got := pickPort(c.def, c.o); got != c.want {
			t.Errorf("pickPort(%d, %+v) = %d, want %d", c.def, c.o.Taken, got, c.want)
		}
	}
}

// TestPrepareAccount: the port is set with the folder's variables (its state and config
// folders), never --standalone (`service` takes none and starts nothing), and read back.
func TestPrepareAccount(t *testing.T) {
	fh, f := newMachine(t, setup{noDB: true})
	for _, e := range New().Spec().Accounts.EnvFor(acct, agent.PosixPath{}) {
		k, v, _ := strings.Cut(e, "=")
		fh.SetEnv(k, v)
	}
	m := New()
	in, err := m.Detect(ctx, fh)
	if err != nil {
		t.Fatal(err)
	}
	in.Profile = added()
	h := agent.Confine(fh, m.Spec(), in)
	port := ""
	envOf := func(env []string, k string) string {
		v := ""
		for _, e := range env {
			if n, val, _ := strings.Cut(e, "="); n == k {
				v = val // the last one counts
			}
		}
		return v
	}
	f.onCall = func(argv []string) (agent.Result, bool) {
		if len(argv) < 3 || argv[0] != "service" || argv[2] != "port" {
			return agent.Result{}, false
		}
		env := f.envs[len(f.envs)-1]
		if envOf(env, "XDG_STATE_HOME") != acct+"/state" || envOf(env, "OPENCODE_CONFIG_DIR") != acct+"/config" {
			return agent.Result{Code: 1, Stderr: []byte("not the folder's variables")}, true
		}
		switch argv[1] {
		case "set":
			port = argv[3]
			return agent.Result{}, true
		case "get":
			return agent.Result{Stdout: []byte(port + "\n")}, true
		}
		return agent.Result{}, false
	}
	got, err := m.PrepareAccount(ctx, h, in, agent.AccountPrep{Taken: []int{49375}})
	if err != nil || got.Port != 49376 {
		t.Fatalf("prepared %+v: %v", got, err)
	}
	if c := f.commands(); !slices.Equal(c, []string{"service set port 49376", "service get port"}) {
		t.Errorf("ran %q", c)
	}

	// OpenCode keeping another port is an error.
	f.onCall2(func(argv []string) (agent.Result, bool) {
		if argv[0] == "service" && argv[1] == "get" {
			return agent.Result{Stdout: []byte("49374\n")}, true
		}
		return agent.Result{}, argv[0] == "service"
	})
	if _, err := m.PrepareAccount(ctx, h, in, agent.AccountPrep{}); err == nil || !strings.Contains(err.Error(), "49374") {
		t.Errorf("a port not kept: %v", err)
	}

	// Never the default folder, a folder whose state folder is elsewhere, or one with a
	// service registered already (setting a port would stop it).
	calls := len(f.commands())
	def := in
	def.Profile = &agent.RuntimeProfile{ID: "d", Default: true, Root: dataDir}
	if _, err := m.PrepareAccount(ctx, h, def, agent.AccountPrep{}); err == nil {
		t.Error("prepared the default folder")
	}
	// The default folder is refused as such, even with its folders and variables inside.
	def.Profile = &agent.RuntimeProfile{ID: "d", Default: true, Root: acct}
	if _, err := m.PrepareAccount(ctx, h, def, agent.AccountPrep{}); err == nil || !strings.Contains(err.Error(), "account folder it made") {
		t.Errorf("prepared a default folder: %v", err)
	}
	// Commands without the folder's state and config variables would reach the default
	// service (stopping it and moving it to the new port).
	novars := in
	novars.Accounts = nil
	if _, err := m.PrepareAccount(ctx, h, novars, agent.AccountPrep{}); err == nil || !strings.Contains(err.Error(), "does not set") {
		t.Errorf("prepared with no folder variables: %v", err)
	}
	nostate := in
	nostate.Accounts = &agent.ProfileSpec{RootVars: []agent.ProfileVar{{Env: "XDG_DATA_HOME", Sub: "data"}, {Env: "OPENCODE_CONFIG_DIR", Sub: "config"}}}
	if _, err := m.PrepareAccount(ctx, h, nostate, agent.AccountPrep{}); err == nil || !strings.Contains(err.Error(), "XDG_STATE_HOME") {
		t.Errorf("prepared with the default state folder's variable: %v", err)
	}
	other := in
	other.Roots = map[string]string{rootData: acct + "/data/opencode", rootConfig: acct + "/config", rootState: home + "/.local/state/opencode"}
	if _, err := m.PrepareAccount(ctx, h, other, agent.AccountPrep{}); err == nil {
		t.Error("prepared a folder whose state folder is the default one")
	}
	fh.Put(acct+"/state/opencode/service.json", []byte(`{}`), timeZero)
	if _, err := m.PrepareAccount(ctx, h, in, agent.AccountPrep{}); err == nil || !strings.Contains(err.Error(), "registered") {
		t.Errorf("prepared a folder with a service: %v", err)
	}
	if len(f.commands()) != calls {
		t.Errorf("refusals ran %q", f.commands()[calls:])
	}
}

// onCall2 replaces the program's answers.
func (f *fakeOpenCode) onCall2(fn func([]string) (agent.Result, bool)) { f.onCall = fn }

// The sign-ins: the sandbox's 2.3.0 list (none) and a hand-made one in the shape of
// OpenCode's Integration and Connection.Info (packages/schema/src/connection.ts): a
// stored key, an environment variable, a provider asking to sign in again, and one with
// both. Only provider names and ids are kept: never a label, a credential id or a
// variable's name.
func TestAccountFixtures(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(fixture, "auth-list.json"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := accountOf(b)
	if err != nil || a.LoggedIn || a.Key != "" || !strings.HasPrefix(a.Label, "no provider signed in") || !strings.Contains(a.Label, "opencode.json") {
		t.Fatalf("2.3.0: %+v %v", a, err)
	}
	b, err = os.ReadFile("testdata/synthetic/auth-list.json")
	if err != nil {
		t.Fatal(err)
	}
	a, err = accountOf(b)
	if err != nil || !a.LoggedIn || a.Key != "" || a.Person != "" || a.Email != "" || a.Confidence != "limited" {
		t.Fatalf("hand-made: %+v %v", a, err)
	}
	if a.Label != "Anthropic, Google, OpenAI; sign in again to GitHub Copilot" || a.Observation != "anthropic,google,openai" {
		t.Errorf("label %q observation %q", a.Label, a.Observation)
	}
	for _, never := range []string{"alice", "cred_", "OPENAI_API_KEY", "GEMINI", "work key", "github.com/login"} {
		if strings.Contains(a.Label+a.Observation+a.Why, never) {
			t.Errorf("the account carries %q", never)
		}
	}
	a, _ = accountOf([]byte(`[{"id":"github-copilot","name":"GitHub Copilot","connections":[{"type":"credential","status":{"status":"needs_auth","message":"x"}}]}]`))
	if a.LoggedIn || !strings.HasSuffix(a.Label, "sign in again to GitHub Copilot") {
		t.Errorf("only a provider to sign in to again: %+v", a)
	}
}

// A new folder copies OpenCode's display settings from the default cli.json, never a
// plugin's options; opencode.json (which may hold provider keys) is not inherited.
func TestInheritCLISettings(t *testing.T) {
	inh := New().Spec().Accounts.Inherit
	if len(inh) != 1 || inh["{config}/cli.json"] == nil {
		t.Fatalf("inherit %v", inh)
	}
	src := []byte(`{
  // the person's
  "$schema": "https://opencode.ai/v2/cli.json",
  "theme": {"name": "tokyonight", "mode": "dark"},
  "mouse": false,
  "scroll": {"speed": 2.5},
  "keybinds": {"leader": "ctrl+x"},
  "plugins": [{"package": "x", "options": {"apiKey": "sk-never-copied"}}],
}`)
	out, err := agent.InheritJSON(src, inh["{config}/cli.json"])
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{`"name": "tokyonight"`, `"mode": "dark"`, `"mouse": false`, `"speed": 2.5`} {
		if !strings.Contains(s, want) {
			t.Errorf("lacks %s:\n%s", want, s)
		}
	}
	for _, never := range []string{"sk-never", "plugins", "keybinds", "$schema"} {
		if strings.Contains(s, never) {
			t.Errorf("copied %s:\n%s", never, s)
		}
	}
}
