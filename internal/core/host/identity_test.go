package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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
