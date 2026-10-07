package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/observe"
	"github.com/roeehrl/hopsesh/internal/core/relay"
)

func TestRelayRootsRejectSiblingTraversalAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "approved")
	outside := filepath.Join(root, "outside")
	for _, dir := range []string{allowed, outside} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := withinRelayRoots(filepath.Join(allowed, "new", "checkout"), []string{allowed}); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{outside, filepath.Join(root, "approved-sibling"), "relative", filepath.Join(allowed, "..", "outside")} {
		if err := withinRelayRoots(target, []string{allowed}); err == nil {
			t.Fatal("escaped roots", target)
		}
	}
	link := filepath.Join(allowed, "alias")
	if err := os.Symlink(outside, link); err == nil {
		if err = withinRelayRoots(filepath.Join(link, "new"), []string{allowed}); err == nil {
			t.Fatal("symlink escaped approved root")
		}
	}
}
func TestScopedCloudPeerCannotReadDeviceInventoryOrReceive(t *testing.T) {
	identity, err := relay.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	a := &App{Cfg: config.Defaults()}
	grant := relay.Grant{Peer: identity.Public, Kind: "cloud-session", Methods: []string{"observe", "export"}, Expires: time.Now().Add(time.Hour).Unix()}
	handler := a.RelayReceiver()
	for _, method := range []string{"observe", "plan", "apply", "undo", "settings.set", "exec"} {
		if _, err := handler(context.Background(), grant, "operation-1234567", method, nil); err == nil {
			t.Fatal("cloud peer escaped scope", method)
		}
	}
}

func TestRelayForegroundObservationRefreshesWhileBackgroundReusesEvidence(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOPSESH_CONFIG_DIR", filepath.Join(root, "config"))
	cfg := config.Defaults()
	cfg.Relay.Enabled = true
	if err := config.Save(&cfg); err != nil {
		t.Fatal(err)
	}
	a := &App{Cfg: cfg}
	identity, err := relay.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	grant := relay.Grant{Peer: identity.Public, Kind: "device", Methods: []string{"observe"}}
	data, _ := json.Marshal(Observation{Machine: "source", InventoryComplete: true})
	old := observe.Snapshot{ObservedAt: time.Now().Add(-time.Minute), ExpiresAt: time.Now().Add(time.Minute), Data: data}
	fresh := old
	fresh.ObservedAt = time.Now()
	fresh.ExpiresAt = fresh.ObservedAt.Add(time.Minute)
	calls := 0
	handler := a.relayReceiver(func() observe.Snapshot { return old }, func(context.Context) (observe.Snapshot, error) { calls++; return fresh, nil })
	for _, explicit := range []bool{false, true, false} {
		params, _ := json.Marshal(map[string]bool{"refresh": explicit})
		value, err := handler(t.Context(), grant, "observation-operation-123", "observe", params)
		if err != nil {
			t.Fatal(err)
		}
		snapshot := value.(observe.Snapshot)
		want := old.ObservedAt
		if explicit {
			want = fresh.ObservedAt
		}
		if !snapshot.ObservedAt.Equal(want) {
			t.Fatal("evidence time changed without an actual refresh")
		}
	}
	if calls != 1 {
		t.Fatal("background read caused collection", calls)
	}
}
