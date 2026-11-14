package fakeopencode

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The stand-in loads plugins as OpenCode 2.3.0 does (packages/core/src/plugin), as far as
// hopsesh's protection needs: it finds them, applies the config's removals and policies,
// lists them (GET /api/plugin), and plays a turn's tool call through hopsesh's plugin. It
// runs no JavaScript: a plugin's id is read from its Plugin.define call, and hopsesh's
// plugin is played from its config.json (the notice hook it runs and what it does with the
// answer, as agents/opencode/plugin/hopsesh.js does).

// builtinPlugins are some of OpenCode's own plugins, listed first, as every loaded
// location lists them.
var builtinPlugins = []string{"opencode.agent", "opencode.tool.shell"}

// plugin is one plugin OpenCode would load for a folder.
type plugin struct {
	id     string
	dir    string // its folder (or file)
	entry  string // the entry file
	failed string // why it did not load ("" when it did)
}

// pluginIDIn finds the id a server entry gives Plugin.define (or its default export).
var pluginIDIn = regexp.MustCompile(`\bid\s*:\s*["']([^"']+)["']`)

// configDirs are the folders OpenCode finds plugins in for a working folder: the global
// config folder, then the folder's own .opencode.
func (r *run) configDirs(workdir string) []string {
	return []string{r.paths().config, filepath.Join(workdir, ".opencode")}
}

// plugins are the plugins OpenCode loads for workdir: auto-discovered ones under
// {plugin,plugins}/ of each config folder, less what a config's "plugins" list removes
// ("-id", "-*", "-prefix.*") and what a policy denies (plugin.use).
func (r *run) plugins(workdir string) []plugin {
	var found []plugin
	for _, dir := range r.configDirs(workdir) {
		for _, name := range []string{"plugin", "plugins"} {
			entries, err := os.ReadDir(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
			for _, e := range entries {
				if p, ok := loadPlugin(filepath.Join(dir, name, e.Name())); ok {
					found = append(found, p)
				}
			}
		}
	}
	// Config documents, global first: their "plugins" removals and their policies.
	var removes []string
	var policies []policy
	for _, doc := range []string{filepath.Join(r.paths().config, "opencode.json"), filepath.Join(workdir, "opencode.json"), filepath.Join(workdir, ".opencode", "opencode.json")} {
		var cfg struct {
			Plugins  []json.RawMessage `json:"plugins"`
			Policies []policy          `json:"policies"`
		}
		b, err := os.ReadFile(doc)
		if err != nil || json.Unmarshal(b, &cfg) != nil {
			continue
		}
		for _, raw := range cfg.Plugins {
			var s string
			if json.Unmarshal(raw, &s) == nil && strings.HasPrefix(s, "-") && len(s) > 1 {
				removes = append(removes, s[1:])
			}
		}
		policies = append(policies, cfg.Policies...)
	}
	var out []plugin
	for _, p := range found {
		if denied(policies, "plugin.use", p.dir) {
			continue // blocked: not listed at all
		}
		removed := false
		for _, sel := range removes {
			if sel == "*" || strings.HasSuffix(sel, ".*") && strings.HasPrefix(p.id, strings.TrimSuffix(sel, "*")) || sel == p.id {
				removed = true
			}
		}
		if !removed {
			out = append(out, p)
		}
	}
	return out
}

// loadPlugin reads a plugin folder (or a lone .js/.ts file): its server entry is
// server.* or index.* at its root; a folder without one is not a server plugin.
func loadPlugin(target string) (plugin, bool) {
	fi, err := os.Stat(target)
	if err != nil {
		return plugin{}, false
	}
	p := plugin{dir: target}
	if fi.IsDir() {
		for _, name := range []string{"server", "index"} {
			for _, ext := range []string{".ts", ".js", ".mjs", ".mts"} {
				if _, err := os.Stat(filepath.Join(target, name+ext)); err == nil && p.entry == "" {
					p.entry = filepath.Join(target, name+ext)
				}
			}
		}
		if p.entry == "" {
			return plugin{}, false
		}
	} else if ext := filepath.Ext(target); ext == ".ts" || ext == ".js" {
		p.entry = target
	} else {
		return plugin{}, false
	}
	b, err := os.ReadFile(p.entry)
	if m := pluginIDIn.FindSubmatch(b); err == nil && m != nil {
		p.id = string(m[1])
	} else {
		p.failed = "Plugin must export a default definition with an id and an effect or setup function."
		p.id = filepath.Base(target)
	}
	return p, true
}

// policy is one statement of a config's "policies".
type policy struct {
	Action   string `json:"action"`
	Resource string `json:"resource"`
	Effect   string `json:"effect"`
}

// denied reports whether the first statement for action that matches resource denies it
// (OpenCode lets the earliest-loaded statement win).
func denied(ps []policy, action, resource string) bool {
	for _, p := range ps {
		if p.Action == action && wildcard(p.Resource, resource) {
			return p.Effect == "deny"
		}
	}
	return false
}

// wildcard matches s against a pattern where "*" is any text.
func wildcard(pattern, s string) bool {
	re := "^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*") + "$"
	ok, _ := regexp.MatchString(re, s)
	return ok
}

