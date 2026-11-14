package opencode

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
)

// localPathHost is a fake machine with this machine's paths, for the plugin folder's real files.
type localPathHost struct{ *agenttest.FakeHost }

func (localPathHost) Path() agent.Path { return agent.PathFor(runtime.GOOS) }

// osDirFS is this machine's files, as the app installs a hook folder (ReadDir by Lstat).
type osDirFS struct{}

func (osDirFS) ReadDir(p string) ([]fs.FileInfo, error) {
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	var out []fs.FileInfo
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, fi)
	}
	return out, nil
}
func (osDirFS) ReadFile(p string, _ int64) ([]byte, error) { return os.ReadFile(p) }
func (osDirFS) WriteFile(p string, b []byte, perm fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, b, perm)
}
func (osDirFS) MkdirAll(p string) error      { return os.MkdirAll(p, 0o755) }
func (osDirFS) Rename(from, to string) error { return os.Rename(from, to) }
func (osDirFS) RemoveAll(p string) error     { return os.RemoveAll(p) }

func pluginInstall(t *testing.T, version string) (agent.Host, agent.Install, string) {
	t.Helper()
	root := t.TempDir()
	h := localPathHost{agenttest.NewFakeHost(root)}
	config := filepath.Join(root, "config", "opencode")
	in := agent.Install{Version: version, Roots: map[string]string{rootData: filepath.Join(root, "data"), rootConfig: config}}
	return h, in, config
}

