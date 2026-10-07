package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/observe"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func remoteEngineFixture(t *testing.T, cfg config.Config, r *remoteObserver) (*observe.Engine, func()) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOPSESH_CONFIG_DIR", filepath.Join(dir, "config"))
	t.Setenv("HOPSESH_STATE_DIR", filepath.Join(dir, "state"))
	if err := config.Save(&cfg); err != nil {
		t.Fatal(err)
	}
	opts := observe.Defaults()
	opts.Reconcile = time.Hour
	opts.Debounce = time.Millisecond
	opts.MaxDelay = 2 * time.Millisecond
	engine, err := observe.New(opts, func(context.Context) (json.RawMessage, error) { return json.Marshal(r.latest()) })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	ownerDone, engineDone := make(chan struct{}), make(chan struct{})
	go func() { defer close(engineDone); _ = engine.Run(ctx) }()
	go func() { defer close(ownerDone); r.run(ctx, engine) }()
	return engine, func() { cancel(); <-ownerDone; <-engineDone }
}

func TestSharedRemoteRequestsCoalesceAndRemovedMachineCannotReturn(t *testing.T) {
	h := config.Host{Name: "box", Destination: "user@box", Allowed: true}
	cfg := config.Defaults()
	cfg.Hosts = []config.Host{h}
	started, release := make(chan struct{}, 4), make(chan struct{})
	var calls atomic.Int32
	r := newRemoteObserver(nil)
	r.collect = func(ctx context.Context, h config.Host) RemoteObservation {
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		body, _ := json.Marshal(Observation{Machine: h.Name, InventoryComplete: true})
		return RemoteObservation{Status: StatusOK, Snapshot: observe.Snapshot{Data: body}}
	}
	engine, stop := remoteEngineFixture(t, cfg, r)
	defer stop()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("owner did not scan added machine")
	}
	replies := []chan remoteRefreshReply{make(chan remoteRefreshReply, 1), make(chan remoteRefreshReply, 1)}
	for _, reply := range replies {
		select {
		case r.requests <- remoteRefresh{name: h.Name, reply: reply}:
		case <-time.After(time.Second):
			t.Fatal("refresh not accepted")
		}
	}
	close(release)
	for _, reply := range replies {
		select {
		case out := <-reply:
			if out.err != nil {
				t.Fatal(out.err)
			}
		case <-time.After(time.Second):
			t.Fatal("shared refresh not returned")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("clients duplicated the owner scan", calls.Load())
	}
	saved, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	saved.Hosts = nil
	if err = config.Save(&saved); err != nil {
		t.Fatal(err)
	}
	engine.Notify()
	deadline := time.Now().Add(time.Second)
	for len(r.latest()) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("removed machine remained in shared inventory")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err = r.refresh(t.Context(), h.Name); err == nil {
		t.Fatal("removed machine was scanned")
	}
}

func TestRemoteAuthenticationFailureNeedsExplicitRetryAndShutdownJoins(t *testing.T) {
	h := config.Host{Name: "box", Destination: "user@box", Allowed: true}
	cfg := config.Defaults()
	cfg.Hosts = []config.Host{h}
	r := newRemoteObserver(nil)
	r.interval = 10 * time.Millisecond
	var calls atomic.Int32
	r.collect = func(context.Context, config.Host) RemoteObservation {
		calls.Add(1)
		return RemoteObservation{Status: StatusAuth, Error: "authentication requires explicit action"}
	}
	_, stop := remoteEngineFixture(t, cfg, r)
	defer stop()
	deadline := time.Now().Add(time.Second)
	for calls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("initial scan missing")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(40 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatal("authentication was automatically retried")
	}
	if _, err := r.refresh(t.Context(), h.Name); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("explicit retry missing")
	}
}

