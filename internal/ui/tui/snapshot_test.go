package tui

import (
	"encoding/json"
	"testing"

	"github.com/roeehrl/hopsesh/internal/app"
)

// Capturing a command on the model thread must not write the current settings;
// other background readers can still be using them. Run this under -race.
func TestDiscoverCaptureDoesNotWriteLiveSettings(t *testing.T) {
	m := newModel(t)
	m.deps.App.Cfg.SetCloudEnvironment("codex-cloud", "github.com/example/demo", "before")
	t.Cleanup(func() {
		if m.scanCancel != nil {
			m.scanCancel()
		}
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			if _, err := json.Marshal(m.deps.App.Cfg); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for i := 0; i < 100; i++ {
		_ = m.discover(app.ScanOptions{})
	}
	<-done
}
