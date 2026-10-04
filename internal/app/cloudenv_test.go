package app

import (
	"testing"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// The environments a hand-off can run in: the repository's own first, then the ones the
// listed tasks used (most used first), then the ones set for other repositories, each once.
func TestEnvChoices(t *testing.T) {
	cloudEnv(t)
	a := cloudApp(t, all.Registry(), "codex-cloud")
	a.Cfg.SetCloudEnvironment("codex-cloud", "github.com/acme/api", "env_api")
	a.Cfg.SetCloudEnvironment("codex-cloud", "github.com/acme/ops", "acme-ops")
	task := func(env, label string) Entry {
		return Entry{Location: agent.CloudLocation("codex-cloud"), Cloud: &agent.CloudSession{Env: env, EnvLabel: label}}
	}
	inv := &Inventory{Entries: []Entry{task("env_web", "acme-web"), task("env_api", "acme-api"), task("env_web", "acme-web"),
		{Location: agent.CloudLocation("claude-cloud"), Cloud: &agent.CloudSession{Env: "env_other"}}}}
	got := a.EnvChoices(inv, "codex-cloud", "github.com/acme/api")
	want := []string{
		"env_api|acme-api (set for this repository; used by 1 of your recent tasks)",
		"env_web|acme-web (used by 2 of your recent tasks)",
		"acme-ops|acme-ops (set for github.com/acme/ops)",
	}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i, c := range got {
		if c.Value+"|"+c.Label != want[i] {
			t.Errorf("%d: %s|%s, want %s", i, c.Value, c.Label, want[i])
		}
	}
	if !got[0].Configured || got[1].Tasks != 2 {
		t.Errorf("%+v", got)
	}
	if got := a.EnvChoices(nil, "codex-cloud", "github.com/acme/web"); len(got) != 2 || got[0].Configured {
		t.Errorf("no listing: %+v", got)
	}
	envs := map[string]string{"github.com/acme/api": "env_api", "github.com/acme/ops": "acme-ops", "github.com/acme/web": "env_web", "github.com/acme/web2": "env_web"}
	for s, want := range map[agent.CloudSession]string{
		{Env: "env_api"}:                       "github.com/acme/api",
		{Env: "env_x", EnvLabel: "ACME-OPS"}:   "github.com/acme/ops",
		{Env: "env_web"}:                       "", // two repositories have it
		{Env: "env_none", EnvLabel: "nothing"}: "",
	} {
		if got := repoForEnv(envs, s); got != want {
			t.Errorf("%+v: %q", s, got)
		}
	}
	rows := a.RepoEnvs(&Inventory{Entries: []Entry{{Location: agent.MachineLocation("here"), Git: gitState("gitlab.example.com/acme/billing")}}}, "codex-cloud")
	if len(rows) != 3 || rows[0].Repo != "github.com/acme/api" || rows[0].Env != "env_api" || rows[2].Unsupported != "not on GitHub" {
		t.Errorf("rows: %+v", rows)
	}
	if a.RepoEnvs(nil, "claude-cloud") != nil {
		t.Error("Claude Code cloud needs no environment")
	}
}

func gitState(identity string) *repos.GitState {
	return &repos.GitState{IsRepo: true, Identity: identity}
}