// The plugin is a folder hopsesh owns whole under {config}/plugins/hopsesh, staged in
// {config}/.hopsesh: installed, recognised, replaced for another hopsesh, and removed.
func TestPluginInstallHasRemove(t *testing.T) {
	h, in, config := pluginInstall(t, "2.3.0")
	pa := h.Path()
	bin := filepath.Join("/opt", "hop sesh", "hopsesh")
	m := New()
	hi := m.MovementHooks(h, in, bin, "--config-dir", "/c", "--state-dir", "/s")
	d := hi.Dir
	if hi.Reason != "" || hi.File != nil || d == nil {
		t.Fatalf("the plugin: %+v", hi)
	}
	if d.Path != filepath.Join(config, "plugins", "hopsesh") || d.Staging != filepath.Join(config, ".hopsesh") || d.Root != rootConfig || !d.In(in, pa) {
		t.Fatalf("the folder: %+v", d)
	}
	if have, _ := agent.ReadHookDir(osDirFS{}, pa, d); d.Has(have) {
		t.Fatal("a missing plugin reads as installed")
	}
	if changed, err := agent.InstallHookDir(osDirFS{}, pa, d); err != nil || !changed {
		t.Fatalf("install: %v %v", changed, err)
	}
	have, err := agent.ReadHookDir(osDirFS{}, pa, d)
	if err != nil || !d.Has(have) {
		t.Fatalf("installed: %v %v", keys(have), err)
	}
	if got := keys(have); !slices.Equal(got, []string{"config.json", "hopsesh.js", "index.js", "package.json"}) {
		t.Fatalf("files: %v", got)
	}
	if !strings.Contains(string(have["index.js"]), `Plugin.define`) || !strings.Contains(string(have["index.js"]), `id: "hopsesh"`) {
		t.Fatalf("index.js: %s", have["index.js"])
	}
	var cfg pluginConfig
	if err := json.Unmarshal(have["config.json"], &cfg); err != nil {
		t.Fatal(err)
	}
	wantArgv := []string{bin, "notice-hook", "--agent", "opencode", "--profile", "", "--config-dir", "/c", "--state-dir", "/s"}
	if !slices.Equal(cfg.Argv, wantArgv) || cfg.Rules != filepath.Join(config, ".hopsesh", "rules.json") || cfg.TimeoutMs != 5000 || cfg.Format != 1 {
		t.Fatalf("config.json: %+v", cfg)
	}
	if prog, ok := d.Runs(have); !ok || prog != bin {
		t.Fatalf("runs %q %v", prog, ok)
	}
	if changed, err := agent.InstallHookDir(osDirFS{}, pa, d); err != nil || changed {
		t.Fatalf("a second install changed it: %v %v", changed, err)
	}
	// Another hopsesh's plugin is not this one's, and an install replaces it whole (an
	// edit too).
	other := m.MovementHooks(h, in, "/usr/local/bin/hopsesh").Dir
	if other.Has(have) {
		t.Fatal("another hopsesh's plugin reads as installed")
	}
	if prog, ok := other.Runs(have); !ok || prog != bin {
		t.Fatalf("Runs reads the program whatever the arguments: %q %v", prog, ok)
	}
	// An older (or edited) copy of the plugin's code, and a file added to the folder (a
	// .DS_Store), still run this hopsesh's notice hook: installed.
	if err := os.WriteFile(filepath.Join(d.Path, "hopsesh.js"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.Path, ".DS_Store"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if have, _ := agent.ReadHookDir(osDirFS{}, pa, other); other.Has(have) || !d.Has(have) {
		t.Fatal("an older copy of this hopsesh's plugin does not read as installed, or another's does")
	}
	if have, _ := agent.ReadHookDir(osDirFS{}, pa, d); d.Has(map[string][]byte{"config.json": have["config.json"]}) {
		t.Fatal("config.json alone reads as installed")
	}
	if _, err := agent.InstallHookDir(osDirFS{}, pa, other); err != nil {
		t.Fatal(err)
	}
	if have, _ := agent.ReadHookDir(osDirFS{}, pa, other); !other.Has(have) || d.Has(have) {
		t.Fatal("the install did not replace the plugin")
	}
	if have, _ := agent.ReadHookDir(osDirFS{}, pa, other); len(have) != 4 {
		t.Fatalf("the install kept a file added to the folder: %v", keys(have))
	}
	if changed, err := agent.RemoveHookDir(osDirFS{}, pa, other); err != nil || !changed {
		t.Fatalf("remove: %v %v", changed, err)
	}
	if _, err := os.Stat(d.Path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the folder is still there: %v", err)
	}
	if entries, _ := os.ReadDir(d.Staging); len(entries) != 0 {
		t.Fatalf("staging copies left: %v", entries)
	}
	// A plugins/hopsesh folder that is not hopsesh's is never replaced or removed.
	for name, b := range map[string]string{"index.js": "export default {}\n", "config.json": `{"argv":["/x/other","notice-hook","--agent","claude"]}`} {
		if err := os.MkdirAll(d.Path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d.Path, name), []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := agent.InstallHookDir(osDirFS{}, pa, d); err == nil || !strings.Contains(err.Error(), "not hopsesh's plugin") {
		t.Fatalf("installed over someone else's folder: %v", err)
	}
	if changed, err := agent.RemoveHookDir(osDirFS{}, pa, d); err != nil || changed {
		t.Fatalf("removed someone else's folder: %v %v", changed, err)
	}
	if have, _ := agent.ReadHookDir(osDirFS{}, pa, d); len(have) != 2 || d.Has(have) {
		t.Fatalf("someone else's folder changed: %v", keys(have))
	}
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// The plugin needs an OpenCode it was seen to run in, a config folder, and a command line
// that can be passed on.
func TestPluginNotOffered(t *testing.T) {
	m := New()
	h, in, _ := pluginInstall(t, "2.2.6")
	if hi := m.MovementHooks(h, in, "/bin/hopsesh"); !strings.Contains(hi.Reason, "needs OpenCode 2.2.7 or newer") || hi.Dir == nil {
		t.Fatalf("old OpenCode: %+v", hi)
	}
	in.Version = "2.3.0"
	if hi := m.MovementHooks(h, in, "/bin/hop\nsesh"); hi.Reason == "" || hi.Dir != nil {
		t.Fatalf("a newline in the program: %+v", hi)
	}
	delete(in.Roots, rootConfig)
	if hi := m.MovementHooks(h, in, "/bin/hopsesh"); hi.Reason == "" || hi.Dir != nil {
		t.Fatalf("no config folder: %+v", hi)
	}
}

// The plugin's call and hopsesh's answer.
func TestNoticeHookProtocol(t *testing.T) {
	m := New()
	call, ok := m.DecodeNoticeHook([]byte(`{"session_id":"ses_a","directory":"/w","event":"UserPromptSubmit"}`))
	if !ok || call != (agent.NoticeHookCall{Session: "ses_a", Event: "UserPromptSubmit"}) {
		t.Fatalf("%+v %v", call, ok)
	}
	for _, bad := range []string{`{`, `[]`, `{"session_id":"ses_a","directory":"` + strings.Repeat("x", 5000) + `"}`} {
		if _, ok := m.DecodeNoticeHook([]byte(bad)); ok {
			t.Errorf("accepted %.40q", bad)
		}
	}
	// Claude Code's JSON carries no event the plugin's way: the core then refuses the call.
	if call, _ := m.DecodeNoticeHook([]byte(`{"session_id":"s","hook_event_name":"SessionStart","transcript_path":"/x"}`)); call.Event != "" || call.Path != "" {
		t.Fatalf("%+v", call)
	}
	for _, c := range []struct {
		a    agent.NoticeHookAnswer
		want string
	}{
		{agent.NoticeHookAnswer{}, `{"block":false}`},
		{agent.NoticeHookAnswer{Text: "Hopsesh status: moved."}, `{"block":false,"context":"Hopsesh status: moved."}`},
		{agent.NoticeHookAnswer{Text: "Blocked by hopsesh.", Block: true}, `{"block":true,"reason":"Blocked by hopsesh."}`},
	} {
		for _, event := range []string{"SessionStart", "UserPromptSubmit"} {
			if got := string(m.EncodeNoticeHook(agent.NoticeHookCall{Event: event}, c.a)); got != c.want {
				t.Errorf("%s %+v: %s", event, c.a, got)
			}
		}
	}
}

// What OpenCode's list of loaded plugins says about hopsesh's.
func TestPluginTrust(t *testing.T) {
	pa := agent.PosixPath{}
	dir := "/home/u/.config/opencode/plugins/hopsesh"
	p := func(id, typ, path, status string) pluginInfo {
		var x pluginInfo
		x.ID, x.Source.Type, x.Source.Path, x.State.Status = id, typ, path, status
		return x
	}
	builtin := p("opencode.agent", "builtin", "", "active")
	failed := p("hopsesh", "local", dir, "failed")
	failed.State.Error, failed.State.Ref = "Plugin entrypoint not found", "err_1234"
	// A plugin that fails to load is listed by its folder, with no id.
	unloaded := p("", "local", dir, "failed")
	unloaded.State.Error, unloaded.State.Ref = "Plugin must export a default definition\x1b[31m\nwith an id", "err_5678"
	for _, c := range []struct {
		name     string
		plugins  []pluginInfo
		state    string
		detail   string
		excluded bool // installing it again changes nothing
	}{
		{"loaded", []pluginInfo{builtin, p("hopsesh", "local", dir, "active")}, agent.HookTrusted, "", false},
		{"loaded, folder written another way", []pluginInfo{p("hopsesh", "local", dir+"/", "active")}, agent.HookTrusted, "", false},
		{"removed or denied", []pluginInfo{builtin}, agent.HookMissing, `"-hopsesh"`, true},
		{"failed", []pluginInfo{builtin, failed}, agent.HookMissing, "err_1234", false},
		{"failed to load", []pluginInfo{builtin, unloaded}, agent.HookMissing, "definition [31m with an id (err_5678 in OpenCode's log)", false},
		{"failed to load, another hopsesh loaded", []pluginInfo{p("hopsesh", "local", "/elsewhere/hopsesh", "active"), unloaded}, agent.HookMissing, "err_5678", false},
		{"someone else's", []pluginInfo{p("hopsesh", "local", "/elsewhere/hopsesh", "active")}, agent.HookMissing, "another plugin", false},
		{"ours among others", []pluginInfo{p("hopsesh", "local", "/elsewhere/hopsesh", "active"), p("hopsesh", "local", dir, "active")}, agent.HookTrusted, "", false},
	} {
		got := pluginTrust(c.plugins, dir, pa)
		if got.State != c.state || !strings.Contains(got.Detail, c.detail) || got.State != agent.HookTrusted && got.Fix == "" || got.Excluded != c.excluded {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
	if k := pluginStorageKey("hopsesh", "failures"); k != "plugin:0068006f00700073006500730068:failures" {
		t.Fatalf("storage key %s", k)
	}
	if k := pluginStorageKey("é😀", "x"); k != "plugin:00e9d83dde00:x" {
		t.Fatalf("UTF-16 key %s", k)
	}
}

// HookTrust asks only a running service, never a private one (which answers before it
// loads plugins), and reads the plugin's failure count from its storage.
func TestHookTrust(t *testing.T) {
	wait := pluginRetryWait
	pluginRetryWait = time.Millisecond
	t.Cleanup(func() { pluginRetryWait = wait })
	fh, f := newMachine(t, setup{})
	m, h, in := open(t, fh)
	dir, _ := pluginDir(h, in)
	if got := m.HookTrust(ctx, h, in, nil); got.State != agent.HookUnknown || !strings.Contains(got.Detail, "isn't running") {
		t.Fatalf("stopped: %+v", got)
	}
	f.status = "http://127.0.0.1:49374"
	lists := []string{`{"data":[]}`, fmt.Sprintf(`{"location":{"directory":"/home/u"},"data":[{"id":"opencode.agent","source":{"type":"builtin"},"features":{"server":true},"state":{"status":"active"}},{"id":"hopsesh","source":{"type":"local","path":%q},"features":{"server":true},"state":{"status":"active"}}]}`, dir)}
	asked := 0
	f.onCall = func(args []string) (agent.Result, bool) {
		if slices.Contains(args, "/api/plugin") {
			if slices.Contains(args, "--standalone") {
				t.Errorf("asked a private server: %v", args)
			}
			asked++
			return agent.Result{Stdout: []byte(lists[min(asked, len(lists))-1])}, true
		}
		return agent.Result{}, false
	}
	if got := m.HookTrust(ctx, h, in, nil); got.State != agent.HookTrusted || got.Failures != 0 || asked != 2 {
		t.Fatalf("running (asked %d): %+v", asked, got)
	}
	// The last failure an hour ago is reported; one more than a week ago no longer is.
	recent := time.Now().Add(-time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
	if err := fh.ExecSQL(dbPath, "INSERT INTO kv (key, value, time_created, time_updated) VALUES (?, ?, 1, 1)",
		pluginStorageKey("hopsesh", "failures"), `{"count":3,"at":"`+recent+`","reason":"hopsesh did not answer within 5000 ms"}`); err != nil {
		t.Fatal(err)
	}
	if got := m.HookTrust(ctx, h, in, nil); got.State != agent.HookTrusted || got.Failures != 3 || !strings.Contains(got.Detail, "did not answer within 5000 ms") {
		t.Fatalf("with failures: %+v", got)
	}
	old := time.Now().Add(-failureWindow - time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
	if err := fh.ExecSQL(dbPath, "UPDATE kv SET value = ? WHERE key = ?",
		`{"count":3,"at":"`+old+`","reason":"hopsesh did not answer within 5000 ms"}`, pluginStorageKey("hopsesh", "failures")); err != nil {
		t.Fatal(err)
	}
	if got := m.HookTrust(ctx, h, in, nil); got.State != agent.HookTrusted || got.Failures != 0 || got.Detail != "" {
		t.Fatalf("with failures over a week old: %+v", got)
	}
	// A list without hopsesh's plugin (OpenCode may not have reloaded its plugins yet) is
	// asked for again: three times in all.
	asked = 0
	f.onCall = func(args []string) (agent.Result, bool) {
		if slices.Contains(args, "/api/plugin") {
			asked++
			return agent.Result{Stdout: []byte(`{"data":[{"id":"opencode.agent","source":{"type":"builtin"},"state":{"status":"active"}}]}`)}, true
		}
		return agent.Result{}, false
	}
	if got := m.HookTrust(ctx, h, in, nil); got.State != agent.HookMissing || asked != 3 || !strings.Contains(got.Detail, "not reloaded") {
		t.Fatalf("not loaded (asked %d): %+v", asked, got)
	}
	asked = 0
	lists[0] = `{"data":[{"id":"opencode.agent","source":{"type":"builtin"},"state":{"status":"active"}}]}`
	f.onCall = func(args []string) (agent.Result, bool) {
		if slices.Contains(args, "/api/plugin") {
			asked++
			return agent.Result{Stdout: []byte(lists[min(asked, len(lists))-1])}, true
		}
		return agent.Result{}, false
	}
	if got := m.HookTrust(ctx, h, in, nil); got.State != agent.HookTrusted || asked != 2 {
		t.Fatalf("loaded a moment later (asked %d): %+v", asked, got)
	}
	f.status = "garbled"
	if got := m.HookTrust(ctx, h, in, nil); got.State != agent.HookUnknown {
		t.Fatalf("unknown service: %+v", got)
	}
}

// The plugin's own tests run with Bun where it is installed (not yet in CI: PR 9 adds it);
// without Bun they skip.
func TestPluginJS(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("Bun is not installed: the plugin's tests (plugin/hopsesh.test.js) did not run")
	}
	cmd := exec.Command(bun, "test")
	cmd.Dir = "plugin"
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bun test: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}

// A plugin installed before hopsesh registered the default folder names no profile; once
// it is registered, that plugin is still the default folder's (its notice hook finds the
// default folder by its root), and never an added folder's.
func TestPluginOfTheDefaultFolderBeforeItWasRegistered(t *testing.T) {
	h, in, _ := pluginInstall(t, "2.3.0")
	pa := h.Path()
	m := New()
	d := m.MovementHooks(h, in, "/bin/hopsesh").Dir
	if _, err := agent.InstallHookDir(osDirFS{}, pa, d); err != nil {
		t.Fatal(err)
	}
	have, err := agent.ReadHookDir(osDirFS{}, pa, d)
	if err != nil {
		t.Fatal(err)
	}
	def := in
	def.Profile = &agent.RuntimeProfile{ID: "registered", Default: true}
	if !m.MovementHooks(h, def, "/bin/hopsesh").Dir.Has(have) {
		t.Error("the default folder's plugin reads as missing once the folder is registered")
	}
	added := in
	added.Profile = &agent.RuntimeProfile{ID: "work"}
	if m.MovementHooks(h, added, "/bin/hopsesh").Dir.Has(have) {
		t.Error("an added folder takes the default folder's plugin as its own")
	}
}
