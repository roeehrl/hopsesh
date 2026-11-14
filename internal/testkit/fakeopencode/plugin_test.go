package fakeopencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// pluginIDs are the ids GET /api/plugin lists, with each one's status.
func (m *machine) pluginIDs(args ...string) []string {
	m.t.Helper()
	var list struct {
		Data []struct {
			ID     string `json:"id"`
			Source struct {
				Type string `json:"type"`
				Path string `json:"path"`
			} `json:"source"`
			State struct {
				Status string `json:"status"`
			} `json:"state"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(m.must(append([]string{"api", "GET", "/api/plugin"}, args...)...)), &list); err != nil {
		m.t.Fatal(err)
	}
	var out []string
	for _, p := range list.Data {
		out = append(out, p.ID+":"+p.State.Status)
		if p.Source.Type == "local" && p.Source.Path == "" {
			m.t.Fatalf("a local plugin without its folder: %+v", p)
		}
	}
	return out
}

func (m *machine) write(rel, s string) {
	m.t.Helper()
	p := filepath.Join(m.home, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		m.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		m.t.Fatal(err)
	}
}

// OpenCode lists the plugins a running server loaded: its own, then the config folders'
// plugin folders, less those a config entry removes ("-id", "-*", "-prefix.*" matching
// the id) or a policy denies; a private server answers before loading any.
func TestPluginList(t *testing.T) {
	m := newMachine(t)
	m.env["FAKE_OPENCODE_SERVICE"] = "running"
	m.write(".config/opencode/plugins/hopsesh/index.js", "export default Plugin.define({ id: \"hopsesh\", setup() {} })\n")
	m.write(".config/opencode/plugins/hopsesh/config.json", "{}")
	m.write(".config/opencode/plugins/other.js", "export default { id: 'other.tool', setup() {} }\n")
	m.write(".config/opencode/plugins/broken/index.ts", "export default 42\n")
	m.write(".config/opencode/plugins/notes/README.md", "no entry: not a plugin\n")
	m.write(".opencode/plugins/local/server.ts", "export default { id: 'project.local', setup() {} }\n")
	all := []string{"opencode.agent:active", "opencode.tool.shell:active", "broken:failed", "hopsesh:active", "other.tool:active", "project.local:active"}
	if got := m.pluginIDs(); !slices.Equal(got, all) {
		t.Fatalf("plugins: %v", got)
	}
	without := func(ids ...string) []string {
		return slices.DeleteFunc(slices.Clone(all), func(s string) bool { return slices.Contains(ids, s) })
	}
	for _, c := range []struct {
		name, config string
		want         []string
	}{
		{"removed by id", `{"plugins":["-hopsesh"]}`, without("hopsesh:active")},
		{"removed with every plugin", `{"plugins":["-*"]}`, without("hopsesh:active", "other.tool:active", "broken:failed", "project.local:active")},
		{"a prefix of other ids", `{"plugins":["-hop.*","-other.*"]}`, without("other.tool:active")},
		{"denied by policy", `{"policies":[{"action":"plugin.use","resource":"*","effect":"deny"}]}`, without("hopsesh:active", "other.tool:active", "broken:failed", "project.local:active")},
		{"a policy for another folder", `{"policies":[{"action":"plugin.use","resource":"*/other.js","effect":"deny"}]}`, without("other.tool:active")},
		{"allowed first", `{"policies":[{"action":"plugin.use","resource":"*","effect":"allow"},{"action":"plugin.use","resource":"*","effect":"deny"}]}`, all},
	} {
		m.write(".config/opencode/opencode.json", c.config)
		if got := m.pluginIDs(); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	// A project's config removes too.
	m.write(".config/opencode/opencode.json", `{}`)
	m.write(".opencode/opencode.json", `{"plugins":["-hopsesh"]}`)
	if got := m.pluginIDs(); slices.Contains(got, "hopsesh:active") {
		t.Errorf("a project config's removal: %v", got)
	}
	// A private server answers before it loads them.
	if got := m.pluginIDs("--standalone"); len(got) != 0 {
		t.Errorf("--standalone: %v", got)
	}
}
