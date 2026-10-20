package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/relay"
)

func TestRelayCLIRequiresSeparatePermissionsAndRejectsInvalidCloudBeforeApproval(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOPSESH_CONFIG_DIR", filepath.Join(dir, "config"))
	t.Setenv("HOPSESH_STATE_DIR", filepath.Join(dir, "state"))
	store := relayStore()
	if _, err := store.Identity(t.Context(), "local-native-endpoint"); err != nil {
		t.Fatal(err)
	}
	id, err := relay.GenerateIdentity("other-native-endpoint")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(id.Public)
	public := filepath.Join(dir, "peer.json")
	if err = os.WriteFile(public, b, 0600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) error {
		cmd := relayCmd()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(args)
		return cmd.ExecuteContext(t.Context())
	}
	if err = run("pair", public, "--fingerprint", id.Public.ID, "--root", dir, "--name", "peer"); err != nil {
		t.Fatal(err)
	}
	g, err := store.Grant(t.Context(), id.Public.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"apply", "export", "preview"} {
		if g.Allows(method, time.Now()) || g.AllowsSend(method, time.Now()) {
			t.Fatal("root selection implicitly granted", method)
		}
	}
	if err = run("pair", public, "--fingerprint", id.Public.ID, "--name", "peer", "--send"); err != nil {
		t.Fatal(err)
	}
	g, _ = store.Grant(t.Context(), id.Public.ID)
	if !g.AllowsSend("apply", time.Now()) || g.AllowsSend("export", time.Now()) || g.Allows("apply", time.Now()) {
		t.Fatal("send widened permissions")
	}
	if err = run("pair", public, "--fingerprint", id.Public.ID, "--name", "peer", "--bring"); err != nil {
		t.Fatal(err)
	}
	g, _ = store.Grant(t.Context(), id.Public.ID)
	if !g.AllowsSend("export", time.Now()) || !g.AllowsSend("preview", time.Now()) || g.AllowsSend("apply", time.Now()) {
		t.Fatal("bring widened permissions")
	}
	if err = run("pair", public, "--fingerprint", id.Public.ID, "--name", "cloud-machine", "--kind", "cloud-session"); err == nil {
		t.Fatal("cloud registered as machine")
	}
	g, _ = store.Grant(t.Context(), id.Public.ID)
	if g.Kind != "device" {
		t.Fatal("invalid cloud arguments mutated existing approval")
	}
	cfg, err := config.Load()
	if err != nil || cfg.FindHost("cloud-machine") != nil {
		t.Fatal("invalid cloud arguments mutated machine registry", err)
	}
	if err = run("pair", public, "--fingerprint", id.Public.ID, "--receive", "--root", dir); err == nil {
		t.Fatal("receiving disabled bypassed")
	}
}

func TestRelayCLIPeersDoesNotCreateState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOPSESH_STATE_DIR", filepath.Join(dir, "state"))
	cmd := relayCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"peers"})
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if out.String() != "[]\n" {
		t.Fatal("unexpected uninitialized peer listing")
	}
	if _, err := os.Stat(relayStore().Directory); !os.IsNotExist(err) {
		t.Fatal("listing created relay state", err)
	}
}
