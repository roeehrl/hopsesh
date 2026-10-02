// Package all is the one list of agent modules compiled into hopsesh. Adding an agent is a
// package under agents/ and a line here.
package all

import (
	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Modules returns every compiled-in module, in display order.
func Modules() []agent.Module { return []agent.Module{claude.New(), codex.New()} }

// Registry checks and registers the modules. A module with an incomplete Spec is a
// programming error, caught by the tests.
func Registry() *registry.Registry {
	r, err := registry.New(Modules()...)
	if err != nil {
		panic(err)
	}
	return r
}
