package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestEndpointIdentitySurvivesAliasesAndRejectsConcurrentInitialization(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	a := &Machine{Name: "address", Local: true, Facts: Facts{Home: home}}
	b := &Machine{Name: "alias", Local: true, Facts: Facts{Home: home}}
	ai, err := a.PrepareIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bi, _ := b.PrepareIdentity(ctx)
	if ai == bi {
		t.Fatal("fixture must reserve independent identities")
	}
	if _, err = os.Stat(filepath.Join(home, ".hopsesh", "endpoint-id")); !os.IsNotExist(err) {
		t.Fatal("planning wrote identity")
	}
	if err = a.CommitIdentity(ctx); err != nil {
		t.Fatal(err)
	}
	if err = b.CommitIdentity(ctx); err == nil {
		t.Fatal("concurrent initialization accepted stale endpoint")
	}
	fresh := &Machine{Name: "another-alias", Local: true, Facts: Facts{Home: home}}
	id, err := fresh.PrepareIdentity(ctx)
	if err != nil || id != ai {
		t.Fatalf("alias changed endpoint: %v %s", err, id)
	}
}

func TestEndpointIdentitySeparatesExplicitProfilesUnderOneAccount(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	a := &Machine{Name: "here", Local: true, Facts: Facts{Home: home, Env: map[string]string{"HOPSESH_CONFIG_DIR": filepath.Join(home, "profile-a")}}}
	b := &Machine{Name: "there", Local: true, Facts: Facts{Home: home, Env: map[string]string{"HOPSESH_CONFIG_DIR": filepath.Join(home, "profile-b")}}}
	ai, err := a.PrepareIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bi, err := b.PrepareIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ai == bi {
		t.Fatal("independent profiles share an endpoint")
	}
	if err = a.CommitIdentity(ctx); err != nil {
		t.Fatal(err)
	}
	if err = b.CommitIdentity(ctx); err != nil {
		t.Fatal(err)
	}
	alias := &Machine{Name: "another alias", Local: true, Facts: Facts{Home: home, Env: a.Facts.Env}}
	got, err := alias.PrepareIdentity(ctx)
	if err != nil || got != ai {
		t.Fatalf("profile alias lost identity: %s %v", got, err)
	}
}

type memKeeper struct{ id string }

func (k *memKeeper) Kept() (string, error) { return k.id, nil }
func (k *memKeeper) Keep(id string) error {
	if k.id != "" && k.id != id {
		return os.ErrExist
	}
	k.id = id
	return nil
}

func TestEndpointIdentityOfMachineWithoutHopseshIsKeptHere(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	keeper := &memKeeper{}
	ssh := func() *Machine {
		m := &Machine{Name: "lurus", Facts: Facts{Home: home}, Keeper: keeper}
		m.fs = localFS{}
		return m
	}
	m := ssh()
	id, err := m.PrepareIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.CommitIdentity(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(home, ".hopsesh", "endpoint-id")); !os.IsNotExist(err) {
		t.Fatal("reaching a machine without hopsesh wrote its identity there")
	}
	if keeper.id != id {
		t.Fatalf("identity not kept here: %q", keeper.id)
	}
	if got, err := ssh().ReadIdentity(ctx); err != nil || got != id {
		t.Fatalf("kept identity not read back: %q %v", got, err)
	}
	if got, err := ssh().PrepareIdentity(ctx); err != nil || got != id {
		t.Fatalf("kept identity not reused: %q %v", got, err)
	}
	with := ssh()
	with.Facts.Binaries = map[string]agent.BinaryFact{Hopsesh.Name: {Path: "/usr/local/bin/hopsesh"}}
	if got, _ := with.ReadIdentity(ctx); got != "" {
		t.Fatal("a machine with hopsesh answered with the identity kept for it here")
	}
}
