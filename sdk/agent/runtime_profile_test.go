package agent

import (
	"slices"
	"strings"
	"testing"
)

func TestRuntimeProfilesSeparateSessionKeysAndCommands(t *testing.T) {
	a := SessionKey{Agent: "codex", Profile: "one", Session: "same"}
	b := a
	b.Profile = "two"
	if a == b || a.String() == b.String() {
		t.Fatal("profile keys collide")
	}
	parsed, err := ParseKey(a.String())
	if err != nil || parsed != a {
		t.Fatalf("round trip: %+v %v", parsed, err)
	}
	for _, id := range []ID{"claude", "codex"} {
		in := Install{Accounts: &ProfileSpec{RootEnv: []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR"}, Unset: []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY"}}, Agent: id, Binary: "/bin/agent", Profile: &RuntimeProfile{ID: "one", Root: "/home/alice/isolated"}}
		c := in.ScopeCommand(Command{Argv: []string{string(id), "resume", "same"}, Env: []string{"CODEX_HOME=/wrong", "CLAUDE_CONFIG_DIR=/wrong", "OPENAI_API_KEY=wrong", "ANTHROPIC_API_KEY=wrong", "TERM=xterm"}})
		if !slices.Contains(c.Env, "TERM=xterm") || c.Argv[0] != "/bin/agent" {
			t.Fatalf("launch %+v", c)
		}
		for _, e := range in.ProfileEnv() {
			if !slices.Contains(c.Env, e) {
				t.Errorf("missing pinned %s", e)
			}
		}
		root := "CODEX_HOME"
		secret := "OPENAI_API_KEY"
		if id == "claude" {
			root = "CLAUDE_CONFIG_DIR"
			secret = "ANTHROPIC_API_KEY"
		}
		if !slices.Contains(c.Unset, root) || !slices.Contains(c.Unset, secret) || slices.Contains(c.Env, root+"=/wrong") || slices.Contains(c.Env, secret+"=wrong") {
			t.Fatalf("unscoped command %+v", c)
		}
	}
}

func TestWindowsProfileEnvironmentCannotUseCaseAlias(t *testing.T) {
	in := Install{OS: "windows", Profile: &RuntimeProfile{Root: `C:\Users\alice\research`}, Accounts: &ProfileSpec{RootEnv: []string{"CODEX_HOME"}, Unset: []string{"OPENAI_API_KEY"}}}
	c := in.ScopeCommand(Command{Env: []string{"codex_home=wrong", "OpenAi_Api_Key=wrong", "TERM=xterm"}})
	if len(c.Env) != 2 || c.Env[0] != "TERM=xterm" || c.Env[1] != "CODEX_HOME="+in.Profile.Root {
		t.Fatalf("case alias escaped isolation: %+v", c.Env)
	}
}

