package repos

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestProbeLocalWorktreesAndState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX probe; Windows uses the PowerShell probe")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	origin := filepath.Join(root, "origin.git")
	repo := filepath.Join(root, "repo")
	git(t, root, "init", "-q", "--bare", "-b", "main", origin)
	git(t, root, "clone", "-q", origin, repo)
	os.WriteFile(filepath.Join(repo, "a"), []byte("a"), 0o644)
	git(t, repo, "add", "a")
	git(t, repo, "commit", "-qm", "one")
	git(t, repo, "push", "-q", "-u", "origin", "main")
	os.WriteFile(filepath.Join(repo, "b"), []byte("b"), 0o644)
	git(t, repo, "add", "b")
	git(t, repo, "commit", "-qm", "two") // 1 ahead of origin
	os.WriteFile(filepath.Join(repo, "dirty"), []byte("x"), 0o644)
	wt := filepath.Join(repo, ".claude", "worktrees", "feat")
	git(t, repo, "worktree", "add", "-q", "-b", "feat", wt)

	states, err := ProbeLocal(context.Background(), []string{repo, wt, filepath.Join(root, "missing"), root})
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 4 {
		t.Fatalf("got %d states", len(states))
	}
	m, w, missing, notRepo := states[0], states[1], states[2], states[3]
	if !m.IsRepo || m.Branch != "main" || m.Ahead != 1 || m.Behind != 0 || m.Dirty != 1 || m.LinkedWorktree || m.Upstream != "origin/main" {
		t.Errorf("main: %+v", m)
	}
	if len(m.Worktrees) != 2 || m.MainBranch != "main" || m.MainWorktree != repo {
		t.Errorf("worktrees: %+v", m.Worktrees)
	}
	if !w.LinkedWorktree || !w.ClaudeWorktree || w.Branch != "feat" || w.Unpushed != 0 && w.Upstream == "" && w.Unpushed != 1 {
		t.Errorf("worktree: %+v", w)
	}
	if w.MainWorktree != repo || w.MainBranch != "main" {
		t.Errorf("worktree main: %q %q", w.MainWorktree, w.MainBranch)
	}
	if missing.Exists || notRepo.IsRepo || !notRepo.Exists {
		t.Errorf("missing/notRepo: %+v %+v", missing, notRepo)
	}
	if m.RootCommit == "" || m.RootCommit != w.RootCommit {
		t.Errorf("root commits: %q %q", m.RootCommit, w.RootCommit)
	}
	if Identity(m.Remote) != "" { // local bare path remote has no identity
		t.Errorf("local remote identity should be empty")
	}
}

func TestParseProbeBranchElsewhere(t *testing.T) {
	out := "@@dir\t/r\nexists\t1\nrepo\t1\ntop\t/r\nbranch\tfeat\nremote\tgit@github.com:o/r.git\n" +
		"wt\tworktree /r\nwt\tHEAD abc\nwt\tbranch refs/heads/main\nwt\t\nwt\tworktree /r/.claude/worktrees/x\nwt\tHEAD def\nwt\tbranch refs/heads/feat\n"
	s := ParseProbe([]byte(out))[0]
	if s.Identity != "github.com/o/r" || s.MainBranch != "main" {
		t.Errorf("%+v", s)
	}
	if got := s.BranchCheckedOutElsewhere(); got != "/r/.claude/worktrees/x" {
		t.Errorf("elsewhere: %q", got)
	}
}
