package gui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/relay"
)

func TestGUIShutdownCancelsPendingRelayApproval(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	a.relayLogin.cancel = cancel
	a.Shutdown()
	if ctx.Err() != context.Canceled {
		t.Fatal("app shutdown left pending browser approval alive")
	}
}

func TestRelaySettingsDoesNotInitializeAndNeverExportsSecrets(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	defer a.Shutdown()
	before := a.RelaySettings()
	if before.Initialized || before.Enrolled || len(before.Peers) != 0 {
		t.Fatal("unconfigured relay claimed configured")
	}
	if _, err := os.Stat(filepath.Join(config.StateDir(), "relay")); !os.IsNotExist(err) {
		t.Fatal("passive settings created relay state", err)
	}
	id, err := a.RelayInitialize()
	if err != nil {
		t.Fatal(err)
	}
	other, err := relay.GenerateIdentity("other-native-endpoint")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(other.Public)
	in := RelayPairInput{Identity: string(b), Fingerprint: other.Public.Fingerprint(), Kind: "device", Name: "paired-machine", Send: true}
	if err = a.RelayPair(in); err != nil {
		t.Fatal(err)
	}
	g, err := (relay.Store{Directory: filepath.Join(config.StateDir(), "relay")}).Grant(t.Context(), other.Public.ID)
	if err != nil || !g.AllowsSend("apply", time.Now()) || g.AllowsSend("export", time.Now()) {
		t.Fatal("sending implied bringing", err)
	}
	settings := a.RelaySettings()
	if !settings.Initialized || settings.Identity.ID != id.ID || len(settings.Peers) != 1 || settings.Peers[0].Name != in.Name {
		t.Fatal("pairing absent from settings")
	}
	raw, _ := json.Marshal(settings)
	if strings.Contains(string(raw), "encryption") || strings.Contains(string(raw), "token") || strings.Contains(string(raw), "AGE-SECRET-KEY") {
		t.Fatal("relay settings exposed secret state")
	}
	if err = a.RelayRevoke(other.Public.ID); err != nil {
		t.Fatal(err)
	}
	if !a.RelaySettings().Peers[0].Revoked {
		t.Fatal("revoke not visible")
	}
	if a.snapshot().Cfg.FindHost(in.Name).Allowed {
		t.Fatal("revoked peer remained enabled in machine navigation")
	}
	b, _ = json.Marshal(id)
	in.Identity = string(b)
	in.Fingerprint = id.Fingerprint()
	in.Name = "self"
	if err = a.RelayPair(in); err == nil {
		t.Fatal("self pairing accepted")
	}
}

func TestRelayPairScopesAndCloudPermissionsAreExplicit(t *testing.T) {
	id, err := relay.GenerateIdentity("peer-endpoint")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(id.Public)
	base := RelayPairInput{Identity: string(b), Fingerprint: id.Public.Fingerprint(), Kind: "device", Name: "peer"}
	g, err := relay.ValidatePair(base)
	if err != nil {
		t.Fatal(err)
	}
	if g.Allows("export", time.Now()) || g.Allows("apply", time.Now()) || g.AllowsSend("apply", time.Now()) {
		t.Fatal("observation approval implied transfers")
	}
	base.Receive = true
	if _, err = relay.ValidatePair(base); err == nil {
		t.Fatal("receiving without repository roots accepted")
	}
	base.Roots = []string{t.TempDir()}
	g, err = relay.ValidatePair(base)
	if err != nil {
		t.Fatal(err)
	}
	if !g.Allows("apply", time.Now()) || g.Allows("export", time.Now()) || g.AllowsSend("apply", time.Now()) {
		t.Fatal("receiving implied sharing/sending")
	}
	base.Receive = false
	base.Export = true
	g, err = relay.ValidatePair(base)
	if err != nil {
		t.Fatal(err)
	}
	if !g.Allows("export", time.Now()) || g.Allows("apply", time.Now()) {
		t.Fatal("sharing implied receiving")
	}
	base.Kind = "cloud-session"
	base.Name = ""
	base.Roots = nil
	base.ExpiresSeconds = 3600
	g, err = relay.ValidatePair(base)
	if err != nil {
		t.Fatal(err)
	}
	if !g.AllowsSend("export", time.Now()) || g.AllowsSend("apply", time.Now()) || g.Allows("apply", time.Now()) {
		t.Fatal("cloud approval widened device permissions")
	}
	base.Send = true
	if _, err = relay.ValidatePair(base); err == nil {
		t.Fatal("cloud device send permission accepted")
	}
}
