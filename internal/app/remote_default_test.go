package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/peer"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// The Remote Control default goes in the same folders as the protection hooks (the
// default and the registered ones here, never another machine's), changing that one key.
func TestRemoteDefaultsAllLocalFolders(t *testing.T) {
	a, m, root := movementHookFixture(t)
	defer m.Close()
	other := filepath.Join(filepath.Dir(root), "other")
	remote := filepath.Join(filepath.Dir(root), "remote")
	for _, r := range []string{other, remote} {
		if err := os.MkdirAll(r, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(other, "settings.json"), []byte(`{"model":"opus"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []agent.RuntimeProfile{
		{ID: strings.Repeat("2", 32), Agent: "claude", Endpoint: m.Facts.Endpoint, Machine: m.Name, Root: other, Name: "work"},
		{ID: strings.Repeat("3", 32), Agent: "claude", Endpoint: strings.Repeat("c", 64), Machine: m.Name, Root: remote, Name: "remote"},
	} {
		if _, err := a.accountStore().Register(p); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	r, err := a.remoteDefaultsOn(ctx, m, nil)
	if err != nil || len(r.Folders) != 2 || r.Folders[0].On || r.Folders[1].On || !r.Local {
		t.Fatalf("read: %+v %v", r, err)
	}
	on := true
	if r, err = a.remoteDefaultsOn(ctx, m, &on); err != nil || !r.Folders[0].On || !r.Folders[1].On {
		t.Fatalf("on: %+v %v", r, err)
	}
	b, _ := os.ReadFile(filepath.Join(other, "settings.json"))
	if !strings.Contains(string(b), `"remoteControlAtStartup": true`) || !strings.Contains(string(b), `"model": "opus"`) {
		t.Fatalf("other keys or the setting: %s", b)
	}
	if _, err := os.Stat(filepath.Join(remote, "settings.json")); !os.IsNotExist(err) {
		t.Fatal("touched another machine's folder")
	}
	off := false
	if r, err = a.remoteDefaultsOn(ctx, m, &off); err != nil || r.Folders[0].On || r.Folders[1].On {
		t.Fatalf("off: %+v %v", r, err)
	}
	if b, _ := os.ReadFile(filepath.Join(other, "settings.json")); strings.Contains(string(b), "remoteControlAtStartup") {
		t.Fatalf("off left the key: %s", b)
	}
}

// Only hopsesh in the login session answers it, and a hopsesh that does says so in hello,
// so an older one is asked to update instead of failing on an unknown request.
func TestRemoteDefaultIsALoginRequest(t *testing.T) {
	if !peer.LoginMethod(peer.MethodRemoteDefault) {
		t.Fatal("not a login request")
	}
}
