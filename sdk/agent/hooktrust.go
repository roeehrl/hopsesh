package agent

import "context"

// HookTrustReporter is for agents that run user hooks only after the user trusts them
// (Codex reviews each hook definition and skips it silently until trusted). It asks the
// agent itself, never re-implementing its trust hash, and never trusts on the user's
// behalf: approving a hook stays in the agent's own review UI.
type HookTrustReporter interface {
	HookTrust(ctx context.Context, h Host, in Install, commands []string) HookTrust
}

// Hook trust states.
const (
	HookTrusted     = "trusted"      // every hopsesh hook runs
	HookNeedsReview = "needs-review" // new or changed: the agent skips it until the user trusts it
	HookDisabled    = "disabled"     // the user turned a hopsesh hook off in the agent
	HookMissing     = "missing"      // the agent does not see the hook at all
	HookUnknown     = "unknown"      // the agent could not be asked
)

// HookTrust is what the agent reports for the hopsesh hooks.
type HookTrust struct {
	State  string   `json:"state"`
	Events []string `json:"events,omitempty"` // hooks in State (other than trusted)
	Fix    string   `json:"fix,omitempty"`    // what the user does, in the agent
	Detail string   `json:"detail,omitempty"`
}
