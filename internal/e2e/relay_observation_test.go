package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/observe"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	localruntime "github.com/roeehrl/hopsesh/internal/core/runtime"
)

// Real filesystem -> shared source -> authenticated encrypted publication ->
// SQLite/R2 notification -> recipient runtime. No explicit remote Scan calls.
func TestRelayObservationChangesSQLiteR2(t *testing.T) {
	if testing.Short() || os.Getenv("HOPSESH_RELAY_PLATFORM") != "1" {
		t.Skip("actual SQLite/R2 source observation qualification")
	}
	ctx, _, origin, cert, http := startSQLiteRelayFixture(t, 2*time.Minute)
	fleet := newRelayFleet(t, ctx, buildHopsesh(t), origin, cert, http)
	clients := map[byte]localruntime.Client{}
	for key, home := range fleet.homes {
		n, err := localruntime.NewNamespace(filepath.Join(home.home, "config"), filepath.Join(home.home, "state"))
		if err != nil {
			t.Fatal(err)
		}
		clients[key] = localruntime.Client{Namespace: n}
	}
	remote := func() app.RemoteObservation {
		t.Helper()
		var snapshot observe.Snapshot
		if err := clients['B'].Call(ctx, "snapshot", nil, &snapshot); err != nil {
			t.Fatal(err)
		}
		var obs app.Observation
		if err := json.Unmarshal(snapshot.Data, &obs); err != nil {
			t.Fatal(err)
		}
		for _, state := range obs.Remotes {
			if state.Binding.Name == fleet.homes['A'].name {
				return state
			}
		}
		return app.RemoteObservation{}
	}
	wait := func(label string, check func(app.RemoteObservation) bool) app.RemoteObservation {
		t.Helper()
		deadline := time.Now().Add(25 * time.Second)
		for {
			state := remote()
			if check(state) {
				return state
			}
			if time.Now().After(deadline) {
				fleet.logHealth(t)
				t.Fatal(label, state.Phase, state.Status, state.Error)
			}
			time.Sleep(30 * time.Millisecond)
		}
	}
	wait("initial remote evidence", func(s app.RemoteObservation) bool { return s.Status == app.StatusOK && s.Snapshot.Fresh(time.Now()) })
	var before relay.Health
	if err := clients['B'].Call(ctx, "relay.status", nil, &before); err != nil {
		t.Fatal(err)
	}
	seed := fleet.seed(t, "codex")
	fresh := wait("filesystem change not pushed", func(s app.RemoteObservation) bool {
		var obs app.Observation
		if !s.Snapshot.Fresh(time.Now()) || json.Unmarshal(s.Snapshot.Data, &obs) != nil {
			return false
		}
		for _, entry := range obs.Entries {
			if entry.Session.Key.Agent == seed.Key.Agent && entry.Session.Key.Session == seed.Key.Session {
				return true
			}
		}
		return false
	})
	var after relay.Health
	if err := clients['B'].Call(ctx, "relay.status", nil, &after); err != nil || after.ObservationReceived <= before.ObservationReceived {
		t.Fatal("inventory changed without authenticated source publication", err, before, after)
	}
	if fresh.Snapshot.ExpiresAt.Sub(fresh.Snapshot.ObservedAt) > relay.ObservationLease {
		t.Fatal("receiver renewed source lease")
	}
	if err := clients['A'].Call(ctx, "pause", nil, nil); err != nil {
		t.Fatal(err)
	}
	paused := wait("source pause not published", func(s app.RemoteObservation) bool { return s.Snapshot.Paused && !s.Snapshot.Fresh(time.Now()) })
	if paused.Snapshot.ObservedAt.After(fresh.Snapshot.ExpiresAt) {
		t.Fatal("pause fabricated source evidence")
	}
	if err := clients['A'].Call(ctx, "resume", nil, nil); err != nil {
		t.Fatal(err)
	}
	wait("resumed source did not publish new evidence", func(s app.RemoteObservation) bool {
		return s.Snapshot.Fresh(time.Now()) && s.Snapshot.ObservedAt.After(fresh.Snapshot.ObservedAt)
	})
	// A full coalescing window without a new collection must not echo remote
	// snapshots back through the mesh. Count admitted source publications only.
	time.Sleep(11 * time.Second)
	for key, client := range clients {
		var health relay.Health
		if err := client.Call(ctx, "relay.status", nil, &health); err != nil {
			t.Fatal(err)
		}
		if health.ObservationFailed != 0 || health.ObservationSent > 16 {
			t.Fatalf("owner %c publication failure or echo: %+v", key, health)
		}
	}
}
