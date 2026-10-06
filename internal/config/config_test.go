package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadSaveAndRefuseOldFormat(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOPSESH_CONFIG_DIR", dir)
	c, err := Load()
	if err != nil || c.Schema != Schema || c.Layout != "flat" {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	c.UpsertHost(Host{Name: "studio", Destination: "me@studio", Allowed: true})
	c.Agents = map[string]Agent{"codex": {Disabled: true}}
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	back, err := Load()
	if err != nil || back.FindHost("studio") == nil || back.AgentEnabled("codex") || !back.AgentEnabled("claude") {
		t.Fatalf("round trip: %+v %v", back, err)
	}
	old := "repos_dir = \"/x\"\nlive_policy = \"handoff\"\n[[hosts]]\nname = \"a\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); !errors.Is(err, ErrOldConfig) {
		t.Fatalf("an older config must be refused, got %v", err)
	}
	old, err = SetAside()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("the old file must be kept: %v", err)
	}
	if c, err := Load(); err != nil || len(c.Hosts) != 0 {
		t.Fatalf("after setting it aside hopsesh starts fresh: %+v %v", c, err)
	}
	// The format before clouds (schema 3) is refused like any other.
	three := "schema = 3\nrepos_dir = \"/x\"\nlayout = \"flat\"\n[[hosts]]\nname = \"a\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(three), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); !errors.Is(err, ErrOldConfig) || errors.Is(err, ErrNewConfig) {
		t.Fatalf("a schema 3 file must be refused as older, got %v", err)
	}
}

// A file a newer hopsesh wrote (after a downgrade) is refused as newer, never as older: the
// way out is updating hopsesh, and setting it aside comes second.
func TestRefuseNewerFormat(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOPSESH_CONFIG_DIR", dir)
	newer := fmt.Sprintf("schema = %d\nrepos_dir = \"/x\"\nlayout = \"flat\"\n[future]\nthing = true\n", Schema+1)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(newer), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if !errors.Is(err, ErrNewConfig) || errors.Is(err, ErrOldConfig) {
		t.Fatalf("a newer file must be refused as newer, got %v", err)
	}
	want := fmt.Sprintf("the configuration was written by a newer hopsesh (schema %d); update hopsesh, or move the file aside to start fresh", Schema+1)
	if !strings.HasPrefix(err.Error(), want) || !strings.Contains(err.Error(), filepath.Join(dir, "config.toml")) {
		t.Fatalf("message: %q", err)
	}
	if c.ReposDir == "/x" {
		t.Fatal("nothing of a newer file is read")
	}
	old, err := SetAside()
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(old); string(b) != newer {
		t.Fatal("set aside, the newer file is kept as it was")
	}
}