func TestRelayReadyRetriesFailedObservationWithoutRefreshingHealthyOrAuthPeers(t *testing.T) {
	for _, inFlight := range []bool{false, true} {
		t.Run(map[bool]string{false: "settled-failure", true: "late-failure"}[inFlight], func(t *testing.T) {
			h := config.Host{Name: "disposable-relay", RelayID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Via: "relay", Allowed: true}
			cfg := config.Defaults()
			cfg.Hosts = []config.Host{h}
			r := newRemoteObserver(nil)
			started, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			r.collect = func(ctx context.Context, _ config.Host) RemoteObservation {
				if calls.Add(1) == 1 {
					close(started)
					if inFlight {
						select {
						case <-release:
						case <-ctx.Done():
						}
					}
					return RemoteObservation{Status: StatusError, Error: "local relay is not connected"}
				}
				return RemoteObservation{Status: StatusOK, Snapshot: observe.Snapshot{ObservedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour), Data: json.RawMessage(`{"inventoryComplete":true}`)}}
			}
			_, stop := remoteEngineFixture(t, cfg, r)
			defer stop()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("initial automatic scan missing")
			}
			if !inFlight {
				deadline := time.Now().Add(time.Second)
				for r.latest()[0].Phase != "done" {
					if time.Now().After(deadline) {
						t.Fatal("initial failure did not settle")
					}
					time.Sleep(time.Millisecond)
				}
			}
			r.relayConnected()
			if inFlight {
				// Deliver the connection transition while collection is still
				// blocked, before returning the already-obsolete failure.
				deadline := time.Now().Add(time.Second)
				for len(r.relayReady) > 0 {
					if time.Now().After(deadline) {
						t.Fatal("connection signal was not consumed")
					}
					time.Sleep(time.Millisecond)
				}
				close(release)
			}
			deadline := time.Now().Add(time.Second)
			for {
				states := r.latest()
				if len(states) == 1 && states[0].Status == StatusOK && states[0].Snapshot.Fresh(time.Now()) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("relay readiness left a failed peer in minute-long backoff")
				}
				time.Sleep(time.Millisecond)
			}
			r.relayConnected()
			time.Sleep(30 * time.Millisecond)
			if calls.Load() != 2 {
				t.Fatal("relay readiness refreshed already healthy evidence", calls.Load())
			}
		})
	}
	// A local connection does not authorize automatic password/key retries.
	h := config.Host{Name: "auth-peer", RelayID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Via: "relay", Allowed: true}
	cfg := config.Defaults()
	cfg.Hosts = []config.Host{h}
	r := newRemoteObserver(nil)
	var calls atomic.Int32
	r.collect = func(context.Context, config.Host) RemoteObservation {
		calls.Add(1)
		return RemoteObservation{Status: StatusAuth, Error: "explicit approval required"}
	}
	_, stop := remoteEngineFixture(t, cfg, r)
	defer stop()
	deadline := time.Now().Add(time.Second)
	for calls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("initial auth check missing")
		}
		time.Sleep(time.Millisecond)
	}
	r.relayConnected()
	time.Sleep(30 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatal("connection readiness widened authentication permission")
	}
}

func TestRemoteMergeRetainsFailedAndPartialEvidenceButRemovesCompleteAbsence(t *testing.T) {
	h := config.Host{Name: "box", RelayID: "approved-id", Allowed: true, Via: "relay"}
	cfg := config.Defaults()
	cfg.Hosts = []config.Host{h}
	a := New(cfg, nil, t.TempDir(), nil)
	key := agent.SessionKey{Agent: "claude", Session: "session"}
	old := &Inventory{Machines: []*Machine{{Name: h.Name}}, Entries: []Entry{{Machine: h.Name, Session: agent.Summary{Key: key}, Live: agent.LiveInfo{State: agent.Live}}}}
	for _, complete := range []bool{false, true} {
		data, _ := json.Marshal(Observation{Machine: h.Name, InventoryComplete: complete})
		state := RemoteObservation{Binding: h, Phase: "done", Status: StatusOK, Snapshot: observe.Snapshot{ObservedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), Data: data}}
		next := &Inventory{}
		a.MergeRemoteObservations(t.Context(), next, old, []RemoteObservation{state}, nil)
		if complete && len(next.Entries) != 0 {
			t.Fatal("complete absence retained stale row")
		}
		if !complete && (len(next.Entries) != 1 || next.Entries[0].Live.State != agent.Unknown) {
			t.Fatal("partial result erased row or inferred active")
		}
		state.Snapshot.Error = errors.New("offline").Error()
		next = &Inventory{}
		a.MergeRemoteObservations(t.Context(), next, old, []RemoteObservation{state}, nil)
		if len(next.Entries) != 1 || next.Entries[0].Live.State != agent.Unknown {
			t.Fatal("failed result erased or renewed old evidence")
		}
		cfg.Hosts[0].Allowed = false
		a.Cfg = cfg
		next = &Inventory{}
		a.MergeRemoteObservations(t.Context(), next, old, []RemoteObservation{state}, nil)
		if len(next.Entries) != 0 || len(next.Machines) != 0 {
			t.Fatal("revoked machine returned from cache")
		}
		cfg.Hosts[0].Allowed = true
		a.Cfg = cfg
	}
}

func TestRelayedObservationKeepsSourceFreshnessRatherThanReceiptTime(t *testing.T) {
	h := config.Host{Name: "relay-box", RelayID: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Via: "relay", Allowed: true}
	cfg := config.Defaults()
	cfg.Hosts = []config.Host{h}
	r := newRemoteObserver(nil)
	observed, expires := time.Now().Add(-20*time.Second), time.Now().Add(40*time.Second)
	data, _ := json.Marshal(Observation{InventoryComplete: true, Machine: h.Name})
	r.collect = func(context.Context, config.Host) RemoteObservation {
		return RemoteObservation{Status: StatusOK, Snapshot: observe.Snapshot{Epoch: "remote-owner", ObservedAt: observed, ExpiresAt: expires, Data: data}}
	}
	_, stop := remoteEngineFixture(t, cfg, r)
	defer stop()
	deadline := time.Now().Add(time.Second)
	for {
		states := r.latest()
		if len(states) == 1 && states[0].Phase == "done" {
			if !states[0].Snapshot.ObservedAt.Equal(observed) || !states[0].Snapshot.ExpiresAt.Equal(expires) || states[0].Snapshot.Epoch != "remote-owner" {
				t.Fatal("receipt renewed source evidence")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("source observation missing")
		}
		time.Sleep(time.Millisecond)
	}
}
