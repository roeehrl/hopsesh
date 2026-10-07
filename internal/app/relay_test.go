package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
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
