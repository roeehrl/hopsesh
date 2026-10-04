// Package amp is the Amp module for Amp's threads and orbs, a cloud-only module: Amp
// keeps every thread on its servers (an orb is a cloud machine a thread runs on), and
// hopsesh reaches them only through the amp CLI (amp threads list, amp threads markdown),
// signed in as the user. Amp's local CLI sessions are threads too, so they are listed
// here. It keeps nothing on any machine, so the file methods come from agent.NoLocal.
package amp

import (
	"context"
	_ "embed"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

const id agent.ID = "amp"

// Module is the Amp module.
type Module struct{ agent.NoLocal }

// New returns the module.
func New() *Module { return &Module{} }

var _ agent.Module = (*Module)(nil)

// iconSVG is the module's own mark for the window (drawn for hopsesh, not the vendor's
// logo).
//
//go:embed icon.svg
var iconSVG string

// Spec declares Amp, reached through the amp CLI.
func (*Module) Spec() agent.Spec {
	return agent.Spec{
		ID:        id,
		Name:      "Amp",
		Vendor:    "Amp",
		Stability: agent.Experimental,
		Tested:    cloud().Tested,
		Binaries: []agent.Binary{{
			Name: "amp",
			Candidates: map[string][]string{
				"*":       {"~/.npm-global/bin/amp", "~/.amp/bin/amp", "~/.local/bin/amp", "/opt/homebrew/bin/amp", "/usr/local/bin/amp"},
				"windows": {"~/AppData/Roaming/npm/amp.cmd"},
			},
			VersionArgs: []string{"--version"},
		}},
		Icon:   agent.Icon{SVG: iconSVG},
		Clouds: []agent.Cloud{cloud()},
	}
}

// Detect finds the amp CLI; there is no data folder.
func (m *Module) Detect(_ context.Context, h agent.Host) (agent.Install, error) {
	return agent.DefaultInstall(m.Spec(), h), nil
}
