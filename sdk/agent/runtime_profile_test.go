package agent

import (
	"slices"
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
