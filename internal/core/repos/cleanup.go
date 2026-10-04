package repos

import (
	"context"
	"strings"
)

// Cleaning up branches after their work was merged: what the remote's default branch is,
// and whether a branch's commit is in it. Every call reads; nothing here writes a ref.

// DefaultBranch is the remote's default branch and the commit it points at, as the remote
// says (ls-remote --symref <remote> HEAD); "" when the remote names none.
func DefaultBranch(ctx context.Context, g Git, dir, remote string) (branch, sha string, err error) {
	out, err := g.Run(ctx, dir, pushEnv, "ls-remote", "--symref", remote, "HEAD")
	if err != nil {
		return "", "", err
	}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		switch {
		case len(f) == 3 && f[0] == "ref:" && f[2] == "HEAD":
			branch = strings.TrimPrefix(f[1], "refs/heads/")
		case len(f) == 2 && f[1] == "HEAD":
			sha = f[0]
		}
	}
	return branch, sha, nil
}

// HaveCommit makes sure the checkout has the commit sha, fetching ref from remote (into
// FETCH_HEAD only, no ref of the user's) when it does not.
func HaveCommit(ctx context.Context, g Git, dir, remote, ref, sha string) error {
	if _, err := g.Run(ctx, dir, nil, "cat-file", "-e", sha+"^{commit}"); err == nil {
		return nil
	}
	_, err := g.Run(ctx, dir, pushEnv, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", remote, ref)
	if err != nil {
		return err
	}
	_, err = g.Run(ctx, dir, nil, "cat-file", "-e", sha+"^{commit}")
	return err
}

// InHistory reports whether commit sha is in the history of commit into (merged by a merge
// or a fast-forward; a squash or a rebase leaves a different commit).
func InHistory(ctx context.Context, g Git, dir, sha, into string) bool {
	_, err := g.Run(ctx, dir, nil, "merge-base", "--is-ancestor", sha, into)
	return err == nil
}
