package cli

import (
	"slices"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// localModules are the compiled-in modules that keep sessions on machines (not cloud-only),
// in display order.
func localModules() []agent.Module {
	var out []agent.Module
	if modules == nil {
		return out
	}
	for _, m := range modules.All() {
		if m.Spec().MainRoot().Name != "" {
			out = append(out, m)
		}
	}
	return out
}

// localNames names the local agents for people ("Claude Code", "Codex").
func localNames() []string {
	var out []string
	for _, m := range localModules() {
		out = append(out, m.Spec().Name)
	}
	return out
}

// localVendors names the local agents' vendors, each once.
func localVendors() []string {
	var out []string
	for _, m := range localModules() {
		if v := m.Spec().Vendor; v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// accountModules are the local agents with account folders.
func accountModules() []agent.Module {
	var out []agent.Module
	for _, m := range localModules() {
		if m.Spec().Accounts != nil {
			out = append(out, m)
		}
	}
	return out
}

// otherAgentName is an agent a session may continue in, for an example ("Codex").
func otherAgentName() string {
	if names := localNames(); len(names) > 1 {
		return names[1]
	}
	return "another agent"
}

// joinWith joins names as a sentence does: "a", "a and b", "a, b and c".
func joinWith(names []string, last string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + last + names[len(names)-1]
}

// localIDs are the local agents' ids, in display order.
func localIDs() []string {
	var out []string
	for _, m := range localModules() {
		out = append(out, string(m.Spec().ID))
	}
	return out
}

// firstLocalID is the first local agent, the default where one must be named.
func firstLocalID() string {
	if ids := localIDs(); len(ids) > 0 {
		return ids[0]
	}
	return ""
}

// firstWriterID is the first agent in display order that writes sessions: the one a
// cloud-only module's text goes into when nothing else decides.
func firstWriterID() string {
	for _, m := range localModules() {
		if agent.Has(m, agent.CapWrite) {
			return string(m.Spec().ID)
		}
	}
	return ""
}

// accountNames and accountIDs name the local agents with account folders.
func accountNames() []string {
	var out []string
	for _, m := range accountModules() {
		out = append(out, m.Spec().Name)
	}
	return out
}

func accountIDs() []string {
	var out []string
	for _, m := range accountModules() {
		out = append(out, string(m.Spec().ID))
	}
	return out
}