func TestClouds(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOPSESH_CONFIG_DIR", dir)
	file := `schema = 4

[clouds.claude-cloud]
allowed = true
code = "bundle"
untracked = ["docs/plan.md"]
rename_vendor_branches = false

[clouds.codex-cloud]
allowed = false
[clouds.codex-cloud.environments]
"github.com/acme/api" = "env_1"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.CloudAllowed("claude-cloud") || c.CloudAllowed("codex-cloud") || c.CloudAllowed("jules") {
		t.Fatalf("consent: %+v", c.Clouds)
	}
	cl := c.CloudSettings("claude-cloud")
	if cl.Code != CloudCodeBundle || cl.BranchPrefix != DefaultBranchPrefix || cl.DeleteBranch != DeleteAfterMerge ||
		*cl.RenameVendorBranches || len(cl.Untracked) != 1 {
		t.Fatalf("claude-cloud settings: %+v", cl)
	}
	if d := c.CloudSettings("jules"); d.Code != CloudCodeBranch || !*d.RenameVendorBranches || d.Allowed {
		t.Fatalf("defaults for a cloud never set up: %+v", d)
	}
	if c.CloudSettings("codex-cloud").Environments["github.com/acme/api"] != "env_1" {
		t.Fatalf("environments: %+v", c.Clouds["codex-cloud"])
	}
	c.SetCloudAllowed("codex-cloud", true)
	c.SetCloudAllowed("jules", true)
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	back, err := Load()
	if err != nil || !back.CloudAllowed("codex-cloud") || !back.CloudAllowed("jules") || back.CloudSettings("codex-cloud").Environments["github.com/acme/api"] != "env_1" {
		t.Fatalf("round trip: %+v %v", back.Clouds, err)
	}
	back.SetCloudEnvironment("codex-cloud", "github.com/acme/web", "acme-web")
	back.SetCloudEnvironment("codex-cloud", "github.com/acme/api", "")
	back.SetCloudEnvironment("claude-cloud", "github.com/acme/api", "x")
	if e := back.Clouds["codex-cloud"].Environments; len(e) != 1 || e["github.com/acme/web"] != "acme-web" || !back.CloudAllowed("codex-cloud") ||
		back.Clouds["claude-cloud"].Environments["github.com/acme/api"] != "x" {
		t.Fatalf("set environments: %+v", back.Clouds)
	}
	back.SetCloudEnvironment("claude-cloud", "github.com/acme/api", "")
	if back.Clouds["claude-cloud"].Environments != nil {
		t.Fatalf("the last environment forgotten: %+v", back.Clouds["claude-cloud"])
	}
	bad := "schema = 4\n[clouds.claude-cloud]\ncode = \"zip\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil || errors.Is(err, ErrOldConfig) {
		t.Fatalf("an unknown code way must be refused (not as an old file), got %v", err)
	}
}

// The terminal app and where sessions resume: omitted until set, kept, and checked.
func TestTerminal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOPSESH_CONFIG_DIR", dir)
	c := Defaults()
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(Path()); strings.Contains(string(b), "terminal") {
		t.Fatalf("an unset terminal is written:\n%s", b)
	}
	if c.ResumeIn() != ResumeTerminal {
		t.Fatal(c.ResumeIn())
	}
	c.Terminal = Terminal{App: TerminalITerm2, Resume: ResumeAsk}
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	back, err := Load()
	if err != nil || back.Terminal != c.Terminal || back.ResumeIn() != ResumeAsk {
		t.Fatalf("%+v %v", back.Terminal, err)
	}
	for _, bad := range []Terminal{{App: "xterm"}, {Resume: "elsewhere"}} {
		c.Terminal = bad
		if c.Check() == nil {
			t.Errorf("%+v passed", bad)
		}
	}
}

// The app's Terminal window: defaults, a round trip, and values it cannot use.
func TestTerminalWindow(t *testing.T) {
	t.Setenv("HOPSESH_CONFIG_DIR", t.TempDir())
	c := Defaults()
	if c.AppResume() != AppResumeDefault || c.ResumeIn() != ResumeTerminal {
		t.Fatalf("defaults: app %q, command line %q", c.AppResume(), c.ResumeIn())
	}
	if c.TerminalFont() != 13 || c.TerminalLines() != 5000 || !c.KeepTabsOn() || !c.NotifyOn() || !c.CloseEndedOn() {
		t.Fatalf("defaults: %d %d %v %v %v", c.TerminalFont(), c.TerminalLines(), c.KeepTabsOn(), c.NotifyOn(), c.CloseEndedOn())
	}
	off, on := false, true
	c.Terminal = Terminal{Resume: ResumeTerminal, Font: "JetBrains Mono, Menlo", FontSize: 15, Scrollback: 10000, KeepTabs: &off, Notify: &off,
		KeepEnded: &on, ScreenReader: "on", SystemConsole: true}
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	back, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if back.AppResume() != ResumeTerminal || back.Terminal.Font != "JetBrains Mono, Menlo" || back.TerminalFont() != 15 || back.TerminalLines() != 10000 ||
		back.KeepTabsOn() || back.NotifyOn() || back.CloseEndedOn() || back.Terminal.ScreenReader != "on" || !back.Terminal.SystemConsole {
		t.Fatalf("%+v", back.Terminal)
	}
	for _, bad := range []Terminal{{FontSize: 8}, {FontSize: 25}, {Scrollback: 2000}, {Font: "x; background:url(//evil)"}, {Font: "a{b}"}, {ScreenReader: "yes"}} {
		c.Terminal = bad
		if c.Check() == nil {
			t.Errorf("%+v passed", bad)
		}
	}
}

// The app window's layout: a round trip, and widths it cannot use.
func TestWindowLayout(t *testing.T) {
	t.Setenv("HOPSESH_CONFIG_DIR", t.TempDir())
	c := Defaults()
	c.Window = Window{SidebarWidth: 260, SidebarHidden: true, InspectorWidth: 900}
	c.Inspector = Inspector{Open: []string{"copies"}, Closed: []string{"repository"}}
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	back, err := Load()
	if err != nil || back.Window != c.Window || !reflect.DeepEqual(back.Inspector, c.Inspector) {
		t.Fatalf("%+v %+v %v", back.Window, back.Inspector, err)
	}
	c.Inspector = Inspector{Open: []string{"history"}}
	if c.Check() == nil {
		t.Error("an unknown inspector section passed the check")
	}
	c.Inspector = Inspector{}
	for _, bad := range []Window{{SidebarWidth: 100}, {SidebarWidth: 400}, {InspectorWidth: 200}, {InspectorWidth: 1000}} {
		c.Window = bad
		if c.Check() == nil {
			t.Errorf("%+v passed the check", bad)
		}
	}
}

// The session list's display and filters: omitted until set, a round trip, and values the
// app does not know, refused with the ones it does.
func TestList(t *testing.T) {
	t.Setenv("HOPSESH_CONFIG_DIR", t.TempDir())
	c := Defaults()
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(Path()); strings.Contains(string(b), "[list") {
		t.Fatalf("an unset list is written:\n%s", b)
	}
	c.List = List{GroupBy: "location", SortBy: "title", SortReverse: true, Density: "compact", CollapseInactive: true,
		Collapsed: []string{"location:laptop"}, Expanded: []string{"location:here"},
		Filter: ListFilter{Status: []string{"working", "idle", "needs"}, Location: []string{"clouds"}, LocationNot: true, Agent: []string{"codex"},
			Repository: []string{"github.com/o/r"}, LastActive: "7d", Has: []string{"tab"}}}
	c.Agents = map[string]Agent{"claude": {Place: PlaceTerminal}, "codex": {Place: PlaceHere}}
	off := false
	c.Previews = &off
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(Path())
	for _, want := range []string{"[list]", "[list.filter]", "group_by = \"location\"", "status = [\"working\", \"idle\", \"needs\"]", "[agents.claude]", "place = \"terminal\"", "previews = false"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the file lacks %s:\n%s", want, b)
		}
	}
	back, err := Load()
	if err != nil || !reflect.DeepEqual(back.List, c.List) || !reflect.DeepEqual(back.Agents, c.Agents) || back.PreviewsOn() {
		t.Fatalf("%+v %+v %v", back.List, back.Agents, err)
	}
	for _, bad := range []List{{GroupBy: "folder"}, {SortBy: "size-desc"}, {Density: "dense"}, {Filter: ListFilter{Status: []string{"running"}}},
		{Filter: ListFilter{Location: []string{"laptop"}}}, {Filter: ListFilter{LastActive: "1y"}}, {Filter: ListFilter{Has: []string{"pr"}}},
		{Collapsed: make([]string, ListKeysMax+1)}} {
		c.List = bad
		if c.Check() == nil {
			t.Errorf("%+v passed the check", bad)
		}
	}
	c.List = List{GroupBy: "folder"}
	if err := c.Check(); err == nil || !strings.Contains(err.Error(), `use "repository", "location", "agent", "account", "tag", "status", "last-active", "none"`) {
		t.Errorf("the error names the allowed values: %v", err)
	}
	c.List = List{}
	c.Agents = map[string]Agent{"claude": {Place: "browser"}}
	if c.Check() == nil {
		t.Error("an unknown place passed the check")
	}
}
