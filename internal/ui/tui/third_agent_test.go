package tui

import (
	"testing"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/internal/testkit/thirdagent"
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
