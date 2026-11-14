package tui

import (
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/agents/opencode"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/internal/testkit/thirdagent"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Keys 1 to 9 add an account folder for the enabled agents with accounts, in display
// order: with a third agent, 3 adds one for it; with Codex off, 2 does.
func TestAccountKeysThirdAgent(t *testing.T) {
	reg, err := registry.New(claude.New(), codex.New(), thirdagent.New())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		cfg  config.Config
		key  string
		want string
	}{
		{config.Config{}, "3", string(thirdagent.ID)},
		{config.Config{Agents: map[string]config.Agent{"codex": {Disabled: true}}}, "2", string(thirdagent.ID)},
		{config.Config{}, "1", "claude"},
	} {
		a := app.New(c.cfg, reg, t.TempDir(), nil)
		m := &model{deps: Deps{App: a}}
		m.accountKeys(c.key)
		_ = a.Catalog.Close()
		if string(m.accts.agent) != c.want || m.accts.editing != "new-name" {
			t.Errorf("key %s: agent %q editing %q, want %s", c.key, m.accts.agent, m.accts.editing, c.want)
		}
	}
}

// OpenCode has account folders too: with every agent registered it is added with 3, and
// a folder's own service port is shown with its sign-in.
func TestAccountKeysOpenCode(t *testing.T) {
	reg, err := registry.New(claude.New(), codex.New(), opencode.New())
	if err != nil {
		t.Fatal(err)
	}
	a := app.New(config.Config{}, reg, t.TempDir(), nil)
	defer a.Catalog.Close()
	m := &model{deps: Deps{App: a}, width: 100, height: 40}
	m.accountKeys("3")
	if m.accts.agent != "opencode" || m.accts.editing != "new-name" {
		t.Fatalf("key 3: agent %q editing %q", m.accts.agent, m.accts.editing)
	}
	m.accts = accountView{rows: []agent.RuntimeProfile{{ID: "work", Name: "Work", Agent: "opencode", Root: "/home/u/.hopsesh/accounts/opencode/work", Port: 49375,
		Account: &agent.Account{LoggedIn: true, Label: "Anthropic, OpenAI"}}}}
	m.accts.rebuild()
	var b strings.Builder
	m.viewAccounts(&b)
	for _, want := range []string{"3 add OpenCode", "Work · opencode", "signed in · Anthropic, OpenAI · service port 49375"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("the view lacks %q:\n%s", want, b.String())
		}
	}
}
