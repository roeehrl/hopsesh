// Package registry holds the agent modules hopsesh was built with and checks that each
// one's Spec is complete.
package registry

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

var idSyntax = regexp.MustCompile(`^[a-z][a-z0-9]{1,15}$`)

// Registry is the set of agent modules.
type Registry struct {
	mods   []agent.Module
	byID   map[agent.ID]agent.Module
	names  map[string]agent.ID
	clouds map[string]agent.ID // cloud name → the module that reaches it
}

// New checks and registers modules (in display order).
func New(mods ...agent.Module) (*Registry, error) {
	r := &Registry{byID: map[agent.ID]agent.Module{}, names: map[string]agent.ID{}, clouds: map[string]agent.ID{}}
	for _, m := range mods {
		s := m.Spec()
		switch {
		case !idSyntax.MatchString(string(s.ID)):
			return nil, fmt.Errorf("agent module id %q is not valid", s.ID)
		case r.byID[s.ID] != nil:
			return nil, fmt.Errorf("two agent modules use the id %q", s.ID)
		case s.Name == "" || len(s.Binaries) == 0 || len(s.Roots) == 0 && len(s.Clouds) == 0:
			return nil, fmt.Errorf("agent module %s needs a name, binaries, and roots or clouds", s.ID)
		}
		if err := agent.CheckClouds(m); err != nil {
			return nil, fmt.Errorf("agent module %s: %w", s.ID, err)
		}
		for _, c := range s.Clouds {
			if other, ok := r.clouds[c.Name]; ok {
				return nil, fmt.Errorf("agent modules %s and %s both declare the cloud %s", other, s.ID, c.Name)
			}
			r.clouds[c.Name] = s.ID
		}
		if err := checkSVG(s.Icon.SVG); err != nil {
			return nil, fmt.Errorf("agent module %s: its icon %w", s.ID, err)
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

// Cloud returns the module that reaches a cloud, and the cloud.
func (r *Registry) Cloud(name string) (agent.Module, agent.Cloud, bool) {
	id, ok := r.clouds[name]
	if !ok {
		return nil, agent.Cloud{}, false
	}
	m := r.byID[id]
	c, _ := m.Spec().FindCloud(name)
	return m, c, true
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

// checkSVG accepts an empty mark or a well-formed <svg> document with no scripts, event
// handlers or references outside itself (the window shows it as an image).
func checkSVG(svg string) error {
	if svg == "" {
		return nil
	}
	d := xml.NewDecoder(strings.NewReader(svg))
	root := true
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("is not well-formed SVG: %w", err)
		}
		el, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if root && el.Name.Local != "svg" {
			return errors.New("must be an <svg> document")
		}
		root = false
		if strings.EqualFold(el.Name.Local, "script") || strings.EqualFold(el.Name.Local, "foreignObject") {
			return fmt.Errorf("may not contain <%s>", el.Name.Local)
		}
		for _, a := range el.Attr {
			n, v := strings.ToLower(a.Name.Local), strings.TrimSpace(a.Value)
			if strings.HasPrefix(n, "on") || n == "href" && !strings.HasPrefix(v, "#") {
				return fmt.Errorf("may not use %s=%q", a.Name.Local, a.Value)
			}
		}
	}
}
