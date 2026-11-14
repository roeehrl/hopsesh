package codex

import (
	"slices"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// noDaemonFloor is the first Codex with --no-daemon.
const noDaemonFloor = "0.156.0"

// OwnedTabCommand runs a resumed or forked thread in a tab hopsesh owns with Codex's
// per-invocation embedded server (--no-daemon). Codex's shared daemon owns the writer
// locks on behalf of unrelated CLI clients, and its initialize handshake has no client
// PID or thread association; the embedded server lets native lock evidence still prove
// ancestry (including a native /new or /fork). Profile roots, authentication, hooks and
// commands opened in an external terminal are unchanged. Unknown and older versions stay
// as they are. Source: openai/codex rust-v0.160.1, tui/src/cli.rs and
// tui/src/daemon_startup.rs.
func (m *Module) OwnedTabCommand(in agent.Install, c agent.Command, getenv func(string) string) agent.Command {
	if len(c.Argv) < 2 || c.Argv[1] != "resume" && c.Argv[1] != "fork" {
		return c
	}
	// Version evidence belongs to this exact scoped executable, not another wrapper or a
	// bare name that PATH could resolve differently at launch.
	if in.Agent != m.Spec().ID || in.Binary == "" || !agent.HookVersionAtLeast(in.Version, noDaemonFloor) || c.Argv[0] != in.Binary || slices.Contains(c.Argv, "--no-daemon") {
		return c
	}
	// --no-daemon conflicts with an explicit remote endpoint. Never change that transport
	// or authentication choice, including an inherited endpoint not removed by the
	// already-scoped command. Vendor daemon auto-start is optional, whereas daemon-only
	// `agents` is excluded by the resume/fork gate above.
	for _, arg := range c.Argv[2:] {
		if arg == "--remote" || strings.HasPrefix(arg, "--remote=") || arg == "--remote-auth-token-env" || strings.HasPrefix(arg, "--remote-auth-token-env=") {
			return c
		}
	}
	const endpointVar = "CODEX_APP_SERVER_ADDRESS"
	endpoint := getenv(endpointVar)
	endpointName := func(name string) bool {
		return name == endpointVar || in.OS == "windows" && strings.EqualFold(name, endpointVar)
	}
	for _, name := range c.Unset {
		if endpointName(name) {
			endpoint = ""
		}
	}
	for _, env := range c.Env {
		if name, v, ok := strings.Cut(env, "="); ok && endpointName(name) {
			endpoint = v
		}
	}
	if endpoint != "" {
		return c
	}
	c.Argv = append([]string{c.Argv[0], c.Argv[1], "--no-daemon"}, c.Argv[2:]...)
	return c
}
