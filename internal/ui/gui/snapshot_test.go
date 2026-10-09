package gui

import (
	"fmt"
	"testing"

	"github.com/roeehrl/hopsesh/internal/agents/all"
)

// Changing cloud consent or its environment must not alter a scan that already
// captured its settings, including the catalog key used to publish its result.
func TestSnapshotKeepsIndependentCloudConfiguration(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	t.Cleanup(func() { _ = a.core.Catalog.Close() })
	if err := a.SetCloudAllowed("codex-cloud", true); err != nil {
		t.Fatal(err)
	}
	if err := a.SetCloudEnvironment("codex-cloud", "github.com/example/demo", "before"); err != nil {
		t.Fatal(err)
	}
	before := a.snapshot()
	scope := before.CatalogScope()
	if err := a.SetCloudEnvironment("codex-cloud", "github.com/example/demo", "after"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetCloudAllowed("codex-cloud", false); err != nil {
		t.Fatal(err)
	}
	if !before.Cfg.CloudAllowed("codex-cloud") || before.Cfg.Clouds["codex-cloud"].Environments["github.com/example/demo"] != "before" || before.CatalogScope() != scope {
		t.Fatal("window settings mutated an in-flight scan's configuration")
	}
	before.Cfg.SetCloudEnvironment("codex-cloud", "github.com/example/demo", "private")
	if a.snapshot().Cfg.Clouds["codex-cloud"].Environments["github.com/example/demo"] != "after" {
		t.Fatal("scan snapshot mutated the window's current configuration")
	}
}

func TestSnapshotCatalogScopeDuringSettingsChanges(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	t.Cleanup(func() { _ = a.core.Catalog.Close() })
	if err := a.SetCloudEnvironment("codex-cloud", "github.com/example/demo", "before"); err != nil {
		t.Fatal(err)
	}
	scan := a.snapshot()
	scope := scan.CatalogScope()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			if scan.CatalogScope() != scope {
				t.Error("catalog scope changed while its scan was running")
				return
			}
		}
	}()
	for i := 0; i < 25; i++ {
		if err := a.SetCloudEnvironment("codex-cloud", "github.com/example/demo", fmt.Sprint(i)); err != nil {
			t.Error(err)
			break
		}
		// Capturing another scan used to decode into shared maps, even when
		// the settings lock correctly protected the current configuration.
		_ = a.snapshot()
	}
	<-done
}
