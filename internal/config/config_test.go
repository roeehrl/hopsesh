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
	old, err = SetAside()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("the old file must be kept: %v", err)
	}
	if c, err := Load(); err != nil || len(c.Hosts) != 0 {
		t.Fatalf("after setting it aside hopsesh starts fresh: %+v %v", c, err)
	}
	// The format before clouds (schema 3) is refused like any other.
	three := "schema = 3\nrepos_dir = \"/x\"\nlayout = \"flat\"\n[[hosts]]\nname = \"a\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(three), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); !errors.Is(err, ErrOldConfig) {
		t.Fatalf("a schema 3 file must be refused, got %v", err)
	}
}

func TestClouds(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOPSESH_CONFIG_DIR", dir)
	file := `schema = 4

[clouds.claude-cloud]
allowed = true
code = "bundle"
untracked = ["docs/plan.md"]
rename_vendor_branches = false

[clouds.codex-cloud]
allowed = false
[clouds.codex-cloud.environments]
"github.com/acme/api" = "env_1"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.CloudAllowed("claude-cloud") || c.CloudAllowed("codex-cloud") || c.CloudAllowed("jules") {
		t.Fatalf("consent: %+v", c.Clouds)
	}
	cl := c.CloudSettings("claude-cloud")
	if cl.Code != CloudCodeBundle || cl.BranchPrefix != DefaultBranchPrefix || cl.DeleteBranch != DeleteAfterMerge ||
		*cl.RenameVendorBranches || len(cl.Untracked) != 1 {
		t.Fatalf("claude-cloud settings: %+v", cl)
	}
	if d := c.CloudSettings("jules"); d.Code != CloudCodeBranch || !*d.RenameVendorBranches || d.Allowed {
		t.Fatalf("defaults for a cloud never set up: %+v", d)
	}
	if c.CloudSettings("codex-cloud").Environments["github.com/acme/api"] != "env_1" {
		t.Fatalf("environments: %+v", c.Clouds["codex-cloud"])
	}
	c.SetCloudAllowed("codex-cloud", true)
	c.SetCloudAllowed("jules", true)
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	back, err := Load()
	if err != nil || !back.CloudAllowed("codex-cloud") || !back.CloudAllowed("jules") || back.CloudSettings("codex-cloud").Environments["github.com/acme/api"] != "env_1" {
		t.Fatalf("round trip: %+v %v", back.Clouds, err)
	}
	bad := "schema = 4\n[clouds.claude-cloud]\ncode = \"zip\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil || errors.Is(err, ErrOldConfig) {
		t.Fatalf("an unknown code way must be refused (not as an old file), got %v", err)
	}
}
