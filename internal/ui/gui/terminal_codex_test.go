package gui

import (
	"reflect"
	"testing"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestOwnedCodexCommandPreservesScopeAndExternalLaunch(t *testing.T) {
	t.Setenv("CODEX_APP_SERVER_ADDRESS", "unix:///unrelated-server")
	key := agent.SessionKey{Agent: "codex", Profile: "work", Session: "original"}
	profile := &agent.RuntimeProfile{ID: "work", Root: "/profiles/work"}
	in := agent.Install{Agent: "codex", Present: true, Version: "0.160.1", Binary: "/bin/codex", Profile: profile, Accounts: &agent.ProfileSpec{
		RootEnv: []string{"CODEX_HOME", "CODEX_SQLITE_HOME"}, Unset: []string{"CODEX_APP_SERVER_ADDRESS", "OPENAI_API_KEY"},
	}}
	base := in.ScopeCommand(agent.Command{Argv: []string{"codex", "resume", "original"}, Dir: "/project", Env: []string{"KEEP=value"}})
	meta := TabMeta{Kind: TabSession, Machine: app.LocalName(), Key: key.String()}
	for _, tc := range []struct {
		name, version, verb string
		modify              func(*TabMeta, *agent.Command)
		install             func(*agent.Install)
		want                bool
	}{
		{name: "installed version", version: "0.160.1", verb: "resume", want: true},
		{name: "native fork", version: "0.160.1", verb: "fork", want: true},
		{name: "first supported version", version: "0.156.0", verb: "resume", want: true},
		{name: "older CLI", version: "0.155.0", verb: "resume"},
		{name: "unknown CLI", verb: "resume"},
		{name: "prerelease CLI", version: "0.160.1-beta", verb: "resume"},
		{name: "wrong profile", version: "0.160.1", verb: "resume", modify: func(m *TabMeta, _ *agent.Command) { k := key; k.Profile = "personal"; m.Key = k.String() }},
		{name: "remote", version: "0.160.1", verb: "resume", modify: func(m *TabMeta, _ *agent.Command) { m.Machine = "remote" }},
		{name: "shell", version: "0.160.1", verb: "resume", modify: func(m *TabMeta, _ *agent.Command) { m.Kind = TabShell }},
		{name: "different binary", version: "0.160.1", verb: "resume", modify: func(_ *TabMeta, c *agent.Command) { c.Argv[0] = "/bin/other" }},
		{name: "unscoped PATH binary", version: "0.160.1", verb: "resume", modify: func(_ *TabMeta, c *agent.Command) { c.Argv[0] = "codex" }},
		{name: "sign in", version: "0.160.1", verb: "login"},
		{name: "explicit remote", version: "0.160.1", verb: "resume", modify: func(_ *TabMeta, c *agent.Command) { c.Argv = append(c.Argv, "--remote=unix:///remote") }},
		{name: "explicit endpoint environment", version: "0.160.1", verb: "resume", modify: func(_ *TabMeta, c *agent.Command) {
			c.Env = append(append([]string{}, c.Env...), "CODEX_APP_SERVER_ADDRESS=unix:///remote")
		}},
		{name: "inherited endpoint retained", version: "0.160.1", verb: "resume", modify: func(_ *TabMeta, c *agent.Command) { c.Unset = nil }},
		{name: "Windows explicit endpoint", version: "0.160.1", verb: "resume", install: func(in *agent.Install) { in.OS = "windows" }, modify: func(_ *TabMeta, c *agent.Command) {
			c.Env = append(append([]string{}, c.Env...), "codex_app_server_address=unix:///remote")
		}},
		{name: "known scoped wrapper", version: "0.160.1", verb: "resume", want: true, install: func(in *agent.Install) { in.Binary = "/bin/codex.cmd" }, modify: func(_ *TabMeta, c *agent.Command) { c.Argv[0] = "/bin/codex.cmd" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i := in
			i.Version = tc.version
			if tc.install != nil {
				tc.install(&i)
			}
			a := &App{inv: &app.Inventory{Machines: []*app.Machine{{Name: app.LocalName(), Local: true, Agents: []app.AgentState{{Agent: "codex", Install: i}}}}}}
			c, m := base, meta
			c.Argv = append([]string{}, base.Argv...)
			c.Argv[1] = tc.verb
			if tc.modify != nil {
				tc.modify(&m, &c)
			}
			original := append([]string{}, c.Argv...)
			got := a.ownedSessionCommand(c, m)
			want := original
			if tc.want {
				want = append([]string{original[0], original[1], "--no-daemon"}, original[2:]...)
			}
			if !reflect.DeepEqual(got.Argv, want) || !reflect.DeepEqual(c.Argv, original) || got.Dir != c.Dir || !reflect.DeepEqual(got.Env, c.Env) || !reflect.DeepEqual(got.Unset, c.Unset) {
				t.Fatalf("owned launch changed scope or external command: got %+v original %+v", got, c)
			}
			if twice := a.ownedSessionCommand(got, m); !reflect.DeepEqual(twice, got) {
				t.Fatal("duplicate override")
			}
		})
	}
}
