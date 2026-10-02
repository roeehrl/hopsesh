// Package sessions locates and summarizes Claude Code sessions.
//
// It knows the config-dir rules (CLAUDE_CONFIG_DIR, ~/.claude, %USERPROFILE%\\.claude),
// the realpath-derived project folder slug, the per-PID live registry, sidecar
// folders, and the title / last-prompt rules used by Claude's own session picker.
// It reads only the head and tail of transcripts. Everything Claude-specific lives
// here and in package rewrite, so format drift has one place to land.
package sessions
