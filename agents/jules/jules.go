// Package jules is the Jules module (Google's asynchronous coding agent), a cloud-only
// module: Jules works in Google's cloud on a GitHub repository, and hopsesh reaches it
// only through Jules Tools, the jules CLI (jules remote list, jules remote pull), signed
// in as the user. It keeps nothing on any machine, so the file methods come from
// agent.NoLocal.
package jules

import (
	"context"
	_ "embed"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

const id agent.ID = "jules"

// Module is the Jules module.
type Module struct{ agent.NoLocal }

// New returns the module.
func New() *Module { return &Module{} }

var _ agent.Module = (*Module)(nil)

// iconSVG is the module's own mark for the window (drawn for hopsesh, not the vendor's
// logo).
//
//go:embed icon.svg
var iconSVG string

// Spec declares Jules, reached through the jules CLI.
func (*Module) Spec() agent.Spec {
	return agent.Spec{
		ID:        id,
		Name:      "Jules",
		Vendor:    "Google",
		Stability: agent.Experimental,
		Tested:    cloud().Tested,
		Binaries: []agent.Binary{{
			Name: "jules",
			Candidates: map[string][]string{
				"*":       {"~/.npm-global/bin/jules", "/opt/homebrew/bin/jules", "/usr/local/bin/jules", "~/.local/bin/jules"},
				"windows": {"~/AppData/Roaming/npm/jules.cmd"},
			},
			VersionArgs: []string{"version"},
		}},
		Icon:   agent.Icon{SVG: iconSVG},
		Clouds: []agent.Cloud{cloud()},
	}
}

// Detect finds the jules CLI; there is no data folder.
func (m *Module) Detect(_ context.Context, h agent.Host) (agent.Install, error) {
	return agent.DefaultInstall(m.Spec(), h), nil
}
