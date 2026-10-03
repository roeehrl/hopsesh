package repos

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestSync: origin gets a new commit (pushed from "the other machine"); the local
// checkout is behind, then dirty, then diverged.
func TestSync(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	other := filepath.Join(root, "other")
	here := filepath.Join(root, "here")
	gitOut(t, root, "init", "--quiet", "--bare", "-b", "main", origin)
	gitOut(t, root, "clone", "--quiet", origin, other)
	os.WriteFile(filepath.Join(other, "a.txt"), []byte("1"), 0o644)
	gitOut(t, other, "add", ".")
	gitOut(t, other, "commit", "--quiet", "-m", "one")
	gitOut(t, other, "push", "--quiet", "origin", "HEAD:main")
	gitOut(t, root, "clone", "--quiet", origin, here)

	os.WriteFile(filepath.Join(other, "a.txt"), []byte("2"), 0o644)
	gitOut(t, other, "commit", "--quiet", "-am", "two")
	two := gitOut(t, other, "rev-parse", "HEAD")

	// Not pushed yet: missing even after fetch.
	if r, err := Sync(ctx, here, "main", two, true, nil, nil); err != nil || r.State != SyncMissing || !r.Fetched {
		t.Fatalf("unpushed: %+v %v", r, err)
	}
	// Still unpushed, but fetched straight from the other machine's repository.
	from := &FetchSource{Name: "other", URL: other}
	if r, err := Sync(ctx, here, "main", two, false, from, nil); err != nil || !r.FromSource || r.State != SyncBehind {
		t.Fatalf("from source: %+v %v", r, err)
	}
	if gitOut(t, here, "rev-parse", "refs/hopsesh/other/main") != two {
		t.Fatal("the source branch is kept under refs/hopsesh/other/main")
	}
	gitOut(t, other, "push", "--quiet", "origin", "HEAD:main")
	// Without fast-forward: behind.
	if r, _ := Sync(ctx, here, "main", two, false, nil, nil); r.State != SyncBehind || r.Behind != 1 {
		t.Fatalf("behind: %+v", r)
	}
	// Dirty checkout is left alone.
	os.WriteFile(filepath.Join(here, "a.txt"), []byte("local edit"), 0o644)
	if r, _ := Sync(ctx, here, "main", two, true, nil, nil); r.State != SyncDirty {
		t.Fatalf("dirty: %+v", r)
	}
	gitOut(t, here, "checkout", "--quiet", "--", "a.txt")
	// Clean: fast-forwarded.
	if r, err := Sync(ctx, here, "main", two, true, nil, nil); err != nil || r.State != SyncFastForwarded {
		t.Fatalf("ff: %+v %v", r, err)
	}
	if r, _ := Sync(ctx, here, "main", two, true, nil, nil); r.State != SyncUpToDate {
		t.Fatalf("up to date: %+v", r)
	}
	// Local commit on top: ahead.
	os.WriteFile(filepath.Join(here, "b.txt"), []byte("x"), 0o644)
	gitOut(t, here, "add", ".")
	gitOut(t, here, "commit", "--quiet", "-m", "local")
	if r, _ := Sync(ctx, here, "main", two, true, nil, nil); r.State != SyncAhead {
		t.Fatalf("ahead: %+v", r)
	}
	// The other machine moves on too: diverged.
	os.WriteFile(filepath.Join(other, "c.txt"), []byte("y"), 0o644)
	gitOut(t, other, "add", ".")
	gitOut(t, other, "commit", "--quiet", "-m", "three")
	gitOut(t, other, "push", "--quiet", "origin", "HEAD:main")
	three := gitOut(t, other, "rev-parse", "HEAD")
	if r, _ := Sync(ctx, here, "main", three, true, nil, nil); r.State != SyncDiverged {
		t.Fatalf("diverged: %+v", r)
	}
	// A different branch checked out: left alone.
	gitOut(t, here, "checkout", "--quiet", "-b", "feat")
	gitOut(t, here, "reset", "--quiet", "--hard", two)
	if r, _ := Sync(ctx, here, "main", three, true, nil, nil); r.State != SyncOtherBranch {
		t.Fatalf("other branch: %+v", r)
	}
}
