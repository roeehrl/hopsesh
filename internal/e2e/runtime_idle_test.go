package e2e

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/observe"
	localruntime "github.com/roeehrl/hopsesh/internal/core/runtime"
)

func TestRuntimeIdleClientsDoNotMultiplyCollections(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the actual CLI host")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	box := newMachineHome(t, t.TempDir(), "runtime-idle", false)
	bin := buildHopsesh(t)
	cfg := config.Defaults()
	cfg.Runtime.ReconcileSeconds = 300
	box.writeConfig(t, cfg)
	cmd := exec.CommandContext(ctx, bin, "runtime", "serve")
	cmd.Env = box.env()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	var status localruntime.Status
	if err = json.NewDecoder(stdout).Decode(&status); err != nil {
		t.Fatal(err)
	}
	n, err := localruntime.NewNamespace(filepath.Join(box.home, "config"), filepath.Join(box.home, "state"))
	if err != nil {
		t.Fatal(err)
	}
	client := localruntime.Client{Namespace: n}
	var owner localruntime.Status
	if err = client.Call(ctx, "status", nil, &owner); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = client.Call(stopCtx, "stop", nil, nil)
	}()
	var snapshot observe.Snapshot
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err = client.Call(ctx, "snapshot", nil, &snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshot.Fresh(time.Now()) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("initial observation unavailable")
		}
		time.Sleep(10 * time.Millisecond)
	}
	waitRuntimeIdle(t, ctx, client)
	for _, count := range []int{0, 1, 5} {
		t.Run(string(rune('0'+count))+"-clients", func(t *testing.T) {
			watchCtx, stop := context.WithCancel(ctx)
			var watchers sync.WaitGroup
			defer func() { stop(); watchers.Wait() }()
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
					t.Fatal("subscriber did not receive shared evidence")
				}
			}
			var before, after observe.Metrics
			if err = client.Call(ctx, "metrics", nil, &before); err != nil {
				t.Fatal(err)
			}
			usageBefore, err := readRuntimeUsage(ctx, owner.PID)
			if err != nil {
				t.Fatal("native idle counters", err)
			}
			started := time.Now()
			// A finite idle measurement, not a claim about macOS power counters.
			time.Sleep(2 * time.Second)
			usageAfter, err := readRuntimeUsage(ctx, owner.PID)
			if err != nil {
				t.Fatal("native idle counters", err)
			}
			cpuPercent := 100 * (usageAfter.CPUSeconds - usageBefore.CPUSeconds) / time.Since(started).Seconds()
			if err = client.Call(ctx, "metrics", nil, &after); err != nil {
				t.Fatal(err)
			}
			if after.Collections != before.Collections || after.Notifications != before.Notifications {
				t.Fatalf("idle clients created work: before %+v, after %+v", before, after)
			}
			t.Logf("clients=%d collections=%d notifications=%d subscribers=%d", count, after.Collections-before.Collections, after.Notifications-before.Notifications, after.Subscribers)
			t.Logf("native idle sample: one-core CPU=%.3f%% RSS=%d bytes RSS delta=%d bytes", cpuPercent, usageAfter.RSSBytes, usageAfter.RSSBytes-usageBefore.RSSBytes)
			if cpuPercent < 0 || usageAfter.RSSBytes <= 0 {
				t.Fatal("native process counters are invalid")
			}
		})
	}
}

// waitRuntimeIdle observes scheduler state, not a guessed startup sleep. A
// bounded quiet interval also allows peer publications already in transit to
// arrive. Call only before adding the clients whose behavior is measured.
func waitRuntimeIdle(t *testing.T, ctx context.Context, client localruntime.Client) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	quietFor := observe.Defaults().MaxDelay + observe.Defaults().Debounce
	var previous observe.Metrics
	var quietSince time.Time
	for {
		var current observe.Metrics
		if err := client.Call(ctx, "metrics", nil, &current); err != nil {
			t.Fatal("runtime settling metrics unavailable", err)
		}
		if current.Collecting || current.Pending || current.Paused || current.Collections != previous.Collections || current.Notifications != previous.Notifications {
			quietSince = time.Time{}
		} else if quietSince.IsZero() {
			quietSince = time.Now()
		} else if time.Since(quietSince) >= quietFor {
			return
		}
		previous = current
		if time.Now().After(deadline) {
			t.Fatalf("runtime did not reach an idle baseline: %+v", current)
		}
		select {
		case <-ctx.Done():
			t.Fatal("runtime settling cancelled", ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}
