package move

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// workspaceInput is a session on a laptop in /Users/sam/git/course, a folder holding
// course-core and course-sdk rather than being a repository, continued here (home).
func workspaceInput(home string) Input {
	member := func(name string, unpushed, dirty int) repos.GitState {
		top := "/Users/sam/git/course/" + name
		remote := "git@github.com:acme/" + name + ".git"
		return repos.GitState{Dir: top, Exists: true, IsRepo: true, Toplevel: top, MainWorktree: top, Branch: "main",
			Remote: remote, Identity: repos.Identity(remote), Unpushed: unpushed, Dirty: dirty}
	}
	return Input{
		Source:  Side{Machine: &host.Machine{Name: "laptop", Facts: host.Facts{OS: "darwin", Home: "/Users/sam"}}},
		Target:  Side{Machine: &host.Machine{Name: "studio", Local: true, Facts: host.Facts{OS: runtime.GOOS, Home: home}}},
		Session: agent.Summary{CWD: "/Users/sam/git/course"},
		Git:     &repos.GitState{Dir: "/Users/sam/git/course", Exists: true, Children: []string{"/Users/sam/git/course/course-core", "/Users/sam/git/course/course-sdk"}},
		Workspace: []repos.GitState{
			member("course-core", 2, 1),
			member("course-sdk", 0, 0),
		},
	}
}

func TestWorkspaceFolderPlansEachRepository(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX paths in the fixture")
	}
	ctx := context.Background()
	home := t.TempDir()
	ws := filepath.Join(home, "git", "course")
	core := filepath.Join(ws, "course-core")
	if err := os.MkdirAll(core, 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, core, "init", "-q", "-b", "main")
	gitT(t, core, "remote", "add", "origin", "https://github.com/acme/course-core.git")
	in := workspaceInput(home)

	t.Run("one here, one to clone", func(t *testing.T) {
		p := &Plan{}
		cwd, err := planRepo(ctx, p, in, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if cwd != ws || p.Repo.Action != RepoWorkspace || p.Repo.LocalPath != ws || len(p.Repo.Members) != 2 {
			t.Fatalf("cwd %s, repo %+v", cwd, p.Repo)
		}
		c, s := p.Repo.Members[0], p.Repo.Members[1]
		if c.Action != RepoUse || c.LocalPath != core || c.Name() != "course-core" {
			t.Errorf("course-core: %+v", c)
		}
		if s.Action != RepoNeedsClone || s.LocalPath != filepath.Join(ws, "course-sdk") {
			t.Errorf("course-sdk: %+v", s)
		}
		if len(p.Blockers) != 1 || !strings.HasPrefix(p.Blockers[0], "course-sdk is not cloned on this machine; clone it into "+ws) {
			t.Errorf("blockers: %q", p.Blockers)
		}
		warned := strings.Join(p.Warnings, "\n")
		if !strings.Contains(warned, "on laptop course-core has 2 unpushed commit(s) and 1 uncommitted file(s)") || strings.Contains(warned, "course-sdk has") {
			t.Errorf("warnings: %q", p.Warnings)
		}
		if !strings.Contains(warned, "holds 2 repositories on laptop") {
			t.Errorf("no word that only the repositories come: %q", p.Warnings)
		}
		// Paths in the conversation: each repository, then the folder, then the home.
		p.Source.CWD, p.Target.CWD = in.Session.CWD, cwd
		ms := map[string]string{}
		for _, m := range mappings(p, in.Source, in.Target) {
			ms[m.From] = m.To
		}
		if ms["/Users/sam/git/course/course-core"] != core || ms["/Users/sam/git/course"] != ws || ms["/Users/sam"] != home {
			t.Errorf("mappings: %v", ms)
		}
	})
	t.Run("clone", func(t *testing.T) {
		p := &Plan{}
		if _, err := planRepo(ctx, p, in, Options{Clone: true}); err != nil {
			t.Fatal(err)
		}
		if len(p.Blockers) != 0 || p.Repo.Members[1].Action != RepoClone {
			t.Fatalf("blockers %q, members %+v", p.Blockers, p.Repo.Members)
		}
	})
	t.Run("elsewhere here", func(t *testing.T) {
		h := t.TempDir()
		other := filepath.Join(h, "src", "course-core")
		os.MkdirAll(other, 0o755)
		gitT(t, other, "init", "-q", "-b", "main")
		gitT(t, other, "remote", "add", "origin", "git@github.com:acme/course-core.git")
		p := &Plan{}
		if _, err := planRepo(ctx, p, workspaceInput(h), Options{Clone: true}); err != nil {
			t.Fatal(err)
		}
		if m := p.Repo.Members[0]; m.Action != RepoUse || m.LocalPath != other {
			t.Fatalf("course-core: %+v", m)
		}
		if !strings.Contains(strings.Join(p.Warnings, "\n"), "course-core is at "+other+" here, outside "+filepath.Join(h, "git", "course")) {
			t.Errorf("warnings: %q", p.Warnings)
		}
	})
}

