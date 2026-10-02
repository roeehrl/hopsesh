// Package transport runs commands and reads files on remote machines.
//
// It drives the system OpenSSH client (BatchMode, ControlMaster, strict host-key
// checking against hopsesh's own known_hosts plus the user's), so the user's keys,
// agent, ProxyJump, ProxyCommand, certificates and config apply unchanged. Files are
// read over SFTP on a separate compressed connection. Windows machines run PowerShell
// through -EncodedCommand. When a host name does not resolve, or macOS local network
// privacy blocks it, the machine's Tailscale name is tried. On macOS the app probes
// gated destinations in-process first (package lnp) so the system prompt appears for
// hopsesh and a denial can be explained.
package transport
