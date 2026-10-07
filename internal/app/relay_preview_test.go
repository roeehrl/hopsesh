package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/observe"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestRelayPreviewSharingIndependentOfReceivingAndObservation(t *testing.T) {
	a, home, root := observerFixture(t, claude.New())
	native := filepath.Join(root, "projects", "project", "33333333-3333-4333-8333-333333333333.jsonl")
	b, err := os.ReadFile(native)
	if err != nil {
		t.Fatal(err)
	}
	b = bytes.ReplaceAll(b, []byte("/project"), []byte(filepath.ToSlash(home)))
	if err = os.WriteFile(native, b, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Relay.Enabled = true
	cfg.Peer.Receive = false
	if err = config.Save(&cfg); err != nil {
		t.Fatal(err)
	}
	obs, err := a.ObserveLocal(t.Context())
	if err != nil || len(obs.Entries) != 1 {
		t.Fatal("fixture observation", err)
	}
	id, err := relay.GenerateIdentity("peer-endpoint")
	if err != nil {
		t.Fatal(err)
	}
	g := relay.Grant{Peer: id.Public, Endpoint: id.Public.Endpoint, Kind: "device", Roots: []string{home}, Methods: []string{"observe", "export", "preview"}}
	data, _ := json.Marshal(obs)
	h := a.RelayReceiver(func() observe.Snapshot { return observe.Snapshot{Data: data} })
	params, _ := json.Marshal(relayPreviewRequest{Key: obs.Entries[0].Session.Key, Messages: 2})
	before := treeDigest(t, root)
	out, err := h(t.Context(), g, "preview-operation-123", "preview", params)
	if err != nil {
		t.Fatal("receiving disabled blocked approved sharing", err)
	}
	p := out.(agent.Preview)
	if len(p.Items) != 1 || !strings.Contains(p.Items[0].Text, "Passive session") {
		t.Fatal("missing conversation preview")
	}
	if !equalTree(before, treeDigest(t, root)) {
		t.Fatal("preview changed native history")
	}
	for _, bad := range []relay.Grant{
		{Kind: "device", Methods: []string{"observe", "preview"}},
		{Kind: "device", Methods: g.Methods, Roots: []string{t.TempDir()}},
		{Kind: "cloud-session", Methods: g.Methods, Expires: time.Now().Add(time.Hour).Unix()},
	} {
		if _, err = h(t.Context(), bad, "denied-operation-123", "preview", params); err == nil {
			t.Fatal("preview escaped scope")
		}
	}
	for _, n := range []int{0, 21} {
		params, _ = json.Marshal(relayPreviewRequest{Key: obs.Entries[0].Session.Key, Messages: n})
		if _, err = h(t.Context(), g, "bounded-operation-123", "preview", params); err == nil {
			t.Fatal("unbounded preview accepted")
		}
	}
	params, _ = json.Marshal(relayPreviewRequest{Key: agent.SessionKey{Agent: "claude", Session: "missing"}, Messages: 2})
	if _, err = h(t.Context(), g, "missing-operation-123", "preview", params); err == nil {
		t.Fatal("unknown key accepted")
	}
}

func equalTree(a, b map[string][32]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if got, ok := b[k]; !ok || got != v {
			return false
		}
	}
	return true
}
