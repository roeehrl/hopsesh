package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

var _ agent.HookTrustReporter = (*Module)(nil)

// HookTrust asks OpenCode's running service whether it loaded the hopsesh plugin. OpenCode
// asks no one before it loads a plugin folder, but a plugin entry in an OpenCode config
// ("-hopsesh", "-*", or a prefix such as "-hop.*" that matches the id) removes it, a
// policy (plugin.use) can deny it, and a plugin that fails to load is listed as failed.
// hopsesh never reads OpenCode's config (it holds provider keys): the service's list says
// what it loaded. With the service stopped nothing can be asked (a private server answers
// before it loads plugins), so trust is unknown until OpenCode runs.
func (m *Module) HookTrust(ctx context.Context, h agent.Host, in agent.Install, _ []string) agent.HookTrust {
	svc := probeService(ctx, h)
	switch svc.State {
	case serviceStopped:
		return agent.HookTrust{State: agent.HookUnknown, Detail: "OpenCode isn't running; it loads the plugin when it starts, and hopsesh checks it then."}
	case serviceUnknown:
		return agent.HookTrust{State: agent.HookUnknown, Detail: "could not ask OpenCode: " + svc.err.Error()}
	}
	dir, _ := pluginDir(h, in)
	plugins, err := loadedPlugins(ctx, h)
	if err != nil {
		return agent.HookTrust{State: agent.HookUnknown, Detail: "could not ask OpenCode: " + err.Error()}
	}
	trust := pluginTrust(plugins, dir, h.Path())
	if trust.State == agent.HookTrusted {
		trust.Failures, trust.Detail = pluginFailures(ctx, h, in)
	}
	return trust
}

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
// folder loads the global plugins). A list without OpenCode's own plugins is one the
// service has not finished loading yet: asked again for a few seconds.
func loadedPlugins(ctx context.Context, h agent.Host) ([]pluginInfo, error) {
	svc := service{State: serviceRunning} // only asked while the service runs: never --standalone
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
		if len(list.Data) > 0 || attempt == 2 {
			if len(list.Data) == 0 {
				return nil, fmt.Errorf("OpenCode has not loaded its plugins yet")
			}
			return list.Data, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
		if probeService(ctx, h).State != svc.State {
			return nil, fmt.Errorf("OpenCode's background service stopped")
		}
	}
}

// pluginTrust reads hopsesh's plugin in OpenCode's list: the one in dir, else one with its id.
func pluginTrust(plugins []pluginInfo, dir string, pa agent.Path) agent.HookTrust {
	fix := "Take the entry that removes hopsesh (\"-hopsesh\", \"-*\") out of the \"plugins\" list in your OpenCode config, or the policy that denies plugins, then restart OpenCode."
	var found *pluginInfo
	for i, p := range plugins {
		if p.ID != pluginName {
			continue
		}
		if found == nil || p.Source.Type == "local" && samePath(pa, p.Source.Path, dir) {
			found = &plugins[i]
		}
	}
	switch {
	case found == nil:
		return agent.HookTrust{State: agent.HookMissing, Fix: fix,
			Detail: "OpenCode doesn't load the hopsesh plugin: a plugin entry in an OpenCode config removes it (\"-hopsesh\", \"-*\"), or a policy denies plugins (plugin.use)."}
	case found.State.Status == "failed":
		detail := "OpenCode could not load the hopsesh plugin: " + strings.TrimSpace(found.State.Error)
		if found.State.Ref != "" {
			detail += " (" + found.State.Ref + " in OpenCode's log)"
		}
		return agent.HookTrust{State: agent.HookMissing, Detail: detail, Fix: "Install hopsesh's hooks again; if OpenCode still can't load the plugin, update OpenCode."}
	case found.State.Status != "active":
		return agent.HookTrust{State: agent.HookUnknown, Detail: "OpenCode lists the hopsesh plugin as " + found.State.Status}
	case found.Source.Type == "local" && !samePath(pa, found.Source.Path, dir):
		return agent.HookTrust{State: agent.HookMissing, Fix: "Install hopsesh's hooks again.",
			Detail: "OpenCode runs another plugin called hopsesh (" + found.Source.Path + "), not the one hopsesh installed in " + dir + "."}
	}
	return agent.HookTrust{State: agent.HookTrusted}
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
	return f.Count, fmt.Sprintf("the plugin let %d call(s) through because hopsesh did not answer; the last at %s: %s", f.Count, f.At, f.Reason)
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