func TestNativeDefaultProfileScopesEveryCommand(t *testing.T) {
	const root = "/remote/alice/.claude"
	for _, tc := range []struct {
		name     string
		root     string
		metadata string
		def      bool
		native   bool
	}{
		{"verified default", root, root, true, true},
		{"unverified default", root, "", true, false},
		{"custom marked default", "/remote/alice/work", root, true, false},
		{"custom named .claude", "/remote/other/.claude", root, true, false},
		{"native root as custom", root, root, false, false},
		{"root alias", "/remote/alice/config/../.claude", root, true, false},
		{"trailing slash alias", root + "/", root, true, false},
		{"empty metadata and root", "", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := Install{Agent: "claude", OS: "linux", Binary: "/remote/bin/claude", NativeDefaultRoot: tc.metadata,
				Profile:  &RuntimeProfile{Root: tc.root, Default: tc.def},
				Accounts: &ProfileSpec{RootEnv: []string{"CLAUDE_CONFIG_DIR"}, Unset: []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"}}}
			wantEnv := []string{"CLAUDE_CONFIG_DIR=" + tc.root}
			if tc.native {
				wantEnv = nil
			}
			if got := in.ProfileEnv(); !slices.Equal(got, wantEnv) {
				t.Fatalf("profile env %v, want %v", got, wantEnv)
			}
			// Account status, login, Resume and module subprocesses all use this
			// launch policy, regardless of the command's arguments.
			for _, args := range [][]string{{"auth", "status", "--json"}, {"auth", "login"}, {"--resume", "session"}, {"--help"}, {"--teleport", "session"}} {
				c := in.ScopeCommand(Command{Argv: append([]string{"claude"}, args...), Dir: "/work", TTY: true,
					Env: []string{"CLAUDE_CONFIG_DIR=/sibling", "ANTHROPIC_API_KEY=wrong", "CLAUDE_CODE_OAUTH_TOKEN=wrong", "TERM=xterm"}, Unset: []string{"SESSION_MARKER"}})
				if !slices.Equal(c.Env, append([]string{"TERM=xterm"}, wantEnv...)) || c.Argv[0] != in.Binary || c.Dir != "/work" || !c.TTY {
					t.Fatalf("command %v: %+v", args, c)
				}
				for _, k := range []string{"SESSION_MARKER", "CLAUDE_CONFIG_DIR", "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"} {
					if !slices.Contains(c.Unset, k) {
						t.Fatalf("command %v retained %s: %+v", args, k, c)
					}
				}
			}
		})
	}
}

func TestWindowsNativeDefaultDropsCaseAliasedOverrides(t *testing.T) {
	root := `C:\Users\alice\.claude`
	in := Install{OS: "windows", NativeDefaultRoot: root, Profile: &RuntimeProfile{Root: root, Default: true},
		Accounts: &ProfileSpec{RootEnv: []string{"CLAUDE_CONFIG_DIR"}, Unset: []string{"ANTHROPIC_API_KEY"}}}
	c := in.ScopeCommand(Command{Env: []string{"claude_config_dir=wrong", "Anthropic_Api_Key=wrong", "TERM=xterm"}})
	if !slices.Equal(c.Env, []string{"TERM=xterm"}) || !slices.Contains(c.Unset, "CLAUDE_CONFIG_DIR") || !slices.Contains(c.Unset, "ANTHROPIC_API_KEY") {
		t.Fatalf("case alias escaped native default: %+v", c)
	}
	in.Profile.Root = `c:\Users\alice\.claude`
	if len(in.ProfileEnv()) != 1 {
		t.Fatal("case-aliased root bypassed exact metadata match")
	}
}

// An agent with several roots pins each to a folder inside the account folder, and a
// command for it cannot reach another folder through any of them.
func TestMultiVariableProfile(t *testing.T) {
	spec := &ProfileSpec{RootVars: []ProfileVar{{Env: "XDG_DATA_HOME", Sub: "data"}, {Env: "XDG_STATE_HOME", Sub: "state"}, {Env: "OPENCODE_CONFIG_DIR", Sub: "config"}},
		Unset: []string{"OPENCODE_PASSWORD"}}
	if got := spec.Vars(); !slices.Equal(got, []string{"XDG_DATA_HOME", "XDG_STATE_HOME", "OPENCODE_CONFIG_DIR"}) {
		t.Fatalf("Vars = %v", got)
	}
	in := Install{Agent: "opencode", Binary: "/bin/opencode", Accounts: spec, Profile: &RuntimeProfile{ID: "work", Root: "/home/u/accounts/opencode/work"}}
	want := []string{"XDG_DATA_HOME=/home/u/accounts/opencode/work/data", "XDG_STATE_HOME=/home/u/accounts/opencode/work/state", "OPENCODE_CONFIG_DIR=/home/u/accounts/opencode/work/config"}
	if got := in.ProfileEnv(); !slices.Equal(got, want) {
		t.Fatalf("ProfileEnv = %v", got)
	}
	c := in.ScopeCommand(Command{Argv: []string{"opencode", "-s", "ses_1"}, Env: []string{"XDG_DATA_HOME=/elsewhere", "OPENCODE_CONFIG_DIR=/elsewhere", "OPENCODE_PASSWORD=x", "TERM=xterm"}})
	if !slices.Equal(c.Env, append([]string{"TERM=xterm"}, want...)) || c.Argv[0] != "/bin/opencode" {
		t.Fatalf("command %+v", c)
	}
	for _, v := range []string{"XDG_DATA_HOME", "XDG_STATE_HOME", "OPENCODE_CONFIG_DIR", "OPENCODE_PASSWORD"} {
		if !slices.Contains(c.Unset, v) {
			t.Errorf("%s is not unset first", v)
		}
	}
	// A Windows account folder joins with its own separators, even before Detect sets OS.
	win := Install{Accounts: spec, Profile: &RuntimeProfile{Root: `C:\Users\u\accounts\work`}}
	if got := win.ProfileEnv()[0]; got != `XDG_DATA_HOME=C:\Users\u\accounts\work\data` {
		t.Fatalf("Windows ProfileEnv = %q", got)
	}
	// RootEnv and RootVars together: the folder itself and folders inside it.
	both := &ProfileSpec{RootEnv: []string{"X_HOME"}, RootVars: []ProfileVar{{Env: "X_HOME"}, {Env: "X_CACHE", Sub: "cache"}}}
	if got := both.Vars(); !slices.Equal(got, []string{"X_HOME", "X_CACHE"}) {
		t.Fatalf("Vars = %v", got)
	}
	if got := both.EnvFor("/p", PosixPath{}); !slices.Equal(got, []string{"X_HOME=/p", "X_HOME=/p", "X_CACHE=/p/cache"}) {
		t.Fatalf("EnvFor = %v", got)
	}
	var none *ProfileSpec
	if none.Vars() != nil || none.EnvFor("/p", PosixPath{}) != nil {
		t.Fatal("no accounts, no variables")
	}
}

// The default account of an agent with several roots is its own folders: its Root is the
// main root, so the variables that would point inside an account folder are not set, and
// the machine's own ones stay. RootEnv, naming the main root itself, is still pinned.
func TestDefaultProfileOfSeveralRoots(t *testing.T) {
	spec := &ProfileSpec{RootVars: []ProfileVar{{Env: "XDG_DATA_HOME", Sub: "data"}, {Env: "OPENCODE_CONFIG_DIR", Sub: "config"}}, Unset: []string{"OPENCODE_PASSWORD"}}
	in := Install{Agent: "opencode", Accounts: spec, Profile: &RuntimeProfile{ID: "default", Default: true, Root: "/home/u/.local/share/opencode"}}
	if got := in.ProfileEnv(); len(got) != 0 {
		t.Fatalf("the default account pins %v", got)
	}
	c := in.ScopeCommand(Command{Argv: []string{"opencode"}, Env: []string{"XDG_DATA_HOME=/elsewhere", "OPENCODE_PASSWORD=x", "TERM=xterm"}})
	if !slices.Equal(c.Env, []string{"TERM=xterm"}) {
		t.Fatalf("a caller's variables reached the default account: %v", c.Env)
	}
	if slices.Contains(c.Unset, "XDG_DATA_HOME") || !slices.Contains(c.Unset, "OPENCODE_PASSWORD") {
		t.Fatalf("unset %v: the machine's XDG_DATA_HOME found the default folders and stays", c.Unset)
	}
	// An added account is pinned as before.
	work := Install{Agent: "opencode", Accounts: spec, Profile: &RuntimeProfile{ID: "work", Root: "/home/u/accounts/opencode/work"}}
	if got := work.ProfileEnv(); len(got) != 2 || got[0] != "XDG_DATA_HOME=/home/u/accounts/opencode/work/data" {
		t.Fatalf("an added account: %v", got)
	}
	// The stand-in third agent: RootEnv names the main root, RootVars a folder inside an
	// account folder. Its default account keeps the first and not the second.
	third := &ProfileSpec{RootEnv: []string{"THIRD_DATA_DIR"}, RootVars: []ProfileVar{{Env: "THIRD_CONFIG_DIR", Sub: "config"}}}
	def := Install{Agent: "third", Accounts: third, Profile: &RuntimeProfile{Default: true, Root: "/home/u/.local/share/third"}}
	if got := def.ProfileEnv(); !slices.Equal(got, []string{"THIRD_DATA_DIR=/home/u/.local/share/third"}) {
		t.Fatalf("third default: %v", got)
	}
	if c := def.ScopeCommand(Command{Argv: []string{"third"}}); !slices.Contains(c.Unset, "THIRD_DATA_DIR") || slices.Contains(c.Unset, "THIRD_CONFIG_DIR") {
		t.Fatalf("third default unset %v", c.Unset)
	}
}

// Where a root of an account folder is, through the same resolver as the default folder.
func TestProfileLocate(t *testing.T) {
	s := Spec{Roots: []Root{
		{Name: "data", EnvSub: []EnvSub{{Env: "XDG_DATA_HOME", Sub: "opencode"}}, Default: map[string]string{"*": "~/.local/share/opencode"}},
		{Name: "config", Env: []string{"OPENCODE_CONFIG_DIR"}, EnvSub: []EnvSub{{Env: "XDG_CONFIG_HOME", Sub: "opencode"}}, Default: map[string]string{"*": "~/.config/opencode"}},
		{Name: "cache", EnvSub: []EnvSub{{Env: "XDG_CACHE_HOME", Sub: "opencode"}}, Default: map[string]string{"*": "~/.cache/opencode"}},
	}, Accounts: &ProfileSpec{RootVars: []ProfileVar{{Env: "XDG_DATA_HOME", Sub: "data"}, {Env: "OPENCODE_CONFIG_DIR", Sub: "config"}}}}
	env := map[string]string{"XDG_CACHE_HOME": "/xdg/cache", "OPENCODE_CONFIG_DIR": "/user/config"}
	getenv := func(k string) string { return env[k] }
	for name, want := range map[string]string{"data": "/p/data/opencode", "config": "/p/config", "cache": "/xdg/cache/opencode", "nope": ""} {
		if got := s.ProfileLocate(name, "/p", "linux", getenv); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestInheritJSON(t *testing.T) {
	src := []byte(`{
  "model": "omlx/qwen",
  "theme": "dark",
  "keybinds": {"leader": "ctrl+x"},
  "provider": {"omlx": {"name": "LAN", "options": {"baseURL": "http://10.0.0.2:8080", "apiKey": "sk-secret", "headers": {"Authorization": "Bearer x"}}, "models": {"qwen": {"limit": {"context": 32768}}}}},
  "mcp": {"tool": {"command": ["x"], "environment": {"TOKEN": "y"}, "env": {"A": "b"}}},
  "big": 12345678901234567890
}`)
	b, err := InheritJSON(src, []string{"model", "keybinds", "provider", "mcp.tool", "big", "absent.field"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, keep := range []string{`"model": "omlx/qwen"`, `"leader": "ctrl+x"`, `"baseURL": "http://10.0.0.2:8080"`, `"context": 32768`, `"command"`, `12345678901234567890`} {
		if !strings.Contains(got, keep) {
			t.Errorf("lost %s: %s", keep, got)
		}
	}
	for _, drop := range []string{"sk-secret", `"TOKEN"`, "Bearer", "apiKey", "headers", `"env"`, "theme", "absent"} {
		if strings.Contains(got, drop) {
			t.Errorf("copied %s: %s", drop, got)
		}
	}
	for _, field := range []string{"provider.omlx.options.apiKey", "auth", "mcp.tool.env", "provider..x", "OPENAI_API_KEY"} {
		if _, err := InheritJSON(src, []string{field}); err == nil {
			t.Errorf("%s is a credential and must be refused", field)
		}
	}
	if b, err := InheritJSON(src, []string{"absent"}); b != nil || err != nil {
		t.Fatalf("nothing to copy: %s, %v", b, err)
	}
	if _, err := InheritJSON([]byte(`[1]`), []string{"model"}); err == nil {
		t.Fatal("not an object")
	}
	for name, want := range map[string]bool{"apiKey": true, "APIKey": true, "api_key": true, "accessToken": true, "Authorization": true, "keybinds": false, "author": false, "environment": false, "model": false, "openAIKey": true, "githubToken": true, "hotkey": true} {
		if credentialKey(name) != want {
			t.Errorf("credentialKey(%q) = %v", name, !want)
		}
	}
}
