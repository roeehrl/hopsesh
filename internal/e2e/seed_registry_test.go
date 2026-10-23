package e2e

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestSeedExcludesProcessRegistryButExplicitActivityStillWorks(t *testing.T) {
	box := newLocation(t, "box", t.TempDir())
	seed(t, box)
	dir := filepath.Join(box.in.Root("home"), "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("seed imported foreign process registry: %v", entries)
	}
	mod := claude.New()
	h, err := box.m.For(context.Background(), mod.Spec(), box.in, nil)
	if err != nil {
		t.Fatal(err)
	}
	read := func() agent.LiveInfo {
		t.Helper()
		live, err := mod.Live(context.Background(), h, box.in, []agent.SessionID{sid})
		if err != nil {
			t.Fatal(err)
		}
		return live[sid]
	}
	if got := read(); got.State != agent.Ended {
		t.Fatalf("inactive seeded transcript: %+v", got)
	}
	// Only this test process is used. Never stop it or probe a guessed runner PID.
	// An explicit registry must still exercise the real production Alive check.
	b, err := json.Marshal(map[string]any{"pid": os.Getpid(), "sessionId": sid, "status": "busy", "entrypoint": "cli"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, strconv.Itoa(os.Getpid())+".json")
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.State != agent.Live || got.PID != os.Getpid() || len(got.Procs) != 1 {
		t.Fatalf("explicit owned registry no longer detected: %+v", got)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.State != agent.Ended {
		t.Fatalf("removed explicit registry remained live: %+v", got)
	}
}
