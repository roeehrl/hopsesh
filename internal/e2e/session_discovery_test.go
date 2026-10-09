package e2e

import (
	"context"
	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// This lifecycle runs in the existing three-OS mandatory scenario matrix.
func TestSessionDiscoveryRestartDeletionAndAccountScope(t *testing.T) {
	here := newMachineHome(t, t.TempDir(), "here", true)
	here.writeConfig(t, config.Config{})
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a := app.New(cfg, all.Registry(), config.StateDir(), nil)
	defer a.Catalog.Close()
	inv := a.Scan(context.Background(), app.ScanOptions{SkipGit: true})
	if len(inv.Entries) == 0 {
		t.Fatal("fixture not discovered")
	}
	e := inv.Entries[0]
	inv.Close()
	b := app.New(cfg, all.Registry(), config.StateDir(), nil)
	defer b.Catalog.Close()
	start := time.Now()
	saved := b.CachedInventory()
	t.Logf("saved %d rows loaded in %s", len(saved.Entries), time.Since(start))
	for _, x := range saved.Entries {
		if !x.Cached || x.Live.State != agent.Unknown {
			t.Fatal("restart invented presence")
		}
	}
	if err = os.Remove(filepath.FromSlash(e.Session.Path)); err != nil {
		t.Fatal(err)
	}
	if fresh, _, err := b.FreshSelection(context.Background(), e); err == nil {
		fresh.Close()
		t.Fatal("deleted cached session authorized action")
	}
	b.Scan(context.Background(), app.ScanOptions{SkipGit: true}).Close()
	for _, x := range b.CachedInventory().Entries {
		if x.Session.Key == e.Session.Key {
			t.Fatal("successful refresh resurrected deleted row")
		}
	}
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	if len(b.CachedInventory().Entries) != 0 {
		t.Fatal("changed account root exposed old catalog")
	}
}
