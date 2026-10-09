package repos

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/proc"
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
	// FromSource: the commit came straight from the other machine (it was not pushed).
	FromSource bool   `json:"fromSource,omitempty"`
	Branch     string `json:"branch,omitempty"` // branch checked out in dir
}

// HasCommit reports whether dir's repository contains commit.
func HasCommit(ctx context.Context, dir, commit string) bool {
	_, err := runGit(ctx, dir, "cat-file", "-e", commit+"^{commit}")
	return err == nil
}

// FetchSource is the other machine's repository, reachable over SSH, to fetch commits
// that were never pushed.
type FetchSource struct {
	Name string   // machine name (used in refs/hopsesh/<name>/<branch>)
	URL  string   // scp-style URL, e.g. laptop:/Users/alice/git/app
	Env  []string // e.g. GIT_SSH_COMMAND for the same SSH settings hopsesh uses
	// Bundle, when set, is used instead of URL: the other machine writes a git bundle of
	// ref, and Bundle returns a local copy of it (and how to remove it). For Windows,
	// whose OpenSSH runs git's upload-pack command through cmd.exe, which mangles it.
	Bundle func(ctx context.Context, ref string) (string, func(), error)
}

// fetch brings ref from the other machine into dst (refs/hopsesh/…).
func (f *FetchSource) fetch(ctx context.Context, dir, ref, dst string) error {
	if f.Bundle != nil {
		path, done, err := f.Bundle(ctx, ref)
		if err != nil {
			return err
		}
		defer done()
		_, err = runGit(ctx, dir, "fetch", "--quiet", "--no-tags", path, "+"+ref+":"+dst)
		return err
	}
	_, err := runGitEnv(ctx, dir, f.Env, "fetch", "--quiet", "--no-tags", f.URL, "+"+ref+":"+dst)
	return err
}

func (f *FetchSource) usable() bool { return f != nil && (f.URL != "" || f.Bundle != nil) }

// Sync brings dir up to commit when that is safe: it fetches from origin if the commit
// is not there yet, then from the other machine itself (from, when given) into
// refs/hopsesh/<machine>/<branch>, and when fastForward is set it moves a clean checkout
// on branch forward with --ff-only. It never merges, rebases, stashes or touches another
// branch.
func Sync(ctx context.Context, dir, branch, commit string, fastForward bool, from *FetchSource, excl []string) (SyncResult, error) {
	r := SyncResult{Commit: commit, Branch: CurrentBranch(ctx, dir)}
	if !HasCommit(ctx, dir, commit) {
		r.Fetched = true
		_, _ = runGit(ctx, dir, "fetch", "--quiet", "origin")
		if branch != "" {
			_, _ = runGit(ctx, dir, "fetch", "--quiet", "origin", branch)
		}
		if !HasCommit(ctx, dir, commit) && from.usable() {
			srcRef, name := "HEAD", "HEAD"
			if branch != "" {
				srcRef, name = "refs/heads/"+branch, branch
			}
			ref := "refs/hopsesh/" + safeRefPart(from.Name) + "/" + name
			if err := from.fetch(ctx, dir, srcRef, ref); err == nil {
				r.FromSource = true
			}
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
		if out, err := runGit(ctx, dir, append([]string{"status", "--porcelain", "--untracked-files=no", "--", ":/"}, excludes(excl)...)...); err != nil {
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

// Push pushes the current branch of a directory on this machine to its upstream without
// prompting, as PushScript does (and with no shell, so on Windows too). Its output is
// git's; a branch without an upstream returns ErrNoUpstream.
func Push(ctx context.Context, dir string) (string, error) {
	up := proc.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if err := up.Run(); err != nil {
		return "", ErrNoUpstream
	}
	cmd := proc.CommandContext(ctx, "git", "-C", dir, "push", "--quiet")
	cmd.Env = proc.PipeEnvironment(append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never"))
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ErrNoUpstream means the branch tracks no remote branch.
var ErrNoUpstream = errors.New("the branch has no upstream")

func safeRefPart(s string) string {
	b := []byte(s)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			b[i] = '-'
		}
	}
	return strings.Trim(string(b), ".")
}
