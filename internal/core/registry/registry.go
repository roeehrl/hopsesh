// Package registry holds the agent modules hopsesh was built with and checks that each
// one's Spec is complete.
package registry

import (
	"fmt"
	"regexp"
	"sort"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

var idSyntax = regexp.MustCompile(`^[a-z][a-z0-9]{1,15}$`)

// Registry is the set of agent modules.
type Registry struct {
	mods  []agent.Module
	byID  map[agent.ID]agent.Module
	names map[string]agent.ID
}

// New checks and registers modules (in display order).
func New(mods ...agent.Module) (*Registry, error) {
	r := &Registry{byID: map[agent.ID]agent.Module{}, names: map[string]agent.ID{}}
	for _, m := range mods {
		s := m.Spec()
		switch {
		case !idSyntax.MatchString(string(s.ID)):
			return nil, fmt.Errorf("agent module id %q is not valid", s.ID)
		case r.byID[s.ID] != nil:
			return nil, fmt.Errorf("two agent modules use the id %q", s.ID)
		case s.Name == "" || len(s.Roots) == 0 || len(s.Binaries) == 0:
			return nil, fmt.Errorf("agent module %s needs a name, roots and binaries", s.ID)
		}
		for _, c := range s.Experimental {
			if !agent.Has(m, c) {
				return nil, fmt.Errorf("agent module %s marks %s experimental but does not implement it", s.ID, c)
			}
		}
		r.mods = append(r.mods, m)
		r.byID[s.ID] = m
		r.names[s.Name] = s.ID
	}
	return r, nil
}

// All returns every module.
func (r *Registry) All() []agent.Module { return r.mods }

// Get returns a module by id.
func (r *Registry) Get(id agent.ID) (agent.Module, bool) {
	m, ok := r.byID[id]
	return m, ok
}

// ByName returns the id of a module by its display name ("Claude Code").
func (r *Registry) ByName(name string) (agent.ID, bool) {
	id, ok := r.names[name]
	return id, ok
}

// Specs returns every module's Spec.
func (r *Registry) Specs() []agent.Spec {
	out := make([]agent.Spec, len(r.mods))
	for i, m := range r.mods {
		out[i] = m.Spec()
	}
	return out
}

// IDs returns the module ids, sorted.
func (r *Registry) IDs() []agent.ID {
	out := make([]agent.ID, 0, len(r.mods))
	for _, m := range r.mods {
		out = append(out, m.Spec().ID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Worktrees lists every module's agent-managed worktree folders.
func (r *Registry) Worktrees() []string {
	var out []string
	for _, m := range r.mods {
		out = append(out, m.Spec().Worktrees...)
	}
	return out
}

// LoginEnv lists every variable the modules need from a login shell.
func (r *Registry) LoginEnv() []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range r.mods {
		for _, e := range host.SpecEnv(m.Spec()) {
			if !seen[e] {
				seen[e] = true
				out = append(out, e)
			}
		}
	}
	return out
}

// As returns a module's optional capability.
func As[T any](m agent.Module) (T, bool) {
	t, ok := m.(T)
	return t, ok
}
