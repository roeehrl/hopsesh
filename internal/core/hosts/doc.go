// Package hosts discovers the user's machines and records consent.
//
// Candidates come from three local sources that never open a connection:
// `tailscale status --json`, `~/.ssh/config` aliases resolved with `ssh -G`, and
// (opt-in) Bonjour `_ssh._tcp`. A host is only contacted after the user allows it;
// trusted host-key fingerprints and cached OS/shell facts are stored per host.
package hosts
