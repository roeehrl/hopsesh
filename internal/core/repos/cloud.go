package repos

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Bringing a cloud session's code home: its branch is fetched into
// refs/hopsesh/<cloud>/<branch>, worked on in a worktree of its own (the user's checkout
// stays as it is), and a branch the cloud's agent named (claude/…) is kept under
// hopsesh/from/<cloud>/… instead.

// ErrNotPushed means a branch is not on the remote.
var ErrNotPushed = errors.New("the branch is not on the remote")

// CloudRef is where a cloud's branch is fetched to: refs/hopsesh/<cloud>/<branch>.
func CloudRef(cloud, branch string) string {
	parts := strings.Split(branch, "/")
	for i, p := range parts {
		parts[i] = safeRefPart(p)
	}
	return "refs/hopsesh/" + safeRefPart(cloud) + "/" + strings.Join(parts, "/")
}

// FromBranch is the name a cloud's own branch gets here: hopsesh/from/<cloud>/<branch
// without the vendor's prefix>.
func FromBranch(cloud, branch, vendorPrefix string) string {
	rest := strings.TrimPrefix(branch, vendorPrefix)
	return "hopsesh/from/" + safeRefPart(cloud) + "/" + rest
}

// RemoteBranch returns the commit branch points at on origin ("" when it is not there).
func RemoteBranch(ctx context.Context, dir, branch string) (string, error) {
	out, err := runGit(ctx, dir, "ls-remote", "--heads", "origin", "refs/heads/"+branch)
	if err != nil {
		return "", err
	}
	if f := strings.Fields(out); len(f) > 0 {
		return f[0], nil
	}
	return "", nil
}

// FetchCloudBranch fetches a cloud's branch from origin into CloudRef and returns the ref
// and its commit; ErrNotPushed when origin does not have it.
func FetchCloudBranch(ctx context.Context, dir, cloud, branch string) (ref, sha string, err error) {
	ref = CloudRef(cloud, branch)
	if _, err := runGit(ctx, dir, "fetch", "--quiet", "--no-tags", "origin", "+refs/heads/"+branch+":"+ref); err != nil {
		if remote, lerr := RemoteBranch(ctx, dir, branch); lerr == nil && remote == "" {
			return ref, "", fmt.Errorf("%w: %s", ErrNotPushed, branch)
		}
		return ref, "", err
	}
	sha, err = runGit(ctx, dir, "rev-parse", "--verify", "--quiet", ref)
	return ref, sha, err
}

// Head returns the commit HEAD points at in dir.
func Head(ctx context.Context, dir string) (string, error) {
	return runGit(ctx, dir, "rev-parse", "--verify", "--quiet", "HEAD")
}

// AddWorktreeAt adds a worktree at path for the checkout repo, at commit: on a new branch
// when branch is set, detached otherwise.
func AddWorktreeAt(ctx context.Context, repo, path, branch, commit string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	args := []string{"worktree", "add", "--detach", "--", path, commit}
	if branch != "" {
		args = []string{"worktree", "add", "-b", branch, "--", path, commit}
	}
	_, err := runGit(ctx, repo, args...)
	return err
}

// BranchExists reports whether a local branch exists in dir.
func BranchExists(ctx context.Context, dir, branch string) bool {
	_, err := runGit(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// FreeBranchName is name, or name-2, name-3, … when a branch of that name exists.
func FreeBranchName(ctx context.Context, dir, name string) string {
	n := name
	for i := 2; BranchExists(ctx, dir, n) && i < 100; i++ {
		n = fmt.Sprintf("%s-%d", name, i)
	}
	return n
}

// Branches returns the local branches under a prefix, with their commits.
func Branches(ctx context.Context, dir, prefix string) (map[string]string, error) {
	out, err := runGit(ctx, dir, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/"+prefix)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if ref, sha, ok := strings.Cut(strings.TrimSpace(l), " "); ok {
			m[ref] = sha
		}
	}
	return m, nil
}

// LocalGit is git on this machine, for the journal's undo of worktrees and refs.
type LocalGit struct{}

// Ref returns the commit ref points at ("" when there is no such ref or checkout).
func (LocalGit) Ref(ctx context.Context, dir, ref string) (string, error) {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	out, err := runGit(ctx, dir, "rev-parse", "--verify", "--quiet", ref)
	if err != nil {
		var ge *GitError
		if errors.As(err, &ge) && strings.TrimSpace(ge.Stderr) == "" {
			return "", nil // --quiet: no such ref
		}
		return "", err
	}
	return out, nil
}

// SetRef sets ref to sha ("" deletes it) while it points at expect.
func (LocalGit) SetRef(ctx context.Context, dir, ref, sha, expect string) error {
	if sha == "" {
		_, err := runGit(ctx, dir, "update-ref", "-d", ref, expect)
		return err
	}
	_, err := runGit(ctx, dir, "update-ref", ref, sha, expect)
	return err
}

// RenameBranch renames a branch (full ref names).
func (LocalGit) RenameBranch(ctx context.Context, dir, from, to string) error {
	_, err := runGit(ctx, dir, "branch", "-m", strings.TrimPrefix(from, "refs/heads/"), strings.TrimPrefix(to, "refs/heads/"))
	return err
}

// WorktreeDirty reports whether a worktree has uncommitted changes or untracked files
// (false when it is gone).
func (LocalGit) WorktreeDirty(ctx context.Context, worktree string) (bool, error) {
	if _, err := os.Stat(worktree); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	out, err := runGit(ctx, worktree, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// RemoveWorktree removes a worktree of the checkout dir (one already gone is pruned).
func (LocalGit) RemoveWorktree(ctx context.Context, dir, worktree string, force bool) error {
	if _, err := os.Stat(worktree); errors.Is(err, os.ErrNotExist) {
		_, err := runGit(ctx, dir, "worktree", "prune")
		return err
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	_, err := runGit(ctx, dir, append(args, "--", worktree)...)
	return err
}
