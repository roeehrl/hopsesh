package opencode

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"slices"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// OpenCode has no command hooks: hopsesh's protection is a plugin, a folder hopsesh owns
// under {config}/plugins/hopsesh that OpenCode loads into every server it runs (its
// background service, or a private one). The plugin asks hopsesh's notice hook about each
// session, as Claude Code's and Codex's hooks do, and refuses the tool calls of a session
// whose copy moved elsewhere (OpenCode cannot refuse a prompt). It also applies hopsesh's
// approval rules (Integration).

var (
	_ agent.MovementHookIntegrator = (*Module)(nil)
	_ agent.NoticeHookProtocol     = (*Module)(nil)
)

// pluginSource is the plugin as OpenCode loads it: plain JavaScript, no dependencies
// (OpenCode provides @opencode/plugin).
//
//go:embed plugin/index.js plugin/hopsesh.js plugin/package.json
var pluginSource embed.FS

// pluginFiles are the plugin's own files, copied as they are; config.json is made per
// install.
var pluginFiles = []string{"index.js", "hopsesh.js", "package.json"}

// pluginConfigFile holds the notice hook's command and the rules file's place.
const pluginConfigFile = "config.json"

// pluginName is the plugin's folder under {config}/plugins and its id in OpenCode (the id
// a config entry "-hopsesh" removes).
const pluginName = "hopsesh"

// hookFloor is the oldest OpenCode the plugin was seen to load in and register its hooks
// with (2.2.7, the desktop app's CLI; the hooks themselves were run on 2.3.0). The plugin
// runs in OpenCode's server, so a service older than this may not load it, which
// HookTrust then reports.
const hookFloor = "2.2.7"

// pluginTimeoutMs bounds one notice-hook call from the plugin; past it the call goes on
// (fail open), as Claude Code's and Codex's 5-second hook timeouts do.
const pluginTimeoutMs = 5000

// pluginCacheMs is how long the plugin keeps a session's verdict for its tool calls; a
// prompt and a move event always ask again.
const pluginCacheMs = 30000

// pluginConfig is config.json.
type pluginConfig struct {
	Format    int      `json:"format"`
	Argv      []string `json:"argv"`
	Rules     string   `json:"rules,omitempty"`
	TimeoutMs int      `json:"timeoutMs"`
	CacheMs   int      `json:"cacheMs"`
}

// pluginDir is the plugin's folder; staging is where its new and old copies wait while it
// is replaced (outside plugins/, which OpenCode scans), beside the rules file.
func pluginDir(h agent.Host, in agent.Install) (dir, staging string) {
	config := in.Root(rootConfig)
	if config == "" {
		return "", ""
	}
	pa := h.Path()
	return pa.Join(config, "plugins", pluginName), pa.Join(config, ".hopsesh")
}

// rulesFile holds hopsesh's approval rules, which the plugin reads.
func rulesFile(h agent.Host, in agent.Install) string {
	_, staging := pluginDir(h, in)
	if staging == "" {
		return ""
	}
	return h.Path().Join(staging, "rules.json")
}

// MovementHooks is the plugin folder, its notice hook run with argv (no shell: the plugin
// spawns the program with its arguments).
func (m *Module) MovementHooks(h agent.Host, in agent.Install, bin string, args ...string) agent.HookIntegration {
	out := agent.HookIntegration{Evidence: "https://opencode.ai/docs/plugins"}
	if _, err := agent.NoticeHookCommand(bin, id, in.ProfileID(), args...); err != nil {
		out.Reason = err.Error()
		return out
	}
	dir, staging := pluginDir(h, in)
	if dir == "" {
		out.Reason = "OpenCode's config folder is not known"
		return out
	}
	argv := append([]string{bin, "notice-hook", "--agent", string(id), "--profile", in.ProfileID()}, args...)
	files, err := renderPlugin(argv, rulesFile(h, in))
	if err != nil {
		out.Reason = err.Error()
		return out
	}
	// The default folder's plugin may have been installed before hopsesh registered the
	// folder (its first scan does): its notice hook then names no profile, which finds
	// the default folder all the same.
	var unnamed map[string][]byte
	if in.Profile != nil && in.Profile.Default {
		argv := append([]string{bin, "notice-hook", "--agent", string(id), "--profile", ""}, args...)
		unnamed, _ = renderPlugin(argv, rulesFile(h, in))
	}
	out.Dir = &agent.HookDir{Root: rootConfig, Path: dir, Staging: staging,
		// hopsesh owns its plugin folder whole: an install replaces it (files added to it
		// go too), and a removal takes it out. A folder of that name that is not hopsesh's
		// plugin is left alone.
		Merge: func(have map[string][]byte) (map[string][]byte, error) {
			if len(have) > 0 && !ownPlugin(have) {
				return nil, fmt.Errorf("%s is not hopsesh's plugin (its %s names no hopsesh notice hook), so hopsesh leaves it; move it away to install hopsesh's", dir, pluginConfigFile)
			}
			return files, nil
		},
		Remove: func(have map[string][]byte) (map[string][]byte, error) {
			if !ownPlugin(have) {
				return have, nil
			}
			return nil, nil
		},
		Has: func(have map[string][]byte) bool {
			return hasPlugin(have, files) || unnamed != nil && hasPlugin(have, unnamed)
		},
		Runs: pluginRuns,
	}
	if !agent.HookVersionAtLeast(in.Version, hookFloor) {
		out.Reason = agent.NeedsVersion("OpenCode", in.Version, hookFloor)
	}
	return out
}

