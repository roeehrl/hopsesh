package app

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/internal/testkit"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func catalogApp(t *testing.T) *App {
	t.Helper()
	return catalogAppAt(t, t.TempDir())
}

func catalogAppAt(t *testing.T, home string) *App {
	t.Helper()
	for k, v := range testkit.Env(home) {
		t.Setenv(k, v)
	}
	if err := testkit.DemoHome(home); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a := New(cfg, all.Registry(), config.StateDir(), nil)
	t.Cleanup(func() { a.Catalog.Close() })
	return a
}

type delayedListing struct {
	agent.Module
	gate <-chan struct{}
}

func (m delayedListing) List(ctx context.Context, h agent.Host, in agent.Install) (agent.Listing, error) {
	select {
	case <-ctx.Done():
		return agent.Listing{}, ctx.Err()
	case <-m.gate:
		return m.Module.List(ctx, h, in)
	}
}

func TestSessionDiscoveryPublishesBeforeSlowAgentCompletes(t *testing.T) {
	a := catalogApp(t)
	gate := make(chan struct{})
	reg, err := registry.New(claude.New(), delayedListing{codex.New(), gate})
	if err != nil {
		t.Fatal(err)
	}
	a.Reg = reg
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	found := make(chan ScanUpdate, 10)
	done := make(chan *Inventory, 1)
	go func() {
		done <- a.Scan(ctx, ScanOptions{SkipGit: true, Progress: func(u ScanUpdate) {
			if len(u.Entries) > 0 {
				select {
				case found <- u:
				default:
				}
			}
		}})
	}()
	select {
	case u := <-found:
		if u.Entries[0].Agent != "claude" || !u.Entries[0].Cached {
			t.Fatalf("unsafe preliminary row: %+v", u)
		}
	case <-ctx.Done():
		t.Fatal("local rows were withheld")
	}
	select {
	case <-done:
		t.Fatal("slow agent was not still running")
	default:
	}
	close(gate)
	inv := <-done
	defer inv.Close()
	if len(inv.Entries) < 4 {
		t.Fatalf("final inventory lost sessions: %d", len(inv.Entries))
	}
}

func TestSessionDiscoveryDefaultRegistrationDoesNotDuplicateObservedFiles(t *testing.T) {
	// Match normal home paths on macOS too: /var's symlink would give the
	// observer and registered root different spellings and hide this regression.
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := catalogAppAt(t, home)
	ctx := context.Background()
	observed, err := a.ObserveLocal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(observed.Entries) == 0 || observed.Entries[0].Session.Key.Profile != "" {
		t.Fatal("fixture must begin with unregistered passive observations")
	}
	current := a.ObservationInventory(ctx, observed)
	defer current.Close()
	var mu sync.Mutex
	var duplicated string
	final := a.Scan(ctx, ScanOptions{SkipGit: true, Progress: func(u ScanUpdate) {
		mu.Lock()
		defer mu.Unlock()
		current = MergeDiscovery(current, u)
		seen := map[string]string{}
		for _, e := range current.Entries {
			if e.Session.Path == "" {
				continue
			}
			file := e.Machine + "/" + string(e.Agent) + "/" + e.Session.Path
			if previous, ok := seen[file]; ok && duplicated == "" {
				duplicated = previous + " and " + e.Session.Key.String()
			}
			seen[file] = e.Session.Key.String()
		}
	}})
	defer final.Close()
	registered := false
	for _, e := range final.Entries {
		registered = registered || e.Session.Key.Profile != ""
	}
	if !registered {
		t.Fatal("fixture did not register accounts during discovery")
	}
	if duplicated != "" {
		t.Fatalf("registration displayed the same native file twice: %s", duplicated)
	}
}

