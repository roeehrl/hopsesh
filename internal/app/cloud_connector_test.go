package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/relay"
)

func TestCloudInspectionWithoutRuntimeExplainsHowToStartIt(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOPSESH_CONFIG_DIR", filepath.Join(root, "config"))
	t.Setenv("HOPSESH_STATE_DIR", filepath.Join(root, "state"))
	cfg := config.Defaults()
	cfg.Relay.Enabled = true
	if err := config.Save(&cfg); err != nil {
		t.Fatal(err)
	}
	peer, err := relay.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	store := relay.Store{Directory: filepath.Join(config.StateDir(), "relay")}
	if err := store.Approve(t.Context(), relay.Grant{Peer: peer.Public, Endpoint: peer.Public.Endpoint, Kind: "cloud-session", SendMethods: []string{"observe"}, Expires: time.Now().Add(time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}
	a := &App{Cfg: cfg, StateDir: config.StateDir()}
	if _, err := a.CloudConnectorObservation(t.Context(), peer.Public.ID); !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "hopsesh runtime start") {
		t.Fatalf("missing runtime did not retain its cause and explain recovery: %v", err)
	}
	// A missing runtime must not hide the more important authorization failure.
	if _, err := a.CloudConnectorConversation(t.Context(), peer.Public.ID); err == nil || !strings.Contains(err.Error(), "not approved") {
		t.Fatalf("export permission check was bypassed: %v", err)
	}
}
