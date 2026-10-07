package codex

import (
	"errors"
	"github.com/BurntSushi/toml"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"io/fs"
)

var _ agent.MovementHookIntegrator = (*Module)(nil)

// MovementHooks uses the current user-layer hooks.json contract. 0.160.0 is a
// conservative compatibility baseline, not a claim about initial hook support.
// Contract and stable version checked 2026-10-06:
// https://developers.openai.com/codex/hooks
// https://developers.openai.com/codex/changelog#october-2026
// Windows runner/source checked at rust-v0.160.0:
// https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/hooks/src/engine/command_runner.rs
// It uses the selected shell or cmd.exe fallback; the encoded PowerShell launch
// works under either without exposing literals to that outer shell.
// Vendor hook trust and features.hooks remain entirely under vendor/user control.
func (m *Module) MovementHooks(h agent.Host, in agent.Install, bin string, args ...string) agent.HookIntegration {
	out := agent.HookIntegration{Evidence: "https://developers.openai.com/codex/hooks"}
	command, err := agent.NoticeHookCommand(bin, m.Spec().ID, in.ProfileID(), args...)
	if err != nil {
		out.Reason = err.Error()
		return out
	}
	handler := map[string]any{"type": "command", "command": command, "timeout": 5}
	if h.Facts().OS == "windows" {
		script, _ := agent.NoticeHookPowerShellScript(bin, m.Spec().ID, in.ProfileID(), args...)
		handler["commandWindows"] = agent.EncodedPowerShellHook(script)
	}
	out.File = agent.JSONMovementHookFileWithHandler(h.Path().Join(in.Root(home), "hooks.json"), handler)
	out.DisabledReason = codexHookPolicy(h, in)
	if !agent.HookVersionAtLeast(in.Version, "0.160.0") {
		out.Reason = "movement hooks require a verified Codex version >= 0.160.0 (major 0)"
	}
	return out
}

// Bounded read of vendor feature switches only; no credentials, shell, or vendor
// process. Per-project and managed policy can still override these user settings.
func codexHookPolicy(h agent.Host, in agent.Install) string {
	b, err := h.FS().ReadFile(h.Path().Join(in.Root(home), "config.toml"), 1<<20)
	if errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	if err != nil {
		return "cannot read Codex hook feature settings"
	}
	type features struct {
		Hooks  *bool `toml:"hooks"`
		Legacy *bool `toml:"codex_hooks"`
	}
	var cfg struct {
		Features    features `toml:"features"`
		ManagedOnly bool     `toml:"allow_managed_hooks_only"`
		Profile     string   `toml:"profile"`
		Profiles    map[string]struct {
			Features features `toml:"features"`
		} `toml:"profiles"`
	}
	if _, err = toml.Decode(string(b), &cfg); err != nil {
		return "cannot parse Codex hook feature settings"
	}
	if cfg.ManagedOnly {
		return "Codex allow_managed_hooks_only is true; user hooks cannot deliver notices"
	}
	enabled := cfg.Features.Hooks
	if enabled == nil {
		enabled = cfg.Features.Legacy
	}
	if p, ok := cfg.Profiles[cfg.Profile]; ok {
		if p.Features.Hooks != nil {
			enabled = p.Features.Hooks
		} else if p.Features.Legacy != nil {
			enabled = p.Features.Legacy
		}
	}
	if enabled != nil && !*enabled {
		return "Codex features.hooks is false; enable hooks in Codex settings to deliver notices"
	}
	return ""
}
