package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Integration: the skill goes in <config>/skills; approval rules are permission rules in
// <config>/settings.json, merged with what the user has there.
func (m *Module) Integration(h agent.Host, in agent.Install) agent.Integration {
	pa := h.Path()
	return agent.Integration{
		SkillDir: pa.Join(in.Root(home), "skills"),
		Rules: &agent.RuleFile{
			Path:   pa.Join(in.Root(home), "settings.json"),
			Merge:  mergeRules,
			Has:    hasRules,
			Remove: removeRules,
		},
	}
}

// permission turns a command prefix into a Claude Code permission rule.
func permission(bin string, prefix []string) string {
	return "Bash(" + bin + " " + strings.Join(prefix, " ") + " *)"
}

func rulesOf(r agent.Rules) (allow, ask []string) {
	for _, p := range r.Allow {
		allow = append(allow, permission(r.Bin, p))
	}
	for _, p := range r.Ask {
		ask = append(ask, permission(r.Bin, p))
	}
	return
}

func parseSettings(b []byte) (map[string]any, error) {
	doc := map[string]any{}
	if len(bytes.TrimSpace(b)) == 0 {
		return doc, nil
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("settings.json is not valid JSON; not changing it: %w", err)
	}
	return doc, nil
}

func mergeRules(existing []byte, r agent.Rules) ([]byte, error) {
	doc, err := parseSettings(existing)
	if err != nil {
		return nil, err
	}
	allow, ask := rulesOf(r)
	perms, _ := doc["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
	}
	add := func(key string, rules []string) {
		cur, _ := perms[key].([]any)
		have := map[string]bool{}
		for _, x := range cur {
			if s, ok := x.(string); ok {
				have[s] = true
			}
		}
		for _, x := range rules {
			if !have[x] {
				cur = append(cur, x)
			}
		}
		perms[key] = cur
	}
	add("allow", allow)
	add("ask", ask)
	doc["permissions"] = perms
	return marshalSettings(doc)
}

func hasRules(existing []byte, r agent.Rules) bool {
	doc, err := parseSettings(existing)
	if err != nil {
		return false
	}
	perms, _ := doc["permissions"].(map[string]any)
	allow, ask := rulesOf(r)
	return containsAll(perms["allow"], allow) && containsAll(perms["ask"], ask)
}

func removeRules(existing []byte, r agent.Rules) ([]byte, error) {
	doc, err := parseSettings(existing)
	if err != nil {
		return nil, err
	}
	perms, _ := doc["permissions"].(map[string]any)
	if perms == nil {
		return existing, nil
	}
	allow, ask := rulesOf(r)
	drop := map[string]bool{}
	for _, x := range append(allow, ask...) {
		drop[x] = true
	}
	for _, key := range []string{"allow", "ask"} {
		cur, _ := perms[key].([]any)
		var kept []any
		for _, x := range cur {
			if s, ok := x.(string); !ok || !drop[s] {
				kept = append(kept, x)
			}
		}
		if kept == nil {
			delete(perms, key)
		} else {
			perms[key] = kept
		}
	}
	return marshalSettings(doc)
}

func containsAll(list any, want []string) bool {
	cur, _ := list.([]any)
	have := map[string]bool{}
	for _, x := range cur {
		if s, ok := x.(string); ok {
			have[s] = true
		}
	}
	for _, w := range want {
		if !have[w] {
			return false
		}
	}
	return true
}

func marshalSettings(doc map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// marshalNoEscape is json.Marshal without HTML escaping (Claude Code writes < > & as is).
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
