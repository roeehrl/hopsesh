// Package transport runs commands and moves files on remote hosts.
//
// It drives the system OpenSSH `ssh` and `sftp` binaries (BatchMode, ControlMaster,
// strict host-key checking) so the user's keys, agents, ProxyJump, certificates and
// config apply unchanged. Transfers are verified with SHA-256 on both ends, written
// atomically, and never convert line endings. Windows remotes (cmd.exe by default)
// are detected and quoted correctly.
package transport