// renderPlugin is the plugin folder for a notice hook argv.
func renderPlugin(argv []string, rules string) (map[string][]byte, error) {
	files := map[string][]byte{}
	for _, name := range pluginFiles {
		b, err := pluginSource.ReadFile(path.Join("plugin", name))
		if err != nil {
			return nil, err
		}
		files[name] = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	}
	cfg, err := json.MarshalIndent(pluginConfig{Format: 1, Argv: argv, Rules: rules, TimeoutMs: pluginTimeoutMs, CacheMs: pluginCacheMs}, "", "  ")
	if err != nil {
		return nil, err
	}
	files[pluginConfigFile] = append(cfg, '\n')
	return files, nil
}

// hasPlugin reports whether the folder runs this hopsesh's notice hook (config.json as
// this install writes it) with hopsesh's plugin files: an older copy of those files still
// protects (the app reports it as differing), and a file added to the folder changes
// nothing.
func hasPlugin(have, want map[string][]byte) bool {
	if !bytes.Equal(have[pluginConfigFile], want[pluginConfigFile]) {
		return false
	}
	for _, name := range pluginFiles {
		if _, ok := have[name]; !ok {
			return false
		}
	}
	return true
}

// ownPlugin reports whether a folder is hopsesh's plugin: its config.json runs a hopsesh
// notice hook for OpenCode.
func ownPlugin(have map[string][]byte) bool {
	_, ok := pluginRuns(have)
	return ok
}

// pluginRuns is the program a plugin folder's notice hook runs, whatever its arguments
// (how another machine's plugin is checked from here).
func pluginRuns(files map[string][]byte) (string, bool) {
	var cfg pluginConfig
	if json.Unmarshal(files[pluginConfigFile], &cfg) != nil || len(cfg.Argv) < 4 {
		return "", false
	}
	if !slices.Equal(cfg.Argv[1:4], []string{"notice-hook", "--agent", string(id)}) || cfg.Argv[0] == "" {
		return "", false
	}
	return cfg.Argv[0], true
}

// noticeCall is what the plugin sends the notice hook: the session, the folder its server
// runs in (not used: the session's lineage follows from its id) and the event.
type noticeCall struct {
	SessionID string `json:"session_id"`
	Directory string `json:"directory"`
	Event     string `json:"event"`
}

// DecodeNoticeHook reads the plugin's call. OpenCode keeps sessions in its database, so
// there is no transcript path: the core finds the session's anchor from its id.
func (*Module) DecodeNoticeHook(raw []byte) (agent.NoticeHookCall, bool) {
	var c noticeCall
	if json.Unmarshal(raw, &c) != nil || len(c.Directory) > 4096 {
		return agent.NoticeHookCall{}, false
	}
	return agent.NoticeHookCall{Session: c.SessionID, Event: c.Event}, true
}

// noticeAnswer is the plugin's verdict: always JSON, so the plugin can tell "nothing to
// say" from a hook that failed (which prints nothing).
type noticeAnswer struct {
	Block   bool   `json:"block"`
	Reason  string `json:"reason,omitempty"`
	Context string `json:"context,omitempty"`
}

// EncodeNoticeHook writes the verdict: a block with its reason (on any event: the plugin
// refuses tool calls, not prompts), or a notice for the model.
func (*Module) EncodeNoticeHook(_ agent.NoticeHookCall, a agent.NoticeHookAnswer) []byte {
	ans := noticeAnswer{Block: a.Block}
	if a.Block {
		ans.Reason = a.Text
	} else {
		ans.Context = a.Text
	}
	b, _ := json.Marshal(ans) // strings and a bool always encode
	return b
}
