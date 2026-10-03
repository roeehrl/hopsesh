package repos

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/proc"
)

// DefaultRoots are the folders searched for existing checkouts, in order, after the
// configured repos folder.
func DefaultRoots(home string) []string {
	var out []string
	for _, d := range []string{"git", "src", "code", "projects", "dev", "work", "repos", "Developer", "ghq"} {
		out = append(out, filepath.Join(home, d))
	}
	return out
}

// Checkout is a local clone found by FindLocal.
type Checkout struct {
	Path   string `json:"path"`
	Remote string `json:"remote"`
}

// FindLocal returns local checkouts whose origin (or first remote) has the given identity.
// It looks at most three levels deep under each root (flat ~/git/repo and ghq-style
// ~/git/host/owner/repo layouts), skipping dot-directories and node_modules.
func FindLocal(identity string, roots []string) []Checkout {
	if identity == "" {
		return nil
	}
	var out []Checkout
	seen := map[string]bool{}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > 3 {
			return
		}
		if remote, ok := readRemote(dir); ok {
			if Identity(remote) == identity && !seen[dir] {
				seen[dir] = true
				out = append(out, Checkout{Path: dir, Remote: remote})
			}
			return // do not descend into repositories
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			n := e.Name()
			if !e.IsDir() || strings.HasPrefix(n, ".") || n == "node_modules" {
				continue
			}
			walk(filepath.Join(dir, n), depth+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	return out
}

// readRemote reports whether dir is the top of a git checkout and returns the URL of its
// "origin" remote (or the first remote) read straight from .git/config.
func readRemote(dir string) (string, bool) {
	gitPath := filepath.Join(dir, ".git")
	fi, err := os.Stat(gitPath)
	if err != nil {
		return "", false
	}
	cfg := filepath.Join(gitPath, "config")
	if !fi.IsDir() { // linked worktree or submodule: .git is a file "gitdir: <path>"
		b, err := os.ReadFile(gitPath)
		if err != nil {
			return "", true
		}
		gd := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(b)), "gitdir:"))
		if !filepath.IsAbs(gd) {
			gd = filepath.Join(dir, gd)
		}
		common := gd
		if c, err := os.ReadFile(filepath.Join(gd, "commondir")); err == nil {
			cd := strings.TrimSpace(string(c))
			if !filepath.IsAbs(cd) {
				cd = filepath.Join(gd, cd)
			}
			common = cd
		}
		cfg = filepath.Join(common, "config")
	}
	f, err := os.Open(cfg)
	if err != nil {
		return "", true
	}
	defer f.Close()
	remotes := map[string]string{}
	var order []string
	section := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		if strings.HasPrefix(section, `[remote "`) {
			k, v, ok := strings.Cut(line, "=")
			if ok && strings.TrimSpace(k) == "url" {
				name := strings.TrimSuffix(strings.TrimPrefix(section, `[remote "`), `"]`)
				if _, dup := remotes[name]; !dup {
					order = append(order, name)
				}
				remotes[name] = strings.TrimSpace(v)
			}
		}
	}
	if u, ok := remotes["origin"]; ok {
		return u, true
	}
	if len(order) > 0 {
		return remotes[order[0]], true
	}
	return "", true
}

// GitError carries git's own explanation of a failure.
type GitError struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *GitError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), msg)
}

func (e *GitError) Unwrap() error { return e.Err }

// runGit runs git without ever prompting: no terminal password prompts and SSH in batch
// mode, so a missing credential fails fast with git's own message.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	return runGitEnv(ctx, dir, nil, args...)
}

// runGitEnv is runGit with extra environment (e.g. GIT_SSH_COMMAND).
func runGitEnv(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := proc.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never"), env...)
	if os.Getenv("GIT_SSH_COMMAND") == "" && !hasEnv(env, "GIT_SSH_COMMAND") {
		cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", &GitError{Args: args, Stderr: stderr.String(), Err: err}
	}
	return strings.TrimSpace(string(out)), nil
}

