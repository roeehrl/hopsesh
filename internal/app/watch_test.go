package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/observe"
)

func TestSessionWatchExcludesProbeDiagnostics(t *testing.T) {
	a := catalogApp(t)
	changed := make(chan string, 20)
	watch, err := observe.NewFiles(4096, func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	watch.SetChanged(func(path string) { changed <- path })
	if err = watch.SetRoots(a.sessionWatchRoots(context.Background())); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	debug := filepath.Join(home, ".claude", "debug")
	if err = os.MkdirAll(debug, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(debug, "probe.log"), []byte("diagnostic"), 0600); err != nil {
		t.Fatal(err)
	}
	// Keep the existing session watched while unrelated diagnostics are written.
	matches, err := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", "*.jsonl"))
	if err != nil || len(matches) == 0 {
		t.Fatal("fixture sessions missing", err)
	}
	file := matches[0]
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("\n")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	seen := false
	for {
		select {
		case path := <-changed:
			if path == debug || path == filepath.Join(debug, "probe.log") {
				t.Fatal("vendor diagnostics trigger a self-refresh loop")
			}
			if path == file {
				seen = true
			}
		case <-deadline:
			if !seen {
				t.Fatal("session change was not detected")
			}
			return
		}
	}
}
