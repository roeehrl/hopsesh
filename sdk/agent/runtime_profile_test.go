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
