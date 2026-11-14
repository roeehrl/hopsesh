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
	// An added account of an agent whose account folders hold its roots: the main root is
	// inside the folder. The default account's folder is the main root itself.
	oc := Spec{Roots: []Root{
		{Name: "data", EnvSub: []EnvSub{{Env: "XDG_DATA_HOME", Sub: "opencode"}}, Default: map[string]string{"*": "~/.local/share/opencode"}},
		{Name: "config", Env: []string{"OPENCODE_CONFIG_DIR"}, Default: map[string]string{"*": "~/.config/opencode"}},
	}, Accounts: &ProfileSpec{RootVars: []ProfileVar{{Env: "XDG_DATA_HOME", Sub: "data"}, {Env: "OPENCODE_CONFIG_DIR", Sub: "config"}}}}
	for _, c := range []struct {
		in   Install
		path string
		want string
	}{
		{Install{Profile: &RuntimeProfile{Root: "/p"}}, "/p", "/p/data/opencode"},
		{Install{OS: "windows", Profile: &RuntimeProfile{Root: `C:\p`}}, `C:\p`, `C:\p\data\opencode`},
		{Install{Profile: &RuntimeProfile{Root: `C:\p`}}, `C:\p`, `C:\p\data\opencode`},
		{Install{Profile: &RuntimeProfile{Default: true, Root: "/d"}}, "/d", "/d"},
		{Install{}, "/d", "/d"},
	} {
		if got := oc.PinMain(c.in, c.path).Main(); got != c.want {
			t.Errorf("PinMain(%+v, %s) = %s, want %s", c.in.Profile, c.path, got, c.want)
		}
		if p := c.in.Profile; p != nil && !p.Default {
			if loc := oc.ProfileLocate("data", c.path, map[bool]string{true: "windows", false: "linux"}[c.want[0] != '/'], func(string) string { return "" }); loc != c.want {
				t.Errorf("ProfileLocate = %s, PinMain %s", loc, c.want)
			}
		}
	}
	// A root the account folder's variables do not set is not inside it: the folder stays.
	plain := Spec{Roots: []Root{{Name: "home", Env: []string{"X_HOME"}}}, Accounts: &ProfileSpec{RootVars: []ProfileVar{{Env: "X_CACHE", Sub: "cache"}}}}
	if got := plain.PinMain(Install{Profile: &RuntimeProfile{Root: "/p"}}, "/p").Main(); got != "/p" {
		t.Errorf("PinMain without the main root's variable = %s", got)
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

type osStub struct {
	*stub
	pa Path
}

func (s osStub) Path() Path { return s.pa }

// The OpenCode roots: XDG variables name a folder the root is inside, an Env variable
// names the root itself and wins, and the defaults are the same on every system.
func TestRootEnvSub(t *testing.T) {
	spec := Spec{ID: "oc", Roots: []Root{
		{Name: "data", EnvSub: []EnvSub{{Env: "XDG_DATA_HOME", Sub: "opencode"}}, Default: map[string]string{"*": "~/.local/share/opencode"}},
		{Name: "config", Env: []string{"OPENCODE_CONFIG_DIR"}, EnvSub: []EnvSub{{Env: "XDG_CONFIG_HOME", Sub: "opencode"}}, Default: map[string]string{"*": "~/.config/opencode"}},
		{Name: "state", EnvSub: []EnvSub{{Env: "XDG_STATE_HOME", Sub: "opencode"}}, Default: map[string]string{"*": "~/.local/state/opencode"}},
		{Name: "desktop", Default: map[string]string{"darwin": "~/Library/Application Support/ai.opencode.desktop"}},
	}}
	if got := spec.Roots[1].Vars(); len(got) != 2 || got[0] != "OPENCODE_CONFIG_DIR" || got[1] != "XDG_CONFIG_HOME" {
		t.Fatalf("Vars = %v, want the Env variable first", got)
	}
	for _, c := range []struct {
		goos, home string
		pa         Path
		env        map[string]string
		want       map[string]string
	}{
		{"linux", "/home/u", PosixPath{}, nil, map[string]string{
			"data": "/home/u/.local/share/opencode", "config": "/home/u/.config/opencode", "state": "/home/u/.local/state/opencode"}},
		{"darwin", "/Users/u", PosixPath{}, nil, map[string]string{
			"data": "/Users/u/.local/share/opencode", "config": "/Users/u/.config/opencode", "desktop": "/Users/u/Library/Application Support/ai.opencode.desktop"}},
		{"windows", `C:\Users\u`, WindowsPath{}, nil, map[string]string{
			"data": `C:\Users\u\.local\share\opencode`, "config": `C:\Users\u\.config\opencode`, "state": `C:\Users\u\.local\state\opencode`}},
		{"linux", "/home/u", PosixPath{}, map[string]string{"XDG_DATA_HOME": "/xdg/data", "XDG_CONFIG_HOME": "/xdg/config", "XDG_STATE_HOME": "/xdg/state/"}, map[string]string{
			"data": "/xdg/data/opencode", "config": "/xdg/config/opencode", "state": "/xdg/state/opencode"}},
		{"linux", "/home/u", PosixPath{}, map[string]string{"XDG_CONFIG_HOME": "/xdg/config", "OPENCODE_CONFIG_DIR": "/oc/config"}, map[string]string{
			"config": "/oc/config"}},
		{"windows", `C:\Users\u`, WindowsPath{}, map[string]string{"XDG_DATA_HOME": `D:\xdg`}, map[string]string{
			"data": `D:\xdg\opencode`}},
	} {
		s := newStub(c.home)
		s.facts.OS = c.goos
		s.facts.Env = map[string]string{}
		for k, v := range c.env {
			s.facts.Env[k] = v
		}
		in := DefaultInstall(spec, osStub{s, c.pa})
		if in.Main() != in.Root("data") || in.MainRoot != "data" {
			t.Errorf("%s: main root %q", c.goos, in.MainRoot)
		}
		for name, want := range c.want {
			if got := in.Root(name); got != want {
				t.Errorf("%s %v: %s = %q, want %q", c.goos, c.env, name, got, want)
			}
		}
	}
}
