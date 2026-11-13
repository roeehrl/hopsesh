// Package thirdagent is a stand-in third local agent for the tests: a module with more
// than one root, account folders, a desktop app and a writer, that is neither Claude Code
// nor Codex. Tests register it beside them to check that nothing assumes two agents.
package thirdagent

import (
	"context"
	"errors"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// ID is the stand-in's agent id.
const ID agent.ID = "third"

// Module is the stand-in agent.
type Module struct{}

// New returns the stand-in agent.
func New() *Module { return &Module{} }

var errStandIn = errors.New("the third agent is a test stand-in")

// Spec declares a data root (the main one), a config root and a state root under it.
func (*Module) Spec() agent.Spec {
	return agent.Spec{
		ID:        ID,
		Name:      "Third Agent",
		Vendor:    "Example",
		Stability: agent.Experimental,
		Tested:    []string{"1.0"},
		Binaries:  []agent.Binary{{Name: "third", VersionArgs: []string{"--version"}}},
		Roots: []agent.Root{
			{Name: "data", Env: []string{"THIRD_DATA_DIR"}, Default: map[string]string{"*": "~/.local/share/third"}},
			// Set as is by THIRD_CONFIG_DIR, else inside XDG_CONFIG_HOME.
			{Name: "config", Env: []string{"THIRD_CONFIG_DIR"}, EnvSub: []agent.EnvSub{{Env: "XDG_CONFIG_HOME", Sub: "third"}},
				Default: map[string]string{"*": "~/.config/third", "windows": "~/AppData/Roaming/third"}},
			{Name: "state", Default: map[string]string{"*": "{data}/state"}},
		},
		// Inherit carries a setting that is not the sign-in, so InheritsSignIn stays off.
		Accounts: &agent.ProfileSpec{RootEnv: []string{"THIRD_DATA_DIR"}, Login: []string{"login"},
			Inherit: map[string][]string{"config.toml": {"theme"}}},
		Desktop: &agent.Desktop{Tool: "Third desktop", Product: "Third"},
		Plans:   "Third",
	}
}

func (m *Module) Detect(_ context.Context, h agent.Host) (agent.Install, error) {
	return agent.DefaultInstall(m.Spec(), h), nil
}

func (*Module) List(context.Context, agent.Host, agent.Install) (agent.Listing, error) {
	return agent.Listing{}, nil
}

func (*Module) Bundle(context.Context, agent.Host, agent.Install, agent.Summary) (agent.Bundle, error) {
	return agent.Bundle{}, errStandIn
}

func (*Module) PlanMove(agent.Install, agent.Install, agent.Summary, agent.Bundle, agent.Placement) (agent.MovePlan, error) {
	return agent.MovePlan{}, errStandIn
}

func (*Module) Verify(context.Context, agent.Host, agent.MovePlan, map[string]string, agent.Placement) error {
	return errStandIn
}

func (*Module) Resume(in agent.Install, key agent.SessionKey, _ agent.Placement, _ agent.ResumeOptions) agent.Command {
	return agent.Command{Argv: []string{"third", "--resume", string(key.Session)}}
}

// Account makes it an agent with accounts (CapAccount).
func (*Module) Account(context.Context, agent.Host, agent.Install) (agent.Account, error) {
	return agent.Account{}, errStandIn
}

// Profile and Write make it a conversion target (CapWrite).
func (*Module) Profile(agent.Install) ir.Profile { return ir.Profile{} }

func (*Module) Write(context.Context, agent.Host, agent.Install, ir.WriteRequest) (ir.WriteResult, error) {
	return ir.WriteResult{}, errStandIn
}