// A folder that is not a repository and holds none continues in the same folder under
// this machine's home, never in another user's home.
func TestPlainFolderMovesUnderThisHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX paths in the fixture")
	}
	home := t.TempDir()
	in := workspaceInput(home)
	in.Workspace, in.Git.Children = nil, nil
	in.Session.CWD = "/Users/sam/notes"
	p := &Plan{}
	cwd, err := planRepo(context.Background(), p, in, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "notes")
	if cwd != want || p.Repo.Action != RepoNone || len(p.Blockers) != 1 || !strings.Contains(p.Blockers[0], want+" is not on this machine; choose a folder here with --to") {
		t.Fatalf("cwd %s, blockers %q", cwd, p.Blockers)
	}
	os.Mkdir(want, 0o755)
	p = &Plan{}
	if cwd, _ = planRepo(context.Background(), p, in, Options{}); cwd != want || len(p.Blockers) != 0 {
		t.Fatalf("an existing folder: cwd %s, blockers %q", cwd, p.Blockers)
	}
	if got := underHome("/srv/data", "/Users/sam", home); got != "/srv/data" {
		t.Errorf("outside the home: %s", got)
	}
	if got := underHome("/Users/sam", "/Users/sam", home); got != home {
		t.Errorf("the home itself: %s", got)
	}
}

// Applying a workspace clones the missing repositories into the folder (or makes the
// folder when nothing is cloned), and undo takes it all away again.
func TestApplyWorkspaceAndUndo(t *testing.T) {
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
	reach := journal.Reach{FS: func(string) (host.FS, error) { return host.LocalFS(), nil }, Git: repos.LocalGit{}}
	in := Input{Target: Side{Machine: &host.Machine{Name: "here", Local: true}}}

	for name, members := range map[string][]RepoPlan{
		"clone": {{Action: RepoClone, Remote: origin, SourceTop: "/Users/sam/git/course/course-sdk"}},
		"none":  {{Action: RepoUse, LocalPath: seed, SourceTop: "/Users/sam/git/course/seed"}},
	} {
		t.Run(name, func(t *testing.T) {
			ws := filepath.Join(root, name, "git", "course")
			for i := range members {
				if members[i].Action == RepoClone {
					members[i].LocalPath = filepath.Join(ws, members[i].Name())
				}
			}
			p := &Plan{Repo: RepoPlan{Action: RepoWorkspace, LocalPath: ws, Members: members}}
			j, err := journal.New(t.TempDir(), journal.KindMove, "test")
			if err != nil {
				t.Fatal(err)
			}
			res := &Result{}
			if err := applyRepo(ctx, p, in, Env{}, j, res, func(string) {}); err != nil {
				t.Fatal(err)
			}
			if fi, err := os.Stat(ws); err != nil || !fi.IsDir() {
				t.Fatalf("no workspace folder: %v", err)
			}
			if name == "clone" {
				if _, err := os.Stat(filepath.Join(ws, "course-sdk", ".git")); err != nil || !res.Cloned {
					t.Fatalf("not cloned: %v", err)
				}
			}
			if err := j.Undo(ctx, reach, false); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
				t.Fatalf("undo left %s", filepath.Join(root, name))
			}
			if _, err := os.Stat(seed); err != nil {
				t.Fatal("undo touched a checkout it did not make")
			}
		})
	}
}

// The warning for continuing under another login names what is true: another account
// when the emails differ, separate runtime profiles only for the same email.
func TestOtherEmail(t *testing.T) {
	in := Input{Source: Side{Account: &agent.Account{Email: "sam@example.com"}}, Target: Side{Account: &agent.Account{Email: "kim@example.com"}}}
	if !otherEmail(in) {
		t.Error("different emails")
	}
	in.Target.Account.Email = "SAM@example.com"
	if otherEmail(in) {
		t.Error("the same email")
	}
	in.Target.Account = nil
	if otherEmail(in) {
		t.Error("an unknown account")
	}
}
