package repos_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/repos"
)

// handoffRepo is a checkout of a bare origin with a commit on main, then work in progress:
// a commit not pushed, a staged change, an unstaged change, a deleted file, untracked files
// (one of them credential-like) and a changed .env that is tracked.
func handoffRepo(t *testing.T) (origin, work string) {
	t.Helper()
	root := t.TempDir()
	origin, work = filepath.Join(root, "origin.git"), filepath.Join(root, "work")
	gitIn(t, root, "init", "-q", "--bare", "-b", "main", origin)
	gitIn(t, root, "clone", "-q", origin, work)
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(work, p)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(work, p), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", "demo\n")
	write("parser/quote.go", "package parser\n")
	write("old.txt", "old\n")
	write(".env", "TOKEN=one\n")
	write(".gitattributes", "*.bin filter=lfs diff=lfs merge=lfs -text\n")
	write("big.bin", "pointer\n")
	gitIn(t, work, "add", "-A")
	gitIn(t, work, "commit", "-qm", "init")
	gitIn(t, work, "push", "-q", "-u", "origin", "main")
	write("parser/quote.go", "package parser\n\nfunc Quote() {}\n")
	gitIn(t, work, "commit", "-qam", "unpushed work")
	write("README.md", "demo, staged\n")
	gitIn(t, work, "add", "README.md")
	write("README.md", "demo, staged, then more\n")
	write("parser/quote_test.go", "package parser\n")
	gitIn(t, work, "add", "parser/quote_test.go")
	if err := os.Remove(filepath.Join(work, "old.txt")); err != nil {
		t.Fatal(err)
	}
	write(".env", "TOKEN=two\n")
	write("big.bin", "pointer, changed\n")
	write("docs/notes.md", "notes\n")
	write("certs/dev.pem", "-----BEGIN PRIVATE KEY-----\n")
	write("id_ed25519", "key\n")
	return origin, work
}

// state is everything of the user's that a snapshot and a push must leave as it was.
type state struct {
	index, head, branch, status string
	files                       map[string]string
}

