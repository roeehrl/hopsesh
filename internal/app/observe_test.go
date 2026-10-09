package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/profiles"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func observerFixture(t *testing.T, mod agent.Module) (*App, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOPSESH_CONFIG_DIR", filepath.Join(home, "config"))
	t.Setenv("HOPSESH_STATE_DIR", filepath.Join(home, "state"))
	t.Setenv("HOPSESH_MACHINE", "here")
	root := filepath.Join(home, "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	writeObservedSession(t, root)
	r, err := registry.New(mod)
	if err != nil {
		t.Fatal(err)
	}
	return New(config.Defaults(), r, config.StateDir(), nil), home, root
}

func writeObservedSession(t *testing.T, root string) {
	t.Helper()
	p := filepath.Join(root, "projects", "project", "33333333-3333-4333-8333-333333333333.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"type":"user","uuid":"u1","sessionId":"33333333-3333-4333-8333-333333333333","cwd":"/project","timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","content":"Passive session"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

func treeDigest(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	if err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			out[p] = [32]byte{}
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[p] = sha256.Sum256(b)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestObserveUninitializedInstallationDoesNotWrite(t *testing.T) {
	a, home, _ := observerFixture(t, claude.New())
	before := treeDigest(t, home)
	for range 3 {
		o, err := a.ObserveLocal(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !o.InventoryComplete || o.Endpoint != "" || len(o.Entries) != 1 || o.Entries[0].Profile != nil {
			t.Fatalf("unexpected initialized identity: %+v", o)
		}
	}
	if !reflect.DeepEqual(before, treeDigest(t, home)) {
		t.Fatal("passive collection changed the installation")
	}
}

type partialInventory struct{ agent.Module }

type registrationDuringListing struct {
	agent.Module
	change func()
}

func (m *registrationDuringListing) List(ctx context.Context, h agent.Host, in agent.Install) (agent.Listing, error) {
	out, err := m.Module.List(ctx, h, in)
	if m.change != nil {
		change := m.change
		m.change = nil
		change()
	}
	return out, err
}

func TestObserveRejectsRegistrationChangedDuringCollection(t *testing.T) {
	mod := &registrationDuringListing{Module: claude.New()}
	a, _, root := observerFixture(t, mod)
	mod.change = func() {
		endpoint := strings.Repeat("a", 64)
		if err := os.MkdirAll(config.Dir(), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(config.Dir(), "endpoint-id"), []byte(endpoint), 0600); err != nil {
			t.Fatal(err)
		}
		canonical, err := filepath.EvalSymlinks(root)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = a.accountStore().Register(agent.RuntimeProfile{ID: "registered", Agent: "claude", Endpoint: endpoint, Root: canonical, Name: "Personal"}); err != nil {
			t.Fatal(err)
		}
	}
	out, err := a.ObserveLocal(t.Context())
	if err != nil || !out.InventoryComplete || len(out.Entries) != 1 || out.Entries[0].Session.Key.Profile != "registered" {
		t.Fatalf("collection did not retry the changed registration: %+v, %v", out, err)
	}
}

func (m partialInventory) List(ctx context.Context, h agent.Host, in agent.Install) (agent.Listing, error) {
	ls, err := m.Module.List(ctx, h, in)
	ls.Errors = append(ls.Errors, agent.SessionError{Path: "unreadable-session.jsonl", Err: errors.New("permission denied")})
	return ls, err
}

func TestObservePartialInventoryCannotProveAbsence(t *testing.T) {
	a, _, _ := observerFixture(t, partialInventory{claude.New()})
	o, err := a.ObserveLocal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if o.InventoryComplete || len(o.Entries) != 1 || len(o.Problems) != 1 {
		t.Fatalf("partial listing claimed complete coverage: %+v", o)
	}
}

func TestObserveCanceledCollectionCannotProveAbsence(t *testing.T) {
	a, _, _ := observerFixture(t, claude.New())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	o, err := a.ObserveLocal(ctx)
	if !errors.Is(err, context.Canceled) || o.InventoryComplete {
		t.Fatalf("canceled listing claimed complete coverage: %+v, %v", o, err)
	}
}

func TestObserveProfilesPreservesBindingsAndPendingMovement(t *testing.T) {
	a, home, root := observerFixture(t, claude.New())
	endpoint := strings.Repeat("a", 64)
	if err := os.MkdirAll(config.Dir(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.Dir(), "endpoint-id"), []byte(endpoint), 0600); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(home, "second")
	writeObservedSession(t, second)
	for i, r := range []string{root, second} {
		canonical, err := filepath.EvalSymlinks(r)
		if err != nil {
			t.Fatal(err)
		}
		_, err = (profiles.Store{Dir: a.StateDir}).Register(agent.RuntimeProfile{ID: []string{"first", "second"}[i], Agent: "claude", Endpoint: endpoint, Root: canonical, Name: "Personal", Binding: "cached-binding", Account: &agent.Account{LoggedIn: true, Observation: "cached-login"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Pending marks and waiting imports remain byte-for-byte untouched, including
	// malformed state: observation is not the place to repair or adopt them.
	for _, name := range []string{"pending-marks.jsonl", "waiting-import.json"} {
		if err := os.WriteFile(filepath.Join(a.StateDir, name), []byte("pending operation\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	before := treeDigest(t, home)
	o, err := a.ObserveLocal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Entries) != 2 || o.Entries[0].Session.Key.Profile == o.Entries[1].Session.Key.Profile {
		t.Fatalf("same native ID merged across profiles: %+v", o.Entries)
	}
	for _, e := range o.Entries {
		if e.Profile.Binding != "cached-binding" || e.Profile.Generation != 1 {
			t.Fatal("observer refreshed identity metadata")
		}
	}
	if !reflect.DeepEqual(before, treeDigest(t, home)) {
		t.Fatal("observer modified registrations, pending movement or transcripts")
	}
}

type failedPresence struct{ agent.Module }

func (failedPresence) Live(context.Context, agent.Host, agent.Install, []agent.SessionID) (map[agent.SessionID]agent.LiveInfo, error) {
	return map[agent.SessionID]agent.LiveInfo{"33333333-3333-4333-8333-333333333333": {State: agent.Ended}}, errors.New("registry unavailable")
}

func TestObservePresenceErrorCannotBecomeEnded(t *testing.T) {
	a, _, _ := observerFixture(t, failedPresence{claude.New()})
	o, err := a.ObserveLocal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Entries) != 1 || o.Entries[0].Live.State != agent.Unknown || len(o.Problems) != 1 {
		t.Fatalf("failed presence was not explicit unknown: %+v", o)
	}
}

type actionAttempt struct {
	agent.Module
	t *testing.T
}

func (m actionAttempt) List(ctx context.Context, h agent.Host, in agent.Install) (agent.Listing, error) {
	for _, err := range []error{
		h.FS().WriteFile(h.Path().Join(in.Root("home"), "unexpected"), []byte("write"), 0600),
		h.Procs().Terminate(ctx, os.Getpid()),
	} {
		if !errors.Is(err, agent.ErrDenied) {
			m.t.Fatalf("observation allowed action: %v", err)
		}
	}
	if _, err := h.Exec().Run(ctx, []string{"claude", "auth", "status"}, agent.RunOptions{}); !errors.Is(err, agent.ErrDenied) {
		m.t.Fatalf("observer allowed execution: %v", err)
	}
	return m.Module.List(ctx, h, in)
}

func TestObserveDeniesModuleSideEffects(t *testing.T) {
	a, home, _ := observerFixture(t, actionAttempt{claude.New(), t})
	before := treeDigest(t, home)
	if _, err := a.ObserveLocal(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, treeDigest(t, home)) {
		t.Fatal("module wrote through observer host")
	}
}
