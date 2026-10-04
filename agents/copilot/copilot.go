// Package copilot is the GitHub Copilot cloud agent module, a cloud-only module: the
// agent works in GitHub's cloud and hands back a pull request on a copilot/… branch, and
// hopsesh reaches it only through the GitHub CLI's agent-task commands (gh agent-task
// list, gh agent-task view [--log]) and gh pr, signed in as the user. It keeps nothing on
// any machine, so the file methods come from agent.NoLocal.
package copilot

import (
	"context"
	_ "embed"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

const id agent.ID = "copilot"

// Module is the Copilot cloud agent module.
type Module struct{ agent.NoLocal }

// New returns the module.
func New() *Module { return &Module{} }

var _ agent.Module = (*Module)(nil)

// iconSVG is the module's own mark for the window (drawn for hopsesh, not the vendor's
// logo).
//
//go:embed icon.svg
var iconSVG string

// Spec declares the Copilot cloud agent, reached through gh.
func (*Module) Spec() agent.Spec {
	return agent.Spec{
		ID:        id,
		Name:      "GitHub Copilot",
		Vendor:    "GitHub",
		Stability: agent.Experimental,
		// The driver's versions: a cloud-only module has no agent of its own here.
		Tested: cloud().Tested,
		Binaries: []agent.Binary{{
			Name: "gh",
			Candidates: map[string][]string{
				"*":       {"/opt/homebrew/bin/gh", "/usr/local/bin/gh", "/usr/bin/gh", "/home/linuxbrew/.linuxbrew/bin/gh"},
				"windows": {"C:/Program Files/GitHub CLI/gh.exe", "~/AppData/Local/Programs/GitHub CLI/gh.exe", "~/scoop/shims/gh.exe"},
			},
			VersionArgs: []string{"--version"},
		}},
		Icon:   agent.Icon{SVG: iconSVG},
		Clouds: []agent.Cloud{cloud()},
	}
}

// Detect finds gh; there is no data folder.
func (m *Module) Detect(_ context.Context, h agent.Host) (agent.Install, error) {
	return agent.DefaultInstall(m.Spec(), h), nil
}
