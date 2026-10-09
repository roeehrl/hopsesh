package gui

import (
	"sync"
	"testing"

	"github.com/roeehrl/hopsesh/internal/agents/all"
)

func TestSessionDiscoveryCachedStartupAndOrderedPublications(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	defer a.Shutdown()
	if s := a.CachedScan(); s.Total != 0 {
		t.Fatal("unexpected cache")
	}
	var mu sync.Mutex
	var publications []int
	a.Emitter = func(name string, data any) {
		if name == DiscoveryEvent {
			d := data.(*ScanDTO)
			mu.Lock()
			publications = append(publications, d.Total)
			mu.Unlock()

		}
	}
	final, err := a.Scan()
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	n := len(publications)
	mu.Unlock()
	if n == 0 || final.Discovering || final.Total == 0 {
		t.Fatalf("no progressive result or completion: %d %+v", n, final)
	}
	b := NewApp(all.Registry())
	defer b.Shutdown()
	s := b.CachedScan()
	if !s.Cached || s.Total != final.Total {
		t.Fatalf("did not restore cache: %+v", s)
	}
	for _, g := range s.Groups {
		for _, e := range g.Entries {
			if e.Live || !e.Cached {
				t.Fatal("cached startup reused presence")
			}
		}
	}
}

func TestSessionDiscoveryPreviewDoesNotReplaceSavedInventory(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	defer a.Shutdown()
	_, err := a.Scan()
	if err != nil {
		t.Fatal(err)
	}
	b := NewApp(all.Registry())
	defer b.Shutdown()
	saved := b.CachedScan()
	var machine, key string
	for _, g := range saved.Groups {
		for _, e := range g.Entries {
			if e.CanPreview {
				machine, key = e.Machine, e.Key
				break
			}
		}
	}
	before := b.inv
	preview, err := b.Preview(machine, key, 4)
	if err != nil || len(preview.Items) == 0 {
		t.Fatalf("saved preview: %+v %v", preview, err)
	}
	if b.inv != before {
		t.Fatal("preview stole discovery ownership")
	}
}
