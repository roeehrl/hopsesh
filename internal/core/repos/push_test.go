package repos

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Push pushes the branch to its upstream, and says when there is none.
func TestPush(t *testing.T) {
	root := t.TempDir()
	origin, work := filepath.Join(root, "origin.git"), filepath.Join(root, "work")
	gitOut(t, root, "init", "-q", "--bare", "-b", "main", origin)
	gitOut(t, root, "clone", "-q", origin, work)
	os.WriteFile(filepath.Join(work, "a"), []byte("a"), 0o644)
	gitOut(t, work, "add", "a")
	gitOut(t, work, "commit", "-qm", "a")
	ctx := context.Background()
	if _, err := Push(ctx, work); !errors.Is(err, ErrNoUpstream) {
		t.Fatalf("no upstream yet: %v", err)
	}
	gitOut(t, work, "push", "-q", "-u", "origin", "main")
	os.WriteFile(filepath.Join(work, "b"), []byte("b"), 0o644)
	gitOut(t, work, "add", "b")
	gitOut(t, work, "commit", "-qm", "b")
	if out, err := Push(ctx, work); err != nil {
		t.Fatalf("push: %v %s", err, out)
	}
	if head, remote := gitOut(t, work, "rev-parse", "HEAD"), gitOut(t, origin, "rev-parse", "main"); head != remote {
		t.Fatal("origin must have the new commit")
	}
}
