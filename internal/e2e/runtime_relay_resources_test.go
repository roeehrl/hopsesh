package e2e

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/observe"
	localruntime "github.com/roeehrl/hopsesh/internal/core/runtime"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Four actual private runtime processes and SQLite/R2/verified HTTPS. The
// measured owner has three registered profiles and three approved remote peers.
// This is a headless resource qualification; a full GUI baseline and hardware
// wakeup measurement are deliberately separate release gates.
func TestRuntimeRelayThreeProfilesThreePeersSQLiteR2(t *testing.T) {
	if testing.Short() || os.Getenv("HOPSESH_RELAY_PLATFORM") != "1" {
		t.Skip("actual SQLite/R2 three-profile/three-peer resource qualification")
	}
	ctx, _, origin, cert, http := startSQLiteRelayFixture(t, 2*time.Minute)
	bin := buildHopsesh(t)
	fleet := newRelayFleet(t, ctx, bin, origin, cert, http, 'D')
	owner := fleet.homes['A']
	var profile agent.RuntimeProfile
	if err := json.Unmarshal(fleet.run(t, owner, "accounts", "add", "claude", "Disposable third profile", "--json"), &profile); err != nil || profile.ID == "" {
		t.Fatal("third profile not registered", err)
	}
	clients := map[byte]localruntime.Client{}
	for key, home := range fleet.homes {
		n, err := localruntime.NewNamespace(filepath.Join(home.home, "config"), filepath.Join(home.home, "state"))
		if err != nil {
			t.Fatal(err)
		}
		clients[key] = localruntime.Client{Namespace: n}
	}
	deadline := time.Now().Add(30 * time.Second)
	for key, client := range clients {
		for {
			var snapshot observe.Snapshot
			var observation app.Observation
			if err := client.Call(ctx, "snapshot", nil, &snapshot); err != nil || json.Unmarshal(snapshot.Data, &observation) != nil {
				t.Fatal("fleet observation unavailable", err)
			}
			ready := snapshot.Fresh(time.Now()) && len(observation.Remotes) == 3
			for _, remote := range observation.Remotes {
				ready = ready && remote.Phase == "done" && remote.Status == app.StatusOK && remote.Snapshot.Fresh(time.Now())
			}
			if key == 'A' {
				profiles := 0
				for _, state := range observation.Agents {
					if state.Install.Profile != nil && state.Install.Present {
						profiles++
					}
				}
				ready = ready && profiles >= 3
			}
			if ready {
				break
			}
			if time.Now().After(deadline) {
				t.Logf("owner %c fresh=%t complete=%t error=%t peers=%d profiles=%d", key, snapshot.Fresh(time.Now()), observation.InventoryComplete, snapshot.Error != "", len(observation.Remotes), len(observation.Agents))
				for _, remote := range observation.Remotes {
					t.Logf("disposable peer phase=%s status=%s fresh=%t reason=%q", remote.Phase, remote.Status, remote.Snapshot.Fresh(time.Now()), remote.Error)
				}
				t.Fatalf("three profiles/peers did not become fresh on %c", key)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	client := clients['A']
	var status localruntime.Status
	if err := client.Call(ctx, "status", nil, &status); err != nil {
		t.Fatal(err)
	}
	resources := func() localruntime.Resources {
		t.Helper()
		var sample localruntime.Resources
		if err := client.Call(ctx, "resources", nil, &sample); err != nil || sample.Goroutines <= 0 || sample.HeapBytes == 0 {
			t.Fatal("native resource sample unavailable", err)
		}
		return sample
	}
	baseline := resources()
	var baselineMetrics observe.Metrics
	if err := client.Call(ctx, "metrics", nil, &baselineMetrics); err != nil {
		t.Fatal(err)
	}
	for _, count := range []int{0, 1, 5} {
		watchCtx, cancel := context.WithCancel(ctx)
		var watchers sync.WaitGroup
		defer func() { cancel(); watchers.Wait() }()
		for range count {
			ready := make(chan struct{}, 1)
			watchers.Go(func() {
				_ = client.Watch(watchCtx, func(observe.Snapshot) error {
					select {
					case ready <- struct{}{}:
					default:
					}
					return nil
				})
			})
			select {
			case <-ready:
			case <-time.After(5 * time.Second):
				cancel()
				watchers.Wait()
				t.Fatal("idle client subscription missing")
			}
		}
		var before, after observe.Metrics
		if err := client.Call(ctx, "metrics", nil, &before); err != nil {
			cancel()
			watchers.Wait()
			t.Fatal(err)
		}
		usage, err := readRuntimeUsage(ctx, status.PID)
		if err != nil {
			cancel()
			watchers.Wait()
			t.Fatal(err)
		}
		start := time.Now()
		time.Sleep(2 * time.Second)
		used, err := readRuntimeUsage(ctx, status.PID)
		if err != nil {
			cancel()
			watchers.Wait()
			t.Fatal(err)
		}
		if err := client.Call(ctx, "metrics", nil, &after); err != nil {
			cancel()
			watchers.Wait()
			t.Fatal(err)
		}
		sample := resources()
		cancel()
		watchers.Wait()
		if after.Collections != before.Collections || after.Notifications != before.Notifications {
			t.Fatalf("idle clients multiplied three-profile/peer work: before=%+v after=%+v", before, after)
		}
		if after.Subscribers != baselineMetrics.Subscribers+count {
			t.Fatal("clients did not share the owner subscriptions")
		}
		t.Logf("profiles>=3 peers=3 clients=%d CPU=%.3f%% RSS=%d heap=%d goroutines=%d subscriber delta=%d", count, 100*(used.CPUSeconds-usage.CPUSeconds)/time.Since(start).Seconds(), used.RSSBytes, sample.HeapBytes, sample.Goroutines, after.Subscribers-baselineMetrics.Subscribers)
	}
	// Repeatedly connect/disconnect without creating independent collectors.
	for range 10 {
		watchCtx, cancel := context.WithCancel(ctx)
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			_ = client.Watch(watchCtx, func(observe.Snapshot) error { cancel(); return nil })
		}()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			cancel()
			t.Fatal("cancelled watcher did not join")
		}
		cancel()
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		var metrics observe.Metrics
		if err := client.Call(ctx, "metrics", nil, &metrics); err != nil {
			t.Fatal(err)
		}
		sample := resources()
		if metrics.Subscribers == baselineMetrics.Subscribers && sample.Goroutines <= baseline.Goroutines+8 {
			t.Logf("joined client churn: goroutines before=%d after=%d subscribers=%d", baseline.Goroutines, sample.Goroutines, metrics.Subscribers)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("client churn retained owner tasks: before=%+v after=%+v subscribers=%d", baseline, sample, metrics.Subscribers)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
