package opencode

import (
	"encoding/json"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

var _ agent.Integrator = (*Module)(nil)

// Integration says OpenCode reads skills from ~/.agents/skills (also ~/.claude/skills, where
// hopsesh puts Claude Code's copy). Its approval rules for hopsesh's commands are a file
// hopsesh owns beside the plugin, {config}/.hopsesh/rules.json, which the plugin applies
// (permission "evaluate"); OpenCode's own config is never edited. Without the plugin
// (hopsesh notices install) the rules do nothing.
func (m *Module) Integration(h agent.Host, in agent.Install) agent.Integration {
	ig := agent.Integration{SkillDir: h.Path().Join(h.Facts().Home, ".agents", "skills")}
	if f := rulesFile(h, in); f != "" {
		ig.Rules = &agent.RuleFile{Path: f, Owned: true, Render: renderRules}
	}
	return ig
}

// pluginRules is rules.json: the hopsesh command as OpenCode's shell runs it, and the
// command prefixes (after it) that run without asking and that always ask.
type pluginRules struct {
	Format int        `json:"format"`
	Bin    string     `json:"bin"`
	Allow  [][]string `json:"allow"`
	Ask    [][]string `json:"ask"`
}

func renderRules(r agent.Rules) []byte {
	b, _ := json.MarshalIndent(pluginRules{Format: 1, Bin: r.Bin, Allow: nonNil(r.Allow), Ask: nonNil(r.Ask)}, "", "  ")
	return append(b, '\n')
}

func nonNil(p [][]string) [][]string {
	if p == nil {
		return [][]string{}
	}
	return p
}