func stateOf(t *testing.T, work string) state {
	t.Helper()
	idx, err := os.ReadFile(filepath.Join(work, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	st := state{index: string(idx), head: gitIn(t, work, "rev-parse", "HEAD"), branch: gitIn(t, work, "symbolic-ref", "HEAD"),
		status: gitIn(t, work, "--no-optional-locks", "status", "--porcelain=v1", "--untracked-files=all"), files: map[string]string{}}
	err = filepath.WalkDir(work, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			fi, _ := d.Info()
			st.files[p] = string(b) + "|" + fi.ModTime().String()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func sameState(t *testing.T, before, after state) {
	t.Helper()
	switch {
	case before.index != after.index:
		t.Error("the user's index changed")
	case before.head != after.head:
		t.Errorf("HEAD moved: %s → %s", before.head, after.head)
	case before.branch != after.branch:
		t.Errorf("the branch changed: %s → %s", before.branch, after.branch)
	case before.status != after.status:
		t.Errorf("git status changed:\n%s\n---\n%s", before.status, after.status)
	}
	if len(before.files) != len(after.files) {
		t.Errorf("the working tree has %d files, had %d", len(after.files), len(before.files))
	}
	for p, b := range before.files {
		if after.files[p] != b {
			t.Errorf("%s changed", p)
		}
	}
}

// The plan carries the changed tracked files and the chosen untracked ones; credential-like
// names, LFS files and files over the limit stay, even when chosen.
func TestPlanSnapshot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX paths in the fixture")
	}
	_, work := handoffRepo(t)
	ctx := context.Background()
	before := stateOf(t, work)
	p, err := repos.PlanSnapshot(ctx, repos.Here{}, work, repos.SnapshotOptions{Include: []string{"docs/notes.md", "*.pem", "id_ed25519"}})
	if err != nil {
		t.Fatal(err)
	}
	sameState(t, before, stateOf(t, work))
	if p.Head != gitIn(t, work, "rev-parse", "HEAD") || p.Branch != "main" {
		t.Fatalf("head: %+v", p)
	}
	if got := strings.Join(p.Tracked, ","); got != "README.md,old.txt,parser/quote_test.go" {
		t.Errorf("tracked: %s", got)
	}
	if got := strings.Join(p.Untracked, ","); got != "docs/notes.md" {
		t.Errorf("untracked: %s", got)
	}
	withheld := map[string]string{}
	for _, w := range p.Withheld {
		withheld[w.Path] = w.Why
	}
	for path, why := range map[string]string{".env": repos.WithheldCredential, "certs/dev.pem": repos.WithheldCredential, "id_ed25519": repos.WithheldCredential, "big.bin": repos.WithheldLFS} {
		if withheld[path] != why {
			t.Errorf("%s: withheld %q, want %q (%+v)", path, withheld[path], why, p.Withheld)
		}
	}
	// Not chosen: offered, unchecked.
	p, _ = repos.PlanSnapshot(ctx, repos.Here{}, work, repos.SnapshotOptions{})
	if len(p.Untracked) != 0 || len(p.Offered) != 1 || p.Offered[0].Path != "docs/notes.md" || p.Offered[0].Size != 6 {
		t.Errorf("untracked must wait to be chosen: %+v %+v", p.Untracked, p.Offered)
	}
	// Over the size limit.
	p, _ = repos.PlanSnapshot(ctx, repos.Here{}, work, repos.SnapshotOptions{Include: []string{"docs/*"}, MaxFile: 5})
	for _, w := range p.Withheld {
		if w.Path == "docs/notes.md" && w.Why != repos.WithheldSize {
			t.Errorf("a large file: %+v", w)
		}
	}
	for _, name := range []string{".env", ".env.local", "a/b/.npmrc", "x.key", "y.p12", "prod.tfvars", "id_rsa.pub", "deploy/key.pem"} {
		if !repos.Denied(name, nil) {
			t.Errorf("%s must be withheld", name)
		}
	}
	if repos.Denied("environment.go", nil) || repos.Denied("keys.md", nil) {
		t.Error("ordinary files are not withheld")
	}
}

// A snapshot commit holds the working tree's version of what the plan carries (and the
// conversation file when asked), on HEAD, with the one trailer; the user's index, working
// tree, branch and HEAD stay byte for byte as they were, through the push too.
func TestSnapshotAndPushLeaveTheCheckoutAlone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX paths in the fixture")
	}
	origin, work := handoffRepo(t)
	ctx := context.Background()
	g := repos.Here{}
	before := stateOf(t, work)
	p, err := repos.PlanSnapshot(ctx, g, work, repos.SnapshotOptions{Include: []string{"docs/notes.md", "certs/dev.pem"}})
	if err != nil {
		t.Fatal(err)
	}
	sha, err := repos.Snapshot(ctx, g, work, p, repos.SnapshotMessage("0b6c6a8e-lineage"), repos.ExtraFile{Path: ".hopsesh/handoff.md", Data: []byte("# history\n")})
	if err != nil {
		t.Fatal(err)
	}
	sameState(t, before, stateOf(t, work))
	if parent := gitIn(t, work, "rev-parse", sha+"^"); parent != p.Head {
		t.Errorf("the snapshot builds on %s, not HEAD", parent)
	}
	msg := gitIn(t, work, "log", "-1", "--format=%B", sha)
	if msg != "hopsesh handoff snapshot\n\nHopsesh-Handoff: 0b6c6a8e-lineage" {
		t.Errorf("message: %q", msg)
	}
	if tr := gitIn(t, work, "log", "-1", "--format=%(trailers:key=Hopsesh-Handoff,valueonly)", sha); tr != "0b6c6a8e-lineage" {
		t.Errorf("trailer: %q", tr)
	}
	files := gitIn(t, work, "ls-tree", "-r", "--name-only", sha)
	for _, want := range []string{"README.md", "parser/quote.go", "parser/quote_test.go", "docs/notes.md", ".hopsesh/handoff.md", ".env", "big.bin"} {
		if !strings.Contains("\n"+files+"\n", "\n"+want+"\n") {
			t.Errorf("%s missing from the snapshot:\n%s", want, files)
		}
	}
	for _, gone := range []string{"old.txt", "certs/dev.pem", "id_ed25519"} {
		if strings.Contains("\n"+files+"\n", "\n"+gone+"\n") {
			t.Errorf("%s must not be in the snapshot", gone)
		}
	}
	if got := gitIn(t, work, "show", sha+":README.md"); got != "demo, staged, then more" {
		t.Errorf("README.md is the working tree's: %q", got)
	}
	if got := gitIn(t, work, "show", sha+":.env"); got != "TOKEN=one" {
		t.Errorf("the tracked .env keeps HEAD's content, not the change: %q", got)
	}
	// HEAD's blob: a pointer where git-lfs is installed, the file itself where it is not.
	if got, want := gitIn(t, work, "rev-parse", sha+":big.bin"), gitIn(t, work, "rev-parse", "HEAD:big.bin"); got != want {
		t.Errorf("the LFS file keeps HEAD's content: %s, not %s", got, want)
	}
	if refs := gitIn(t, work, "for-each-ref", "--contains", sha); refs != "" {
		t.Errorf("no ref holds the snapshot before the push: %s", refs)
	}

	ref := "refs/heads/" + repos.HandoffBranch("", "20261004", "0b6c6a8e-1d2f")
	if ref != "refs/heads/hopsesh/handoff/20261004-0b6c6a8e" {
		t.Fatal(ref)
	}
	if err := repos.PushRef(ctx, g, work, "origin", sha, ref); err != nil {
		t.Fatal(err)
	}
	sameState(t, before, stateOf(t, work))
	if got := gitIn(t, origin, "rev-parse", ref); got != sha {
		t.Fatalf("origin has %s", got)
	}
	if err := repos.PushRef(ctx, g, work, "origin", p.Head, ref); !errors.Is(err, repos.ErrRefExists) {
		t.Fatalf("a second push to the same branch must refuse: %v", err)
	}
	if got := gitIn(t, origin, "rev-parse", ref); got != sha {
		t.Fatal("the refused push moved the branch")
	}
	if next := repos.FreeRemoteBranch(ctx, g, work, "origin", "hopsesh/handoff/20261004-0b6c6a8e"); next != "hopsesh/handoff/20261004-0b6c6a8e-2" {
		t.Errorf("free name: %s", next)
	}
}

