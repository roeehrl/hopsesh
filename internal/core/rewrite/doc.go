// Package rewrite adapts a copied session to the target machine.
//
// It applies a line-preserving prefix map (repo root, home, config dir) that is
// correct for JSON-escaped Windows paths, never edits uuid/parentUuid/sessionId or
// signed thinking blocks (dropping them whole only for cross-account moves), strips
// the source's Remote Control link record, and appends a `relocated` record the way
// Claude's own /cd does.
package rewrite
