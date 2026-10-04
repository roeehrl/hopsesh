package repos_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/repos"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Sam Doe", "GIT_AUTHOR_EMAIL=sam@example.com", "GIT_COMMITTER_NAME=Sam Doe", "GIT_COMMITTER_EMAIL=sam@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// A cloud's branch comes home into refs/hopsesh/<cloud>/…, in a worktree of its own, and
// its vendor name gives way to hopsesh/from/<cloud>/…; the journal undoes all of it, and
// refuses while the worktree holds changes.
func TestCloudBranchHomeAndUndo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX paths in the fixture")
	}
	ctx := context.Background()
	root := t.TempDir()
	origin, repo := filepath.Join(root, "origin.git"), filepath.Join(root, "demo")
	gitIn(t, root, "init", "-q", "--bare", "-b", "main", origin)
	gitIn(t, root, "init", "-q", "-b", "main", repo)
	gitIn(t, repo, "remote", "add", "origin", origin)
	gitIn(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	gitIn(t, repo, "push", "-q", "origin", "main")
	gitIn(t, repo, "checkout", "-q", "-b", "claude/web-session-abc123")
	gitIn(t, repo, "commit", "-q", "--allow-empty", "-m", "cloud work")
	gitIn(t, repo, "push", "-q", "origin", "claude/web-session-abc123")
	gitIn(t, repo, "checkout", "-q", "main")
	gitIn(t, repo, "branch", "-q", "-D", "claude/web-session-abc123")

	if _, _, err := repos.FetchCloudBranch(ctx, repo, "claude-cloud", "claude/never-pushed"); !errors.Is(err, repos.ErrNotPushed) {
		t.Fatalf("an unpushed branch: %v", err)
	}
	ref, sha, err := repos.FetchCloudBranch(ctx, repo, "claude-cloud", "claude/web-session-abc123")
	if err != nil || ref != "refs/hopsesh/claude-cloud/claude/web-session-abc123" || len(sha) != 40 {
		t.Fatalf("fetch: %s %s %v", ref, sha, err)
	}
	j, err := journal.New(t.TempDir(), journal.KindFetch, "test")
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(j.Ref("here", repo, ref, sha, ""))
	wt := filepath.Join(root, "demo-cloud")
	must(j.Worktree("here", repo, wt))
	must(repos.AddWorktreeAt(ctx, repo, wt, "", sha))
	// The vendor's checkout in the worktree, then the rename.
	gitIn(t, wt, "checkout", "-q", "-b", "claude/web-session-abc123")
	to := repos.FromBranch("claude-cloud", "claude/web-session-abc123", "claude/")
	if to != "hopsesh/from/claude-cloud/web-session-abc123" {
		t.Fatal(to)
	}
	must(repos.LocalGit{}.RenameBranch(ctx, repo, "refs/heads/claude/web-session-abc123", "refs/heads/"+to))
	must(j.RenamedBranch("here", repo, "refs/heads/claude/web-session-abc123", "refs/heads/"+to, sha, true))
	if b := repos.CurrentBranch(ctx, wt); b != to {
		t.Fatalf("the worktree is on %s", b)
	}
	reach := journal.Reach{FS: func(string) (host.FS, error) { return host.LocalFS(), nil }, Git: repos.LocalGit{}}
	must(os.WriteFile(filepath.Join(wt, "notes.md"), []byte("wip\n"), 0o600))
	if err := j.Changed(ctx, reach); !errors.Is(err, journal.ErrChanged) || !strings.Contains(err.Error(), "not committed") {
		t.Fatalf("a worktree with changes: %v", err)
	}
	must(os.Remove(filepath.Join(wt, "notes.md")))
	must(j.Undo(ctx, reach, false))
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("the worktree is still there")
	}
	if repos.BranchExists(ctx, repo, to) || repos.BranchExists(ctx, repo, "claude/web-session-abc123") {
		t.Fatal("the branch made for it is still there")
	}
	if now, _ := (repos.LocalGit{}).Ref(ctx, repo, ref); now != "" {
		t.Fatal("the fetched ref is still there")
	}
	if b := repos.CurrentBranch(ctx, repo); b != "main" {
		t.Fatalf("the user's checkout moved to %s", b)
	}
}
