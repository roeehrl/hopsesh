package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/internal/testkit/fakecloud"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

type pendingMovementCloud struct {
	agent.Module
	gate    <-chan struct{}
	started chan struct{}
}

func (m pendingMovementCloud) ListCloud(ctx context.Context, _ agent.Host, _ agent.Install, _ agent.CloudQuery) (agent.CloudListing, error) {
	close(m.started)
	select {
	case <-m.gate:
		return agent.CloudListing{}, nil
	case <-ctx.Done():
		return agent.CloudListing{}, ctx.Err()
	}
}

// A -> B -> A -> B changes both existing native identities. A completed local
// source must publish that movement before a slower cloud finishes, even when
// the native listing was cached from the two-hop state.
func TestScanPublishesLocalThirdHopWhileCloudPending(t *testing.T) {
	a := catalogApp(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	initial := a.Scan(ctx, ScanOptions{SkipGit: true})
	defer initial.Close()
	var pair []Entry
	for _, id := range []agent.ID{"claude", "codex"} {
		for _, e := range initial.Entries {
			if e.Agent == id {
				pair = append(pair, e)
				break
			}
		}
	}
	if len(pair) != 2 {
		t.Fatalf("fixture requires both agents, got %d", len(pair))
	}
	g := lineage.New("progress-" + pair[0].Session.Path)
	var ids []lineage.ReplicaID
	for _, e := range pair {
		m := initial.Machine(e.Machine)
		in, _ := m.InstallProfile(e.Agent, e.Session.Key.Profile)
		ids = append(ids, g.Upsert(lineage.Replica{Key: e.Session.Key, Location: e.Machine, Endpoint: m.host.Facts.Endpoint, Binding: in.BindingID()}))
	}
	write := func() {
		t.Helper()
		for _, e := range pair {
			if err := os.WriteFile(lineage.PathFor(e.Session.Path), g.Encode(), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	for i := range 2 {
		if err := g.AppendHop(lineage.Hop{ID: string(rune('1' + i)), Kind: lineage.HopContinue, From: ids[i%2], To: ids[(i+1)%2], Notify: true}); err != nil {
			t.Fatal(err)
		}
	}
	write()
	a.Scan(ctx, ScanOptions{SkipGit: true}).Close() // warm sidecar, movement and catalog caches
	if err := g.AppendHop(lineage.Hop{ID: "3", Kind: lineage.HopContinue, From: ids[0], To: ids[1], Notify: true}); err != nil {
		t.Fatal(err)
	}
	write()
	bin := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		b, err := os.ReadFile(self)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, "fakecloud.exe"), b, 0700); err != nil {
			t.Fatal(err)
		}
	} else if err := os.Symlink(self, filepath.Join(bin, "fakecloud")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	gate := make(chan struct{})
	started := make(chan struct{})
	defer close(gate)
	mods := append(a.Reg.All(), pendingMovementCloud{Module: fakecloud.Stub(), gate: gate, started: started})
	a.Reg, err = registry.New(mods...)
	if err != nil {
		t.Fatal(err)
	}
	a.Cfg.SetCloudAllowed(fakecloud.FakeCloud, true)
	ready := make(chan ScanUpdate, 1)
	done := make(chan *Inventory, 1)
	go func() {
		done <- a.Scan(ctx, ScanOptions{SkipGit: true, Progress: func(u ScanUpdate) {
			if u.Complete && u.Machine != nil && u.Machine.Local {
				ready <- u
			}
		}})
	}()
	defer func() { cancel(); (<-done).Close() }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("fixture cloud never started its pending listing")
	}
	select {
	case u := <-ready:
		for i, source := range pair {
			found := false
			for _, e := range u.Entries {
				if e.Session.Key != source.Session.Key {
					continue
				}
				found = true
				if e.Lineage == nil || len(e.Lineage.Hops) != 3 || e.Cached {
					t.Fatalf("local publication replayed the two-hop state: %+v", e)
				}
				if i == 0 && (e.Departure == nil || e.Departure.Operation != "3") || i == 1 && (e.Departure != nil || e.Arrival == nil || e.Arrival.Operation != "3") {
					t.Fatalf("local movement not ready before cloud completion: departure=%+v arrival=%+v", e.Departure, e.Arrival)
				}
			}
			if !found {
				t.Fatal("local publication lost an existing native identity")
			}
		}
	case <-ctx.Done():
		t.Fatal("local movement waited for the pending cloud")
	}
	select {
	case inv := <-done:
		done <- inv // leave cleanup ownership with the defer
		t.Fatal("fixture cloud did not remain pending")
	default:
	}
}
