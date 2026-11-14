package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

var _ agent.HookTrustReporter = (*Module)(nil)

// HookTrust asks OpenCode's running service whether it loaded the hopsesh plugin. OpenCode
// asks no one before it loads a plugin folder, but a plugin entry in an OpenCode config
// ("-hopsesh" or "-*"; a selector "x.*" matches only ids that start with "x.", which
// "hopsesh" does not) removes it, a policy (plugin.use) can deny it, and a plugin that
// fails to load is listed as failed. hopsesh never reads OpenCode's config (it holds
// provider keys): the service's list says what it loaded. With the service stopped
// nothing can be asked (a private server answers before it loads plugins), so trust is
// unknown until OpenCode runs.
func (m *Module) HookTrust(ctx context.Context, h agent.Host, in agent.Install, _ []string) agent.HookTrust {
	svc := probeService(ctx, h)
	switch svc.State {
	case serviceStopped:
		return agent.HookTrust{State: agent.HookUnknown, Detail: "OpenCode isn't running; it loads the plugin when it starts, and hopsesh checks it then."}
	case serviceUnknown:
		return agent.HookTrust{State: agent.HookUnknown, Detail: "could not ask OpenCode: " + svc.err.Error()}
	}
	dir, _ := pluginDir(h, in)
	plugins, err := loadedPlugins(ctx, h, dir)
	if err != nil {
		return agent.HookTrust{State: agent.HookUnknown, Detail: "could not ask OpenCode: " + err.Error()}
	}
	trust := pluginTrust(plugins, dir, h.Path())
	if trust.State == agent.HookTrusted {
		trust.Failures, trust.Detail = pluginFailures(ctx, h, in)
	}
	return trust
}

// pluginRetryWait is the pause between asks while OpenCode's list lacks hopsesh's plugin.
var pluginRetryWait = time.Second

// pluginInfo is one plugin in OpenCode's list (GET /api/plugin).
type pluginInfo struct {
	ID     string `json:"id"`
	Source struct {
		Type string `json:"type"`
		Path string `json:"path"`
	} `json:"source"`
	State struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Ref    string `json:"ref"`
	} `json:"state"`
}

// loadedPlugins lists the plugins the running service loaded for the home folder (every
// folder loads the global plugins). An empty list is one the service has not finished
// loading yet, and one without hopsesh's plugin (from dir) may be from before OpenCode
// reloaded a plugin folder hopsesh just wrote: both are asked again for a few seconds.
func loadedPlugins(ctx context.Context, h agent.Host, dir string) ([]pluginInfo, error) {
	// Only asked while the service runs, so never --standalone; the service is probed again
	// before each new ask. One that stops in the moment between a probe and the ask is
	// started by `opencode api`, the gap cd.go accepts too.
	svc := service{State: serviceRunning}
	for attempt := 0; ; attempt++ {
		res, err := run(ctx, h, []string{"api", "GET", "/api/plugin"}, agent.RunOptions{Dir: h.Facts().Home, Timeout: 30 * time.Second})
		if err != nil {
			return nil, err
		}
		if res.Code != 0 {
			return nil, apiError("GET", "/api/plugin", res)
		}
		var list struct {
			Data []pluginInfo `json:"data"`
		}
		if err := json.Unmarshal(res.Stdout, &list); err != nil {
			return nil, fmt.Errorf("OpenCode's answer to GET /api/plugin: %w", err)
		}
		if len(list.Data) > 0 && findPlugin(list.Data, dir, h.Path()) != nil || attempt == 2 {
			if len(list.Data) == 0 {
				return nil, fmt.Errorf("OpenCode has not loaded its plugins yet")
			}
			return list.Data, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pluginRetryWait):
		}
		if probeService(ctx, h).State != svc.State {
			return nil, fmt.Errorf("OpenCode's background service stopped")
		}
	}
}

// findPlugin is hopsesh's plugin in OpenCode's list: the one from dir (a plugin that
// failed to load is listed with its folder but no id), else one with its id.
func findPlugin(plugins []pluginInfo, dir string, pa agent.Path) *pluginInfo {
	for i, p := range plugins {
		if p.Source.Type == "local" && samePath(pa, p.Source.Path, dir) {
			return &plugins[i]
		}
	}
	for i, p := range plugins {
		if p.ID == pluginName {
			return &plugins[i]
		}
	}
	return nil
}

