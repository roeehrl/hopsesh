package config

import (
	"os"
	"strings"
	"testing"
)

func TestAppearancePersistenceAndDefault(t *testing.T) {
	t.Setenv("HOPSESH_CONFIG_DIR", t.TempDir())
	c, err := Load()
	if err != nil || c.AppearanceMode() != "system" {
		t.Fatalf("default: %q, %v", c.AppearanceMode(), err)
	}
	for _, mode := range []string{"", "light", "dark", "system"} {
		c.Appearance = mode
		if err := c.Check(); err != nil {
			t.Fatal(err)
		}
		if err := Save(c); err != nil {
			t.Fatal(err)
		}
		back, err := Load()
		if err != nil || back.Appearance != mode {
			t.Fatalf("round trip %q: %q, %v", mode, back.Appearance, err)
		}
	}
	c.Appearance = "sepia"
	if err := c.Check(); err == nil {
		t.Fatal("invalid appearance accepted")
	}
	// A bad value in a hand-edited config must also be rejected on startup.
	b, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), []byte(strings.Replace(string(b), `appearance = "system"`, `appearance = "sepia"`, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("invalid saved appearance accepted")
	}
}