// ErrDestExists means the clone destination already exists and is not empty.
var ErrDestExists = errors.New("destination already exists")

// Clone clones remote into dest (which must not exist or be empty).
func Clone(ctx context.Context, remote, dest string) error {
	if entries, err := os.ReadDir(dest); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s: %w", dest, ErrDestExists)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	_, err := runGit(ctx, filepath.Dir(dest), "clone", "--", remote, dest)
	return err
}

// CloneDest returns where to clone a repository under reposDir: flat (<reposDir>/<name>)
// or ghq-style (<reposDir>/<host>/<owner>/<repo>).
func CloneDest(reposDir, identity string, ghqLayout bool) string {
	if ghqLayout {
		return filepath.Join(append([]string{reposDir}, strings.Split(identity, "/")...)...)
	}
	return filepath.Join(reposDir, Name(identity))
}

// BranchStatus says where a branch can be found in a local checkout.
type BranchStatus struct {
	Local      bool   `json:"local"`
	Remote     bool   `json:"remote"`     // exists on origin after fetch
	CheckedOut string `json:"checkedOut"` // worktree path where it is checked out, if any
}

// InspectBranch fetches origin quietly (best effort) and reports where branch exists.
func InspectBranch(ctx context.Context, repo, branch string) BranchStatus {
	var bs BranchStatus
	_, _ = runGit(ctx, repo, "fetch", "--quiet", "origin", branch)
	if _, err := runGit(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		bs.Local = true
	}
	if _, err := runGit(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch); err == nil {
		bs.Remote = true
	}
	if out, err := runGit(ctx, repo, "worktree", "list", "--porcelain"); err == nil {
		var path string
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "worktree ") {
				path = strings.TrimPrefix(line, "worktree ")
			} else if line == "branch refs/heads/"+branch {
				bs.CheckedOut = path
			}
		}
	}
	return bs
}

// CurrentBranch returns the branch checked out in dir ("" when detached).
func CurrentBranch(ctx context.Context, dir string) string {
	b, _ := runGit(ctx, dir, "branch", "--show-current")
	return b
}

// AddWorktree creates a worktree at path for branch: from the local branch if it exists,
// otherwise tracking origin/<branch>. It never moves an existing checkout.
func AddWorktree(ctx context.Context, repo, branch, path string) error {
	bs := InspectBranch(ctx, repo, branch)
	if bs.CheckedOut != "" {
		return fmt.Errorf("branch %s is already checked out at %s", branch, bs.CheckedOut)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	switch {
	case bs.Local:
		_, err := runGit(ctx, repo, "worktree", "add", "--", path, branch)
		return err
	case bs.Remote:
		_, err := runGit(ctx, repo, "worktree", "add", "--track", "-b", branch, "--", path, "origin/"+branch)
		return err
	default:
		return fmt.Errorf("branch %s exists neither locally nor on origin (was it pushed?)", branch)
	}
}

// SwitchBranch switches the main checkout to branch, only when it has no uncommitted changes.
func SwitchBranch(ctx context.Context, repo, branch string, excl []string) error {
	if out, err := runGit(ctx, repo, append([]string{"status", "--porcelain", "--", ":/"}, excludes(excl)...)...); err != nil {
		return err
	} else if out != "" {
		return errors.New("working tree has uncommitted changes; use a worktree instead")
	}
	bs := InspectBranch(ctx, repo, branch)
	switch {
	case bs.Local:
		_, err := runGit(ctx, repo, "checkout", "--quiet", branch)
		return err
	case bs.Remote:
		_, err := runGit(ctx, repo, "checkout", "--quiet", "--track", "origin/"+branch)
		return err
	}
	return fmt.Errorf("branch %s exists neither locally nor on origin", branch)
}

func hasEnv(env []string, key string) bool {
	for _, e := range env {
		if strings.HasPrefix(e, key+"=") {
			return true
		}
	}
	return false
}