// pluginTrust reads hopsesh's plugin in OpenCode's list. OpenCode's text (an error, a
// path) is shown on one line, without control characters.
func pluginTrust(plugins []pluginInfo, dir string, pa agent.Path) agent.HookTrust {
	fix := "Take the entry that removes hopsesh (\"-hopsesh\", \"-*\") out of the \"plugins\" list in your OpenCode config, or the policy that denies plugins, then restart OpenCode."
	found := findPlugin(plugins, dir, pa)
	switch {
	case found == nil:
		return agent.HookTrust{State: agent.HookMissing, Fix: fix,
			Detail: "OpenCode doesn't load the hopsesh plugin: a plugin entry in an OpenCode config removes it (\"-hopsesh\", \"-*\"), a policy denies plugins (plugin.use), or OpenCode has not reloaded its plugins since hopsesh installed it (restart OpenCode)."}
	case found.State.Status == "failed":
		detail := "OpenCode could not load the hopsesh plugin: " + oneLine(found.State.Error, 500)
		if found.State.Ref != "" {
			detail += " (" + oneLine(found.State.Ref, 64) + " in OpenCode's log)"
		}
		return agent.HookTrust{State: agent.HookMissing, Detail: detail, Fix: "Install hopsesh's hooks again; if OpenCode still can't load the plugin, update OpenCode."}
	case found.State.Status != "active":
		return agent.HookTrust{State: agent.HookUnknown, Detail: "OpenCode lists the hopsesh plugin as " + oneLine(found.State.Status, 64)}
	case found.Source.Type == "local" && !samePath(pa, found.Source.Path, dir):
		return agent.HookTrust{State: agent.HookMissing, Fix: "Install hopsesh's hooks again.",
			Detail: "OpenCode runs another plugin called hopsesh (" + oneLine(found.Source.Path, 1000) + "), not the one hopsesh installed in " + dir + "."}
	}
	return agent.HookTrust{State: agent.HookTrusted}
}

// oneLine is text from OpenCode or its plugin as one line of at most limit bytes, without
// control or bidirectional-formatting characters.
func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == 0x2028 || r == 0x2029 {
			return ' '
		}
		return r
	}, s)), " ")
	if len(s) > limit {
		s = strings.ToValidUTF8(s[:limit], "") + "…"
	}
	return s
}

func samePath(pa agent.Path, a, b string) bool {
	return a != "" && b != "" && pa.Clean(a) == pa.Clean(b)
}

// pluginFailures is the plugin's count of notice-hook calls that failed and let a call
// through, which it keeps in its OpenCode storage (the kv table, under the plugin's id),
// and when and why the last one failed.
func pluginFailures(ctx context.Context, h agent.Host, in agent.Install) (int, string) {
	r, ok, err := openSQL(ctx, h, in)
	if !ok || err != nil {
		return 0, ""
	}
	rows, err := r.sq.Query(ctx, r.db, pluginStorageQuery, pluginStorageKey(pluginName, "failures"))
	if err != nil || len(rows.Rows) == 0 {
		return 0, ""
	}
	var f struct {
		Count  int    `json:"count"`
		At     string `json:"at"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal([]byte(rows.Rows[0].String("value")), &f) != nil || f.Count <= 0 {
		return 0, ""
	}
	return f.Count, fmt.Sprintf("the plugin let %d call(s) through because hopsesh did not answer; the last at %s: %s", f.Count, oneLine(f.At, 64), oneLine(f.Reason, 500))
}

// pluginStorageKey is the kv key OpenCode keeps a plugin's storage entry under: the id's
// UTF-16 code units in 4-digit hex (packages/core/src/plugin/host.ts, storage()).
func pluginStorageKey(pluginID, key string) string {
	var b strings.Builder
	b.WriteString("plugin:")
	for _, u := range utf16.Encode([]rune(pluginID)) {
		fmt.Fprintf(&b, "%04x", u)
	}
	b.WriteString(":" + key)
	return b.String()
}