// Undo deletes the pushed branch with a lease: the journal refuses while the cloud has
// moved it on, and deletes it when forced; a branch already gone is fine.
func TestDeleteRefLease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX paths in the fixture")
	}
	origin, work := handoffRepo(t)
	ctx := context.Background()
	g := repos.Here{}
	p, _ := repos.PlanSnapshot(ctx, g, work, repos.SnapshotOptions{})
	sha, err := repos.Snapshot(ctx, g, work, p, repos.SnapshotMessage("x"))
	if err != nil {
		t.Fatal(err)
	}
	const ref = "refs/heads/hopsesh/handoff/20261004-lease"
	state := t.TempDir()
	j, err := journal.New(state, journal.KindHandoff, "hand-off")
	if err != nil {
		t.Fatal(err)
	}
	if err := j.PushRef("here", work, "origin", ref, sha); err != nil {
		t.Fatal(err)
	}
	if err := repos.PushRef(ctx, g, work, "origin", sha, ref); err != nil {
		t.Fatal(err)
	}
	// The cloud pushes on top.
	other := filepath.Join(t.TempDir(), "cloud")
	gitIn(t, filepath.Dir(other), "clone", "-q", "--branch", strings.TrimPrefix(ref, "refs/heads/"), origin, other)
	if err := os.WriteFile(filepath.Join(other, "cloud.md"), []byte("cloud\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, other, "add", "-A")
	gitIn(t, other, "commit", "-qm", "cloud work")
	gitIn(t, other, "push", "-q", "origin", "HEAD:"+ref)
	moved := gitIn(t, other, "rev-parse", "HEAD")

	if err := repos.DeleteRef(ctx, g, work, "origin", ref, sha); !errors.Is(err, repos.ErrRefMoved) {
		t.Fatalf("delete with a stale lease: %v", err)
	}
	reach := journal.Reach{Refs: repos.Refs{Machines: func(context.Context, string) (repos.Git, error) { return g, nil }}}
	if err := j.Changed(ctx, reach); !errors.Is(err, journal.ErrChanged) || !strings.Contains(err.Error(), "moved on") {
		t.Fatalf("changed: %v", err)
	}
	if err := j.Undo(ctx, reach, false); !errors.Is(err, journal.ErrChanged) {
		t.Fatalf("undo must refuse: %v", err)
	}
	if got := gitIn(t, origin, "rev-parse", ref); got != moved {
		t.Fatal("the refused undo touched the branch")
	}
	if err := j.Undo(ctx, reach, true); err != nil {
		t.Fatal(err)
	}
	if out := gitIn(t, origin, "for-each-ref", ref); out != "" {
		t.Fatalf("a forced undo deletes the branch: %s", out)
	}
	if err := repos.DeleteRef(ctx, g, work, "origin", ref, sha); err != nil {
		t.Fatalf("a branch already gone: %v", err)
	}
}

// The driver's folder is a new worktree on the branch: the user's checkout stays on its
// own branch, and a branch made for it goes again afterwards.
func TestDriverDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX paths in the fixture")
	}
	_, work := handoffRepo(t)
	ctx := context.Background()
	g := repos.Here{}
	p, _ := repos.PlanSnapshot(ctx, g, work, repos.SnapshotOptions{})
	sha, _ := repos.Snapshot(ctx, g, work, p, repos.SnapshotMessage("x"))
	const branch = "hopsesh/handoff/20261004-driver"
	if err := repos.PushRef(ctx, g, work, "origin", sha, "refs/heads/"+branch); err != nil {
		t.Fatal(err)
	}
	before := stateOf(t, work)
	dir, done, err := repos.DriverDir(ctx, work, "", branch, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := repos.CurrentBranch(ctx, dir); got != branch {
		t.Fatalf("the driver's folder is on %s", got)
	}
	if up := gitIn(t, dir, "rev-parse", "--abbrev-ref", "@{u}"); up != "origin/"+branch {
		t.Errorf("upstream %s", up)
	}
	done()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("the folder stays")
	}
	if repos.BranchExists(ctx, work, branch) {
		t.Error("the branch made for the driver stays")
	}
	after := stateOf(t, work)
	sameState(t, before, after)
	// The checkout's own branch, checked out there already.
	dir, done, err = repos.DriverDir(ctx, work, "", "main", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := repos.CurrentBranch(ctx, dir); got != "main" {
		t.Fatalf("on %s", got)
	}
	done()
	if !repos.BranchExists(ctx, work, "main") || !bytes.Equal([]byte(repos.CurrentBranch(ctx, work)), []byte("main")) {
		t.Error("the user's branch must stay")
	}
}
