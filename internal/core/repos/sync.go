package repos

import (
	"context"
	"strconv"
	"strings"
)

// Sync states for a checkout compared with the commit a session last saw.
const (
	SyncUpToDate      = "up-to-date"     // the checkout is at that commit
	SyncFastForwarded = "fast-forwarded" // it was behind, clean and on the branch: moved forward
	SyncAhead         = "ahead"          // the checkout already contains it and has more
	SyncMissing       = "missing"        // not here even after fetching (unpushed on the source?)
	SyncDiverged      = "diverged"       // both have commits the other lacks; left alone
	SyncDirty         = "dirty"          // behind, but has uncommitted changes; left alone
	SyncOtherBranch   = "other-branch"   // checked out on a different branch; left alone
	SyncBehind        = "behind"         // behind and fast-forward was not requested
)

// SyncResult reports what Sync found or did.
type SyncResult struct {
	State   string `json:"state"`
	Commit  string `json:"commit"`            // the session's commit
	Behind  int    `json:"behind,omitempty"`  // commits the checkout was missing
	Fetched bool   `json:"fetched,omitempty"` // a fetch was needed to find the commit
	Branch  string `json:"branch,omitempty"`  // branch checked out in dir
}

// HasCommit reports whether dir's repository contains commit.
func HasCommit(ctx context.Context, dir, commit string) bool {
	_, err := runGit(ctx, dir, "cat-file", "-e", commit+"^{commit}")
	return err == nil
}

// Sync brings dir up to commit when that is safe: it fetches if the commit is not
// there yet, and when fastForward is set it moves a clean checkout on branch forward
// with --ff-only. It never merges, rebases, stashes or touches another branch.
func Sync(ctx context.Context, dir, branch, commit string, fastForward bool) (SyncResult, error) {
	r := SyncResult{Commit: commit, Branch: CurrentBranch(ctx, dir)}
	if !HasCommit(ctx, dir, commit) {
		r.Fetched = true
		_, _ = runGit(ctx, dir, "fetch", "--quiet", "origin")
		if branch != "" {
			_, _ = runGit(ctx, dir, "fetch", "--quiet", "origin", branch)
		}
		if !HasCommit(ctx, dir, commit) {
			r.State = SyncMissing
			return r, nil
		}
	}
	head, err := runGit(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return r, err
	}
	if strings.HasPrefix(head, commit) || strings.HasPrefix(commit, head) {
		r.State = SyncUpToDate
		return r, nil
	}
	if _, err := runGit(ctx, dir, "merge-base", "--is-ancestor", commit, "HEAD"); err == nil {
		r.State = SyncAhead
		return r, nil
	}
	if _, err := runGit(ctx, dir, "merge-base", "--is-ancestor", "HEAD", commit); err != nil {
		r.State = SyncDiverged
		return r, nil
	}
	if n, err := runGit(ctx, dir, "rev-list", "--count", "HEAD.."+commit); err == nil {
		r.Behind, _ = strconv.Atoi(n)
	}
	switch {
	case branch != "" && r.Branch != branch:
		r.State = SyncOtherBranch
	case !fastForward:
		r.State = SyncBehind
	default:
		if out, err := runGit(ctx, dir, "status", "--porcelain", "--untracked-files=no", "--", ":/", ":(top,exclude).claude/worktrees"); err != nil {
			return r, err
		} else if out != "" {
			r.State = SyncDirty
			return r, nil
		}
		if _, err := runGit(ctx, dir, "merge", "--ff-only", "--quiet", commit); err != nil {
			return r, err
		}
		r.State = SyncFastForwarded
	}
	return r, nil
}

// PushScript pushes the current branch of a directory to its upstream without
// prompting (run on the source machine, with its own credentials). It prints
// "no-upstream" and exits 3 when the branch has no upstream.
const PushScript = `cd "$1" || exit 2
git rev-parse --abbrev-ref --symbolic-full-name '@{u}' >/dev/null 2>&1 || { echo no-upstream; exit 3; }
GIT_TERMINAL_PROMPT=0 GCM_INTERACTIVE=never GIT_SSH_COMMAND="${GIT_SSH_COMMAND:-ssh -o BatchMode=yes}" git push --quiet 2>&1`
