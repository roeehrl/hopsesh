package opencode

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// hopsesh's approval rules are a file the plugin reads, beside the plugin's staging
// folder; the skill goes where Codex's does.
func TestRulesFile(t *testing.T) {
	h, in, config := pluginInstall(t, "2.3.0")
	ig := New().Integration(h, in)
	if ig.SkillDir != filepath.Join(h.Facts().Home, ".agents", "skills") {
		t.Fatalf("skills: %s", ig.SkillDir)
	}
	if ig.Rules == nil || !ig.Rules.Owned || ig.Rules.Path != filepath.Join(config, ".hopsesh", "rules.json") {
		t.Fatalf("rules: %+v", ig.Rules)
	}
	b := ig.Rules.Render(agent.Rules{Bin: "hopsesh", Allow: [][]string{{"ls"}, {"hosts", "--json"}}, Ask: [][]string{{"pull"}}})
	var r pluginRules
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if r.Format != 1 || r.Bin != "hopsesh" || len(r.Allow) != 2 || !slices.Equal(r.Allow[1], []string{"hosts", "--json"}) || !slices.Equal(r.Ask[0], []string{"pull"}) {
		t.Fatalf("rules.json: %s", b)
	}
	if b := ig.Rules.Render(agent.Rules{Bin: "hopsesh"}); !strings.Contains(string(b), `"allow": []`) {
		t.Fatalf("no rules must still be lists: %s", b)
	}
}