// pluginList is GET /api/plugin's data. A private server (--standalone) answers before
// it has loaded any plugin, as 2.3.0's does: an empty list.
func (r *run) pluginList() *value {
	list := arr()
	if r.standalone {
		return list
	}
	add := func(id string, source, state *value) {
		p := obj()
		p.set("id", str(id))
		p.set("source", source)
		f := obj()
		f.set("server", lit("true"))
		p.set("features", f)
		p.set("state", state)
		list.vals = append(list.vals, p)
	}
	active := func() *value { s := obj(); s.set("status", str("active")); return s }
	for _, id := range builtinPlugins {
		s := obj()
		s.set("type", str("builtin"))
		add(id, s, active())
	}
	for _, p := range r.plugins(r.workdir()) {
		s := obj()
		s.set("type", str("local"))
		s.set("path", str(p.dir))
		state := active()
		if p.failed != "" {
			state = obj()
			state.set("status", str("failed"))
			state.set("error", str(p.failed))
			state.set("ref", str("err_00000000"))
		}
		add(p.id, s, state)
	}
	return list
}

// hopseshPlugin is hopsesh's plugin's config.json, when OpenCode loads it for workdir.
type hopseshPlugin struct {
	Argv      []string `json:"argv"`
	TimeoutMs int      `json:"timeoutMs"`
}

func (r *run) hopseshPlugin(workdir string) (hopseshPlugin, bool) {
	for _, p := range r.plugins(workdir) {
		if p.id != "hopsesh" || p.failed != "" {
			continue
		}
		var cfg hopseshPlugin
		b, err := os.ReadFile(filepath.Join(p.dir, "config.json"))
		if err != nil || json.Unmarshal(b, &cfg) != nil || len(cfg.Argv) == 0 {
			return cfg, false // the plugin fails open, registering nothing
		}
		return cfg, true
	}
	return hopseshPlugin{}, false
}

// turn plays `opencode run -s <id>` whose model makes one shell call, $FAKE_OPENCODE_TOOL
// (the stand-in has no model and runs no command). Its server loads the plugins first, as
// a turn waits for them. With hopsesh's plugin, the prompt asks hopsesh's notice hook about
// the session (its root session: hopsesh keeps lineage there), and the call fails with the
// hook's reason while the session is blocked, printed as 2.3.0's `run` prints a failed
// tool: "✗ <command> failed", then "Error: <reason>". The turn's messages are not recorded.
func (r *run) turn(command string) int {
	db, err := r.open()
	if err != nil {
		return r.fail("Error: %v", err)
	}
	defer db.Close()
	s, err := getRow(db, r.resume)
	if err != nil {
		return r.fail("Error: Session not found: %s", r.resume)
	}
	fmt.Fprintln(r.Stderr, "> build · stand-in")
	if cfg, ok := r.hopseshPlugin(r.workdir()); ok {
		if block, reason := r.noticeHook(cfg, rootSession(db, s.id), s.directory); block {
			fmt.Fprintf(r.Stderr, "✗ %s failed\nError: %s\n", command, reason)
			fmt.Fprintln(r.Stdout, "The shell call failed: "+reason)
			return 0
		}
	}
	fmt.Fprintf(r.Stderr, "$ %s\n", command)
	fmt.Fprintln(r.Stdout, "(stand-in: the command was allowed; it was not run)")
	return 0
}

// rootSession is the session a subagent's session belongs to.
func rootSession(db *sql.DB, id string) string {
	for range 8 {
		var parent sql.NullString
		if db.QueryRow("SELECT parent_id FROM session_v2 WHERE id = ?", id).Scan(&parent) != nil || !parent.Valid || parent.String == "" {
			return id
		}
		id = parent.String
	}
	return id
}

// noticeHook runs the plugin's notice hook as the plugin does (no shell, the call on
// stdin, a deadline), and fails open.
func (r *run) noticeHook(cfg hopseshPlugin, session, dir string) (block bool, reason string) {
	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	in, _ := json.Marshal(map[string]string{"session_id": session, "directory": dir, "event": "UserPromptSubmit"})
	cmd := exec.CommandContext(ctx, cfg.Argv[0], cfg.Argv[1:]...)
	cmd.Stdin = bytes.NewReader(in)
	out, err := cmd.Output()
	if err != nil {
		r.log("opencode: hopsesh plugin let a call through: " + err.Error())
		return false, ""
	}
	var ans struct {
		Block  *bool  `json:"block"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal(bytes.TrimSpace(out), &ans) != nil || ans.Block == nil {
		r.log("opencode: hopsesh plugin let a call through: no verdict")
		return false, ""
	}
	return *ans.Block, ans.Reason
}