func TestSessionDiscoveryDefaultRebindingKeepsScopeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Inventory, *[]Entry)
		want   int
	}{
		{"same local native file", nil, 1},
		{"registered copy already present", func(inv *Inventory, in *[]Entry) { inv.Entries = append(inv.Entries, (*in)[0]) }, 1},
		{"remote", func(inv *Inventory, _ *[]Entry) { inv.Machines[0].Local = false }, 2},
		{"old cloud", func(inv *Inventory, _ *[]Entry) { inv.Entries[0].Location = agent.CloudLocation("local") }, 2},
		{"incoming cloud", func(_ *Inventory, in *[]Entry) { (*in)[0].Location = agent.CloudLocation("local") }, 2},
		{"other machine", func(_ *Inventory, in *[]Entry) { (*in)[0].Machine = "other" }, 2},
		{"explicit old profile", func(inv *Inventory, _ *[]Entry) { inv.Entries[0].Session.Key.Profile = "other" }, 2},
		{"nondefault", func(_ *Inventory, in *[]Entry) { (*in)[0].Profile.Default = false }, 2},
		{"missing endpoint", func(_ *Inventory, in *[]Entry) { (*in)[0].Profile.Endpoint = "" }, 2},
		{"missing root", func(_ *Inventory, in *[]Entry) { (*in)[0].Profile.Root = "" }, 2},
		{"different native file", func(_ *Inventory, in *[]Entry) { (*in)[0].Session.Path += ".fork" }, 2},
		{"different session", func(_ *Inventory, in *[]Entry) { (*in)[0].Session.Key.Session = "fork" }, 2},
		{"profile agent mismatch", func(_ *Inventory, in *[]Entry) { (*in)[0].Profile.Agent = "codex" }, 2},
		{"ambiguous nondefault", func(_ *Inventory, in *[]Entry) {
			e := (*in)[0]
			p := *e.Profile
			p.ID, p.Default = "other", false
			e.Profile, e.Session.Key.Profile = &p, p.ID
			*in = append(*in, e)
		}, 3},
		{"ambiguous defaults", func(_ *Inventory, in *[]Entry) {
			e := (*in)[0]
			p := *e.Profile
			p.ID = "other"
			e.Profile, e.Session.Key.Profile = &p, p.ID
			*in = append(*in, e)
		}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			git := &repos.GitState{Identity: "github.com/example/demo", IsRepo: true}
			old := &Inventory{Machines: []*Machine{{Name: "local", Local: true}}, Entries: []Entry{{Machine: "local", Agent: "claude", Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Session: "one"}, Path: "/native/session"}, Git: git}}}
			e := old.Entries[0]
			e.Session.Key.Profile = "default"
			e.Profile = &agent.RuntimeProfile{ID: "default", Agent: "claude", Endpoint: "endpoint", Root: "/native", Default: true}
			e.Git = nil
			incoming := []Entry{e}
			if tc.change != nil {
				tc.change(old, &incoming)
			}
			oldKey := old.Entries[0].Session.Key
			next := MergeDiscovery(old, ScanUpdate{Entries: incoming})
			if len(next.Entries) != tc.want {
				t.Fatalf("got %d entries, want %d: %+v", len(next.Entries), tc.want, next.Entries)
			}
			if old.Entries[0].Session.Key != oldKey || old.Entries[0].Git != git {
				t.Fatal("mutated previously published inventory")
			}
			if tc.name == "same local native file" && (next.Entries[0].Session.Key != incoming[0].Session.Key || next.Entries[0].Git != git || !next.Entries[0].Cached) {
				t.Fatal("registration lost provisional repository metadata or kept the old key")
			}
		})
	}
}

func TestSessionCatalogRestoresWithoutPresenceAndScopesSettings(t *testing.T) {
	a := catalogApp(t)
	inv := a.Scan(context.Background(), ScanOptions{SkipGit: true})
	defer inv.Close()
	b := New(a.Cfg, a.Reg, a.StateDir, nil)
	defer b.Catalog.Close()
	saved := b.CachedInventory()
	if len(saved.Entries) != len(inv.Entries) {
		t.Fatalf("restart catalog: %d / %d", len(saved.Entries), len(inv.Entries))
	}
	for _, e := range saved.Entries {
		if !e.Cached || e.Live.State != agent.Unknown || len(e.Returns) > 0 {
			t.Fatal("cached data claimed live evidence")
		}
	}
	for _, m := range saved.Machines {
		if m.Host() != nil {
			t.Fatal("a saved row carried a transport")
		}
	}
	b.Cfg.Agents = map[string]config.Agent{}
	b.Cfg.Agents["claude"] = config.Agent{Disabled: true}
	if len(b.CachedInventory().Entries) != 0 {
		t.Fatal("disabled scope exposed old cache")
	}
}

func TestSessionDiscoveryRetainsFailedScopeAndDeletesOnlyComplete(t *testing.T) {
	old := &Inventory{Machines: []*Machine{{Name: "remote", Status: StatusOK}}, Entries: []Entry{{Machine: "remote", Cached: true, Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Session: "one"}}}}}
	for _, u := range []ScanUpdate{{Machine: &Machine{Name: "remote", Status: StatusUnreachable}, Complete: true}, {Machine: &Machine{Name: "remote", Status: StatusOK}}, {Machine: &Machine{Name: "remote", Status: StatusOK, Agents: []AgentState{{Error: "directory unreadable"}}}, Complete: true}} {
		if len(MergeDiscovery(old, u).Entries) != 1 {
			t.Fatal("partial/failure deleted saved session")
		}
	}
	if len(MergeDiscovery(old, ScanUpdate{Machine: &Machine{Name: "remote", Status: StatusOK}, Complete: true}).Entries) != 0 {
		t.Fatal("successful empty enumeration did not remove deleted session")
	}
}

func TestSessionCatalogSelectionRechecksDeletionAndNarrowRefresh(t *testing.T) {
	a := catalogApp(t)
	inv := a.Scan(context.Background(), ScanOptions{SkipGit: true})
	inv.Close()
	saved := a.CachedInventory()
	e := saved.Entries[0]
	if err := os.Remove(filepath.FromSlash(e.Session.Path)); err != nil {
		t.Fatal(err)
	}
	if fresh, _, err := a.FreshSelection(context.Background(), e); err == nil {
		fresh.Close()
		t.Fatal("action accepted deleted cached session")
	}
	a.Scan(context.Background(), ScanOptions{Hosts: []string{LocalName()}, SkipGit: true}).Close()
	for _, x := range a.CachedInventory().Entries {
		if x.Session.Key == e.Session.Key {
			t.Fatal("deleted row resurrected in catalog")
		}
	}
}

