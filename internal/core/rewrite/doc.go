// Package rewrite adapts a copied session to the target machine.
//
// It applies a single-pass prefix map (repo root, worktree, home, config dir) to every
// JSON string in the transcript, keys included, on path boundaries. It handles
// JSON-escaped Windows paths and paths that follow JSON escapes, converts separators
// below mapped folders between Windows and macOS/Linux, and never edits ids or signed
// thinking blocks (dropping them whole only for cross-account moves). It strips the
// source's Remote Control link record, optionally redacts secrets, and appends a
// `relocated` record the way Claude Code's own /cd does.
package rewrite
