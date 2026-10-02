// Package hosts discovers the user's machines without connecting to any of them.
//
// Candidates come from `tailscale status --json` (phones and expired nodes dropped,
// machines shared by other people marked) and from `~/.ssh/config` aliases (following
// Include; Match blocks are left to ssh). The two lists are merged by address and name.
// Consent and host-key trust are recorded elsewhere (config and package transport); a
// machine is contacted only after the user allows it.
package hosts
