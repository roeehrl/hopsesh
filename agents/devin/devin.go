// Package devin is the Devin module (Cognition's coding agent), a cloud-only module for
// Devin's cloud sessions: hopsesh reaches them only through the Devin CLI's
// non-interactive commands (devin auth status, devin list --format json), signed in as
// the user. The CLI's own way home (/pickup or /handoff in `devin --cloud -r <id>`) is
// interactive, so hopsesh brings the session's pull request branch instead. It keeps
// nothing on any machine, so the file methods come from agent.NoLocal.
package devin

import (
	"context"
	_ "embed"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

const id agent.ID = "devin"

// Module is the Devin module.
type Module struct{ agent.NoLocal }

// New returns the module.
func New() *Module { return &Module{} }

var _ agent.Module = (*Module)(nil)

// iconSVG is the module's own mark for the window (drawn for hopsesh, not the vendor's
// logo).
//
//go:embed icon.svg
var iconSVG string

// Spec declares Devin, reached through the devin CLI.
func (*Module) Spec() agent.Spec {
	return agent.Spec{
		ID:        id,
		Name:      "Devin",
		Vendor:    "Cognition",
		Stability: agent.Experimental,
		Tested:    cloud().Tested,
		Binaries: []agent.Binary{{
			Name: "devin",
			Candidates: map[string][]string{
				"*":       {"~/.local/bin/devin", "~/.devin/bin/devin", "/opt/homebrew/bin/devin", "/usr/local/bin/devin"},
				"windows": {"~/.local/bin/devin.exe", "~/AppData/Local/Programs/devin/devin.exe"},
			},
			VersionArgs: []string{"version"},
		}},
		Icon:   agent.Icon{SVG: iconSVG},
		Clouds: []agent.Cloud{cloud()},
	}
}

// Detect finds the devin CLI; there is no data folder.
func (m *Module) Detect(_ context.Context, h agent.Host) (agent.Install, error) {
	return agent.DefaultInstall(m.Spec(), h), nil
}
