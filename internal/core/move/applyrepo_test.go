package move

import (
	"context"
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

func gitT(t *testing.T, dir string, args ...string) string {
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

// A move that cloned the repository and added a worktree is undone completely: the
// worktree, the branch made for it, the clone and the folders the clone made go. A clone
// with work of its own stays, and undo says so.
func TestUndoRemovesCloneAndWorktree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX paths in the fixture")
	}
	ctx := context.Background()
	root := t.TempDir()
	origin, seed := filepath.Join(root, "origin.git"), filepath.Join(root, "seed")
	gitT(t, root, "init", "-q", "--bare", "-b", "main", origin)
	gitT(t, root, "init", "-q", "-b", "main", seed)
	gitT(t, seed, "remote", "add", "origin", origin)
	gitT(t, seed, "commit", "-q", "--allow-empty", "-m", "init")
	gitT(t, seed, "push", "-q", "origin", "main")
	gitT(t, seed, "checkout", "-q", "-b", "feature")
	gitT(t, seed, "commit", "-q", "--allow-empty", "-m", "work")
	gitT(t, seed, "push", "-q", "origin", "feature")

	move := func(t *testing.T, name string) (*journal.Journal, string, string) {
		clone := filepath.Join(root, name, "src", "demo")
		wt := filepath.Join(root, name, "src", "demo.worktrees", "feature")
		p := &Plan{Repo: RepoPlan{Action: RepoClone, Remote: origin, LocalPath: clone, Worktree: wt, SourceBranch: "feature"}}
		in := Input{Target: Side{Machine: &host.Machine{Name: "here", Local: true}}}
		j, err := journal.New(t.TempDir(), journal.KindMove, "test")
		if err != nil {
			t.Fatal(err)
		}
		if err := applyRepo(ctx, p, in, Env{}, j, &Result{}, func(string) {}); err != nil {
			t.Fatal(err)
		}
		if b := repos.CurrentBranch(ctx, wt); b != "feature" {
			t.Fatalf("the worktree is on %q", b)
		}
		return j, clone, wt
	}
	reach := journal.Reach{FS: func(string) (host.FS, error) { return host.LocalFS(), nil }, Git: repos.LocalGit{}}

	t.Run("nothing new", func(t *testing.T) {
		j, clone, wt := move(t, "a")
		if err := j.Undo(ctx, reach, false); err != nil {
			t.Fatal(err)
		}
		for _, p := range []string{wt, clone, filepath.Join(root, "a")} {
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Fatalf("%s is still there", p)
			}
		}
		if len(j.Left) != 0 || !j.Undone {
			t.Fatalf("left %v, undone %v", j.Left, j.Undone)
		}
	})
	for name, work := range map[string]func(t *testing.T, clone string){
		"ignored file": func(t *testing.T, clone string) {
			os.WriteFile(filepath.Join(clone, ".gitignore"), []byte(".env\n"), 0o600)
			gitT(t, clone, "add", ".gitignore")
			gitT(t, clone, "commit", "-q", "-m", "ignore")
			gitT(t, clone, "push", "-q", "origin", "main")
			os.WriteFile(filepath.Join(clone, ".env"), []byte("TOKEN=x\n"), 0o600)
		},
		"unpushed commit": func(t *testing.T, clone string) {
			gitT(t, clone, "commit", "-q", "--allow-empty", "-m", "mine")
		},
		"stash": func(t *testing.T, clone string) {
			os.WriteFile(filepath.Join(clone, "wip.md"), []byte("wip\n"), 0o600)
			gitT(t, clone, "stash", "-q", "-u")
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := strings.ReplaceAll(name, " ", "-")
			j, clone, wt := move(t, dir)
			work(t, clone)
			if err := j.Undo(ctx, reach, true); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(wt); !os.IsNotExist(err) {
				t.Fatal("the worktree is still there")
			}
			if _, err := os.Stat(filepath.Join(clone, ".git")); err != nil {
				t.Fatalf("a clone with work was removed: %v", err)
			}
			if len(j.Left) != 1 || !strings.HasPrefix(j.Left[0], clone+": ") || !j.Undone {
				t.Fatalf("left %v, undone %v", j.Left, j.Undone)
			}
		})
	}
}
