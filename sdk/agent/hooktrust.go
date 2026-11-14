package agent

import "context"

// HookTrustReporter is for agents that can say whether the hopsesh hooks they have
// installed actually run (Codex lists each hook with its trust; OpenCode lists the plugins
// it loaded). It asks the agent itself, never re-implementing its trust hash, and never
// trusts on the user's behalf.
type HookTrustReporter interface {
	HookTrust(ctx context.Context, h Host, in Install, commands []string) HookTrust
}

// HookApprover marks an agent that runs a new or changed hook only after the user approves
// it in the agent (Codex reviews each hook definition and skips it silently until
// trusted). Approving stays in the agent's own review UI; hopsesh only reports it. Where
// the agent cannot be asked (a machine read over SSH), such a hook's state is unknown.
// An agent that reports trust without an approval step (OpenCode) does not implement it.
type HookApprover interface {
	ApprovesHooks()
}

// ApprovesHooks reports whether m runs a new hook only after the user approves it there.
func ApprovesHooks(m Module) bool {
	_, ok := m.(HookApprover)
	return ok
}

// Hook trust states.
const (
	HookTrusted     = "trusted"      // every hopsesh hook runs
	HookNeedsReview = "needs-review" // new or changed: the agent skips it until the user trusts it
	HookDisabled    = "disabled"     // the user turned a hopsesh hook off in the agent
	HookMissing     = "missing"      // the agent does not see (or does not load) the hook at all
	HookUnknown     = "unknown"      // the agent could not be asked
)

// HookTrust is what the agent reports for the hopsesh hooks.
type HookTrust struct {
	State  string   `json:"state"`
	Events []string `json:"events,omitempty"` // hooks in State (other than trusted)
	Fix    string   `json:"fix,omitempty"`    // what the user does, in the agent
	Detail string   `json:"detail,omitempty"`
	// Failures counts the hook's calls that failed and let the agent go on (a hook fails
	// open), where the agent keeps that count (OpenCode's plugin); Detail then says when
	// the last one was and why.
	Failures int `json:"failures,omitempty"`
	// Excluded marks a missing hook that the agent leaves out by its own settings (a
	// config entry or a policy removes OpenCode's plugin), or has not reloaded since it
	// was installed: installing it again does not change that. Another missing hook (one
	// that failed to load, or another of the same name runs) is installed again.
	Excluded bool `json:"excluded,omitempty"`
}
