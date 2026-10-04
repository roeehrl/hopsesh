package app

import (
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/move"
)

// Which branches hopsesh offers to delete, and why (not): only merged ones whose cloud
// deletes after merge, never one that is gone, kept by choice, or not merged yet.
func TestCleanupDecision(t *testing.T) {
	brought := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name   string
		cand   BranchCandidate
		facts  BranchFacts
		offer  bool
		reason string
	}{
		{"merged", BranchCandidate{Policy: move.CleanupAfterMerge, CloudTitle: "Codex cloud"}, BranchFacts{Exists: true, Merged: true, By: "in its history", Into: "main"},
			true, "merged into main (in its history)"},
		{"merged and brought", BranchCandidate{Policy: move.CleanupAfterMerge, Brought: brought}, BranchFacts{Exists: true, Merged: true, Into: "main"},
			true, "merged into main; its work came here"},
		{"by pull request", BranchCandidate{Policy: move.CleanupAfterMerge}, BranchFacts{Exists: true, Merged: true, By: "pull request #12 was merged", Into: "trunk"},
			true, "merged into trunk (pull request #12 was merged)"},
		{"not merged", BranchCandidate{Policy: move.CleanupAfterMerge}, BranchFacts{Exists: true, Into: "main"}, false, "not merged into main yet"},
		{"gone", BranchCandidate{Policy: move.CleanupAfterMerge}, BranchFacts{Merged: true}, false, "gone from the remote already"},
		{"never", BranchCandidate{Policy: move.CleanupNever, CloudTitle: "Jules"}, BranchFacts{Exists: true, Merged: true}, false, "kept: delete_branch is never for Jules"},
		{"on undo", BranchCandidate{Policy: move.CleanupOnUndo, CloudTitle: "Claude Code cloud"}, BranchFacts{Exists: true, Merged: true}, false,
			"kept until you undo it: delete_branch is on-undo for Claude Code cloud"},
		{"unreachable", BranchCandidate{Policy: move.CleanupAfterMerge}, BranchFacts{Error: "could not read from remote"}, false,
			"hopsesh could not ask the remote: could not read from remote"},
		{"no default branch", BranchCandidate{Policy: move.CleanupAfterMerge}, BranchFacts{Exists: true}, false, "not merged into the default branch yet"},
	} {
		offer, why := CleanupDecision(c.cand, c.facts)
		if offer != c.offer || !strings.HasPrefix(why, c.reason) {
			t.Errorf("%s: %v %q", c.name, offer, why)
		}
	}
}
