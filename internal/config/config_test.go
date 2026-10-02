package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSaveAndRefuseOldFormat(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOPSESH_CONFIG_DIR", dir)
	c, err := Load()
	if err != nil || c.Schema != Schema || c.Layout != "flat" {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	c.UpsertHost(Host{Name: "studio", Destination: "me@studio", Allowed: true})
	c.Agents = map[string]Agent{"codex": {Disabled: true}}
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	back, err := Load()
	if err != nil || back.FindHost("studio") == nil || back.AgentEnabled("codex") || !back.AgentEnabled("claude") {
		t.Fatalf("round trip: %+v %v", back, err)
	}
	old := "repos_dir = \"/x\"\nlive_policy = \"handoff\"\n[[hosts]]\nname = \"a\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); !errors.Is(err, ErrOldConfig) {
		t.Fatalf("an older config must be refused, got %v", err)
	}
}
