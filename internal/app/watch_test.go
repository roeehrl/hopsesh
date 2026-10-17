package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/observe"
)

func TestSessionWatchExcludesProbeDiagnostics(t *testing.T) {
	a := catalogApp(t)
	observation, err := a.ObserveLocal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for name, roots := range map[string][]string{"standalone": a.sessionWatchRoots(t.Context()), "shared runtime": observation.WatchRoots} {
		t.Run(name, func(t *testing.T) { checkSessionWatchRoots(t, roots) })
	}
}

func checkSessionWatchRoots(t *testing.T, roots []string) {
	t.Helper()
	home, _ := os.UserHomeDir()
	registry := filepath.Join(home, ".claude", "sessions", "1234.json")
	if err := os.MkdirAll(filepath.Dir(registry), 0700); err != nil {
		t.Fatal(err)
	}
	changed := make(chan string, 20)
	watch, err := observe.NewFiles(4096, func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	watch.SetChanged(func(path string) { changed <- path })
	if err = watch.SetRoots(roots); err != nil {
		t.Fatal(err)
	}
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
	if err = os.WriteFile(registry, []byte(`{"pid":1234,"status":"idle"}`), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	seen, presenceSeen := false, false
	for {
		select {
		case path := <-changed:
			if path == debug || path == filepath.Join(debug, "probe.log") {
				t.Fatal("vendor diagnostics trigger a self-refresh loop")
			}
			if path == file {
				seen = true
			}
			if path == registry {
				presenceSeen = true
			}
		case <-deadline:
			if !seen {
				t.Fatal("session change was not detected")
			}
			if !presenceSeen {
				t.Fatal("process registry change was not detected")
			}
			return
		}
	}
}
