package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDesktopNewAndExistingInstall(t *testing.T) {
	t.Setenv("HOPSESH_CONFIG_DIR", t.TempDir())
	c, err := Load()
	if err != nil || c.Desktop.Placement() != "both" {
		t.Fatalf("new install: %+v %v", c.Desktop, err)
	}
	c.Desktop = Desktop{}
	if err = Save(&c); err != nil {
		t.Fatal(err)
	}
	c, err = Load()
	if err != nil || c.Desktop.Placement() != "app" {
		t.Fatalf("existing install: %+v %v", c.Desktop, err)
	}
	no := false
	c.Desktop = Desktop{Mode: "tray", Close: "keep", Attention: &no, Previews: &no}
	if err = Save(&c); err != nil {
		t.Fatal(err)
	}
	c, err = Load()
	if err != nil || c.Desktop.Placement() != "tray" || c.Desktop.PreviewsOn() || c.Desktop.AttentionOn() {
		t.Fatalf("round trip: %+v %v", c.Desktop, err)
	}
}
func TestDesktopInvalidSettings(t *testing.T) {
	for _, p := range []Desktop{{Mode: "dock-ish"}, {Close: "minimize"}} {
		c := Defaults()
		c.Desktop = p
		if c.Check() == nil {
			t.Fatal("accepted invalid desktop settings")
		}
	}
}
func TestDesktopMissingSection(t *testing.T) {
	t.Setenv("HOPSESH_CONFIG_DIR", t.TempDir())
	// A pre-feature config has no desktop table at all.
	if err := os.WriteFile(filepath.Join(Dir(), "config.toml"), []byte("schema = 5\nlayout = 'flat'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil || c.Desktop.Mode != "" {
		t.Fatalf("changed existing preference: %+v %v", c.Desktop, err)
	}
}
