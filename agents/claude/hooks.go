package claude

import (
	"encoding/json"
	"errors"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"io/fs"
)

var _ agent.MovementHookIntegrator = (*Module)(nil)

// MovementHooks uses documented synchronous command hooks, never prompt/agent
// hooks. 2.1.284 is our conservative compatibility baseline, not their introduction.
// Windows explicitly selects the documented PowerShell shell, avoiding Git Bash
// availability/default-shell ambiguity. Contract checked 2026-10-06: https://code.claude.com/docs/en/hooks
func (m *Module) MovementHooks(h agent.Host, in agent.Install, bin string, args ...string) agent.HookIntegration {
	out := agent.HookIntegration{Evidence: "https://code.claude.com/docs/en/hooks"}
	command, err := agent.NoticeHookCommand(bin, m.Spec().ID, in.ProfileID(), args...)
	if err != nil {
		out.Reason = err.Error()
		return out
	}
	path := h.Path().Join(in.Root(home), "settings.json")
	handler := map[string]any{"type": "command", "command": command, "timeout": 5}
	if h.Facts().OS == "windows" {
		handler["command"], _ = agent.NoticeHookPowerShellScript(bin, m.Spec().ID, in.ProfileID(), args...)
		handler["shell"] = "powershell"
	}
	out.File = agent.JSONMovementHookFileWithHandler(path, handler)
	if b, err := h.FS().ReadFile(path, 1<<20); err == nil {
		var cfg struct {
			Disable bool `json:"disableAllHooks"`
		}
		if json.Unmarshal(b, &cfg) != nil {
			out.DisabledReason = "cannot read Claude hook policy from settings.json"
		} else if cfg.Disable {
			out.DisabledReason = "Claude disableAllHooks is true; enable hooks in Claude settings to deliver notices"
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		out.DisabledReason = "cannot read Claude hook policy from settings.json"
	}
	if !agent.HookVersionAtLeast(in.Version, "2.1.284") {
		out.Reason = "movement hooks require a verified Claude Code version >= 2.1.284 (major 2)"
	}
	return out
}
