package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/core/observe"
	"github.com/roeehrl/hopsesh/internal/core/registry"
)

type snapshotWriter struct {
	mu    sync.Mutex
	buf   string
	lines chan string
}

func (w *snapshotWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf += string(p)
	for {
		line, rest, ok := strings.Cut(w.buf, "\n")
		if !ok {
			break
		}
		w.buf = rest
		select {
		case w.lines <- line:
		default:
		}
	}
	return len(p), nil
}

func TestRuntimeWatchUsesChangeNotificationsAndCancels(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOPSESH_CONFIG_DIR", filepath.Join(home, "config"))
	t.Setenv("HOPSESH_STATE_DIR", filepath.Join(home, "state"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude"))
	p := filepath.Join(home, "claude", "projects", "p", "33333333-3333-4333-8333-333333333333.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	first := `{"type":"user","uuid":"u1","sessionId":"33333333-3333-4333-8333-333333333333","cwd":"/project","timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","content":"before change"}}`
	if err := os.WriteFile(p, []byte(first+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := registry.New(claude.New())
	if err != nil {
		t.Fatal(err)
	}
	out := &snapshotWriter{lines: make(chan string, 20)}
	cmd := NewRoot(out, r)
	ctx, cancel := context.WithCancel(context.Background())
	cmd.SetContext(ctx)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"runtime", "observe", "--watch", "--reconcile", "24h"})
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("watch did not stop")
		}
	}()
	read := func() observe.Snapshot {
		t.Helper()
		select {
		case line := <-out.lines:
			var snapshot observe.Snapshot
			if err := json.Unmarshal([]byte(line), &snapshot); err != nil {
				t.Fatal(err)
			}
			if snapshot.Error != "" {
				t.Fatal(snapshot.Error)
			}
			return snapshot
		case <-time.After(5 * time.Second):
			t.Fatal("no filesystem-triggered snapshot")
			return observe.Snapshot{}
		}
	}
	initial := read()
	second := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(first, "u1", "u2"), "before change", "after change"), "10:00:00Z", "10:00:05Z")
	if err := os.WriteFile(p, []byte(first+"\n"+second+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for {
		next := read()
		if strings.Contains(string(next.Data), "after change") {
			if next.Epoch != initial.Epoch || next.Sequence <= initial.Sequence {
				t.Fatal("invalid observation continuity")
			}
			break
		}
	}
	if _, err := os.Stat(filepath.Join(home, "config")); !os.IsNotExist(err) {
		t.Fatal("watch initialized configuration")
	}
	if _, err := os.Stat(filepath.Join(home, "state")); !os.IsNotExist(err) {
		t.Fatal("watch wrote account or audit state")
	}
}
