package repos

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFindLocalCloneWorktree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX git fixtures")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	origin := filepath.Join(root, "origin.git")
	seed := filepath.Join(root, "seed")
	git(t, root, "init", "-q", "--bare", "-b", "main", origin)
	git(t, root, "clone", "-q", origin, seed)
	os.WriteFile(filepath.Join(seed, "a"), []byte("a"), 0o644)
	git(t, seed, "add", "a")
	git(t, seed, "commit", "-qm", "1")
	git(t, seed, "push", "-q", "-u", "origin", "main")
	git(t, seed, "checkout", "-qb", "feat")
	git(t, seed, "push", "-q", "-u", "origin", "feat")
	// a checkout whose remote is a GitHub URL, found by identity in a ghq-style tree
	ghq := filepath.Join(root, "git", "github.com", "owner", "proj")
	os.MkdirAll(filepath.Join(ghq, ".git"), 0o755)
	os.WriteFile(filepath.Join(ghq, ".git", "config"), []byte("[core]\n[remote \"origin\"]\n\turl = git@github.com:Owner/proj.git\n"), 0o644)
	found := FindLocal("github.com/owner/proj", []string{filepath.Join(root, "git")})
	if len(found) != 1 || found[0].Path != ghq {
		t.Fatalf("FindLocal: %+v", found)
	}

	dest := CloneDest(filepath.Join(root, "repos"), "github.com/owner/proj", false)
	if err := Clone(ctx, origin, dest); err != nil {
		t.Fatal(err)
	}
	if err := Clone(ctx, origin, dest); !errors.Is(err, ErrDestExists) {
		t.Errorf("second clone: %v", err)
	}
	var ge *GitError
	if err := Clone(ctx, filepath.Join(root, "nope.git"), filepath.Join(root, "x")); !errors.As(err, &ge) || ge.Stderr == "" {
		t.Errorf("clone failure must carry git's reason: %v", err)
	}
	wt := filepath.Join(dest, ".claude", "worktrees", "feat")
	if err := AddWorktree(ctx, dest, "feat", wt); err != nil {
		t.Fatal(err)
	}
	if CurrentBranch(ctx, wt) != "feat" {
		t.Error("worktree not on feat")
	}
	if err := AddWorktree(ctx, dest, "feat", filepath.Join(root, "wt2")); err == nil {
		t.Error("checking out a branch twice must fail")
	}
	if err := AddWorktree(ctx, dest, "ghost", filepath.Join(root, "wt3")); err == nil {
		t.Error("unknown branch must fail")
	}
	if err := SwitchBranch(ctx, dest, "main", []string{".claude/worktrees"}); err != nil {
		t.Errorf("checkout main: %v", err)
	}
}
