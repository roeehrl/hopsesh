package agent

import (
	"encoding/json"
	"testing"
)

func TestMainRoot(t *testing.T) {
	s := Spec{ID: "x", Roots: []Root{{Name: "data"}, {Name: "config"}}}
	if got := s.MainRoot().Name; got != "data" {
		t.Fatalf("MainRoot = %q, want data", got)
	}
	if got := (Spec{ID: "cloud"}).MainRoot().Name; got != "" {
		t.Fatalf("cloud-only MainRoot = %q, want none", got)
	}
	in := Install{Roots: map[string]string{"data": "/d", "config": "/c"}, MainRoot: "data"}
	if in.Main() != "/d" {
		t.Fatalf("Main = %q", in.Main())
	}
	pinned := s.PinMain(in, "/profile")
	if pinned.Main() != "/profile" || len(pinned.Roots) != 1 || pinned.MainRoot != "data" {
		t.Fatalf("PinMain = %+v", pinned)
	}
	if in.Main() != "/d" {
		t.Fatal("PinMain changed the original install")
	}
	// An install from an older peer does not name its main root; a single root is it.
	var old Install
	if err := json.Unmarshal([]byte(`{"agent":"claude","roots":{"home":"/h"},"present":true}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.Main() != "/h" {
		t.Fatalf("old Main = %q", old.Main())
	}
	if (Install{Roots: map[string]string{"a": "/a", "b": "/b"}}).Main() != "" {
		t.Fatal("an unnamed main root among several must not be guessed")
	}
}

// A root is set by its first set variable, else its default for the OS; DefaultInstall
// resolves every root of a multi-root agent the same way and names the first the main one.
func TestRootLocate(t *testing.T) {
	data := Root{Name: "data", Env: []string{"X_DATA", "X_ALT"}, Default: map[string]string{"*": "~/.local/share/x", "darwin": "~/Library/x"}}
	config := Root{Name: "config", Env: []string{"X_CONFIG"}, Default: map[string]string{"*": "{data}/config"}}
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	if got := data.Locate("linux", getenv); got != "~/.local/share/x" {
		t.Fatalf("linux default = %q", got)
	}
	if got := data.Locate("darwin", getenv); got != "~/Library/x" {
		t.Fatalf("darwin default = %q", got)
	}
	env["X_ALT"] = "/alt"
	if got := data.Locate("linux", getenv); got != "/alt" {
		t.Fatalf("second variable = %q", got)
	}
	env["X_DATA"] = "/data"
	if got := data.Locate("linux", getenv); got != "/data" {
		t.Fatalf("first variable = %q", got)
	}
	if got := data.Vars(); len(got) != 2 || got[0] != "X_DATA" {
		t.Fatalf("Vars = %v", got)
	}

	s := Spec{ID: "x", Roots: []Root{data, config}}
	h := newStub("/home/u")
	h.facts.Env = map[string]string{}
	in := DefaultInstall(s, h)
	if in.MainRoot != "data" || in.Main() != "/home/u/.local/share/x" || in.Root("config") != "/home/u/.local/share/x/config" {
		t.Fatalf("defaults: %+v", in)
	}
	h.facts.Env["X_DATA"] = "/d"
	h.facts.Env["X_CONFIG"] = "/c"
	in = DefaultInstall(s, h)
	if in.Main() != "/d" || in.Root("config") != "/c" {
		t.Fatalf("from the environment: %+v", in)
	}
}