func TestSessionDiscoverySharesCollectorAndPreservesIndependentSources(t *testing.T) {
	a := catalogApp(t)
	// Establish account bindings before comparing collectors in the same namespace.
	// First-time root registration deliberately changes the catalog scope.
	a.Scan(context.Background(), ScanOptions{SkipGit: true}).Close()
	gate := make(chan struct{})
	reg, err := registry.New(claude.New(), delayedListing{codex.New(), gate})
	if err != nil {
		t.Fatal(err)
	}
	a.Reg = reg
	b := New(a.Cfg, a.Reg, a.StateDir, nil)
	defer b.Catalog.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	found := make(chan struct{}, 1)
	done := make(chan *Inventory, 1)
	go func() {
		done <- a.Scan(ctx, ScanOptions{SkipGit: true, Progress: func(u ScanUpdate) {
			if len(u.Entries) > 0 {
				select {
				case found <- struct{}{}:
				default:
				}
			}
		}})
	}()
	<-found
	following := make(chan *Inventory, 1)
	seen := make(chan struct{}, 1)
	go func() {
		following <- b.Scan(ctx, ScanOptions{SkipGit: true, Progress: func(u ScanUpdate) {
			if len(u.Entries) > 0 {
				select {
				case seen <- struct{}{}:
				default:
				}
			}
		}})
	}()
	select {
	case <-seen:
	case <-ctx.Done():
		t.Fatal("second front end did not follow discovered rows")
	}
	close(gate)
	(<-done).Close()
	follower := <-following
	defer follower.Close()
	if len(follower.Entries) < 4 {
		t.Fatalf("shared collector lost results: %d", len(follower.Entries))
	}
	for _, e := range follower.Entries {
		if !e.Cached || e.Live.State != agent.Unknown {
			t.Fatal("shared catalog invented live evidence")
		}
	}
	now := time.Now()
	one := &Inventory{Machines: []*Machine{{Name: "one", Status: StatusOK}}, Entries: []Entry{{Machine: "one", Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Session: "first"}}}}}
	two := &Inventory{Machines: []*Machine{{Name: "two", Status: StatusOK}}, Entries: []Entry{{Machine: "two", Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Session: "second"}}}}}
	a.saveCatalog(one, now)
	b.saveCatalog(two, now.Add(time.Second))
	a.saveCatalog(&Inventory{Machines: one.Machines}, now.Add(-time.Second))
	saved := a.CachedInventory()
	if saved.Machine("one") == nil || saved.Machine("two") == nil {
		t.Fatal("independent source merge lost a machine")
	}
	first := false
	for _, e := range saved.Entries {
		first = first || e.Machine == "one"
	}
	if !first {
		t.Fatal("late source enumeration removed newer row")
	}
}

func TestProgressiveRefreshRetainsRepositoryUntilEnrichmentCompletes(t *testing.T) {
	key := agent.SessionKey{Agent: "claude", Session: "kept"}
	git := &repos.GitState{Identity: "github.com/example/demo", IsRepo: true}
	old := &Inventory{Entries: []Entry{{Machine: "local", Session: agent.Summary{Key: key}, Git: git}}}
	early := Entry{Machine: "local", Session: agent.Summary{Key: key, Title: "New title"}}
	next := MergeDiscovery(old, ScanUpdate{Entries: []Entry{early}, Listed: true})
	if next.Entries[0].Git != git || !next.Entries[0].Cached || next.Entries[0].Session.Title != "New title" {
		t.Fatal("preliminary refresh erased repository or ignored fresh summary")
	}
	final := MergeDiscovery(next, ScanUpdate{Entries: []Entry{early}, Complete: true})
	if final.Entries[0].Git != nil || final.Entries[0].Cached {
		t.Fatal("completion failed to clear an obsolete repository")
	}
	if old.Entries[0].Git != git {
		t.Fatal("published old snapshot was mutated")
	}
}

func TestDiscoveryLeaseSeparatesSharedSourceEvidence(t *testing.T) {
	a := catalogApp(t)
	for _, test := range []struct {
		name          string
		first, second ScanOptions
	}{
		{"local snapshots", ScanOptions{LocalSnapshot: &Observation{Machine: "first"}}, ScanOptions{LocalSnapshot: &Observation{Machine: "second"}}},
		{"shared vs direct", ScanOptions{SharedRemotes: true}, ScanOptions{}},
		{"remote snapshots", ScanOptions{SharedRemotes: true, RemoteSnapshots: []RemoteObservation{{Phase: "queued"}}}, ScanOptions{SharedRemotes: true, RemoteSnapshots: []RemoteObservation{{Phase: "done"}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, release := a.discoveryLease(t.Context(), test.first)
			defer release()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			followed, stop := a.discoveryLease(ctx, test.second)
			defer stop()
			if ctx.Err() != nil || followed != nil {
				t.Fatal("distinct source evidence waited for or reused another collector")
			}
		})
	}
}
