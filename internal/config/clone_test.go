package config

import (
	"reflect"
	"testing"
)

func TestCloneSeparatesMutableSettings(t *testing.T) {
	off := false
	c := Defaults()
	c.Desktop.Previews = &off
	c.Terminal.KeepTabs = &off
	c.MovementNotices = &off
	c.Hosts = []Host{{Name: "studio", Destination: "studio", Allowed: true}}
	c.FamilyNames = map[string]string{"family": "Before"}
	c.Agents = map[string]Agent{"claude": {Place: PlaceHere}}
	c.Inspector.Open = []string{"conversation"}
	c.List.Filter.Agent = []string{"claude"}
	c.Clouds = map[string]Cloud{"codex-cloud": {Allowed: true, RenameVendorBranches: &off,
		Environments: map[string]string{"github.com/example/demo": "before"}, Untracked: []string{"notes.txt"}}}
	copy := c.Clone()
	if !reflect.DeepEqual(c, copy) {
		t.Fatal("copy lost configuration values")
	}
	*copy.Desktop.Previews = true
	*copy.Terminal.KeepTabs = true
	*copy.MovementNotices = true
	copy.Hosts[0].Allowed = false
	copy.FamilyNames["family"] = "After"
	copy.Agents["claude"] = Agent{Disabled: true}
	copy.Inspector.Open[0] = "details"
	copy.List.Filter.Agent[0] = "codex"
	*copy.Clouds["codex-cloud"].RenameVendorBranches = true
	copy.Clouds["codex-cloud"].Environments["github.com/example/demo"] = "after"
	copy.Clouds["codex-cloud"].Untracked[0] = "other.txt"
	if off || !c.Hosts[0].Allowed || c.FamilyNames["family"] != "Before" || c.Agents["claude"].Disabled ||
		c.Inspector.Open[0] != "conversation" || c.List.Filter.Agent[0] != "claude" ||
		c.Clouds["codex-cloud"].Environments["github.com/example/demo"] != "before" || c.Clouds["codex-cloud"].Untracked[0] != "notes.txt" {
		t.Fatal("copy retained mutable references to its source")
	}
}

func TestClonePreservesUnsetAndEmptySettings(t *testing.T) {
	for _, c := range []Config{{}, {Clouds: map[string]Cloud{}, Hosts: []Host{}, List: List{Collapsed: []string{}}}} {
		if !reflect.DeepEqual(c, c.Clone()) {
			t.Fatal("copy changed unset or explicitly empty settings")
		}
	}
}
