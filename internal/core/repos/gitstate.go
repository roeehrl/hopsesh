package repos

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// Worktree is one entry of `git worktree list`.
type Worktree struct {
	Path     string `json:"path"`
	Branch   string `json:"branch,omitempty"` // short name; empty when detached
	Head     string `json:"head,omitempty"`
	Detached bool   `json:"detached,omitempty"`
	Main     bool   `json:"main,omitempty"`
}

// GitState describes the git situation of one directory on one machine.
type GitState struct {
	Dir      string `json:"dir"`
	Exists   bool   `json:"exists"`
	IsRepo   bool   `json:"isRepo"`
	Toplevel string `json:"toplevel,omitempty"`
	Branch   string `json:"branch,omitempty"` // empty when detached
	Head     string `json:"head,omitempty"`
	Remote   string `json:"remote,omitempty"`
	Identity string `json:"identity,omitempty"`
	// Upstream tracking: Ahead/Behind are relative to the upstream branch when there is one;
	// otherwise Unpushed counts commits that are on no remote branch at all.
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	Unpushed int    `json:"unpushed"`
	Dirty    int    `json:"dirty"` // changed + untracked files
	// Worktrees: whether Dir is a linked worktree (not the main checkout), whether it is one
	// of an agent's own managed worktrees (<repo>/.claude/worktrees/<name>), the main
	// checkout's path and branch, and every worktree of the repository.
	LinkedWorktree bool       `json:"linkedWorktree,omitempty"`
	AgentWorktree  bool       `json:"agentWorktree,omitempty"`
	MainWorktree   string     `json:"mainWorktree,omitempty"`
	MainBranch     string     `json:"mainBranch,omitempty"`
	Worktrees      []Worktree `json:"worktrees,omitempty"`
	RootCommit     string     `json:"rootCommit,omitempty"`
}

// BranchCheckedOutElsewhere returns the path of another worktree that has Branch checked
// out (git refuses to check a branch out twice), or "".
func (g *GitState) BranchCheckedOutElsewhere() string {
	if g.Branch == "" {
		return ""
	}
	for _, w := range g.Worktrees {
		if w.Branch == g.Branch && !samePath(w.Path, g.Toplevel) {
			return w.Path
		}
	}
	return ""
}

// LeftBehind reports work on the source that a fresh clone would not contain.
func (g *GitState) LeftBehind() (unpushed, dirty int) {
	if g.Upstream != "" {
		return g.Ahead, g.Dirty
	}
	return g.Unpushed, g.Dirty
}

func samePath(a, b string) bool {
	return strings.TrimRight(a, `/\`) == strings.TrimRight(b, `/\`)
}

// ProbeScript returns a POSIX sh script that prints the git state of each directory in a
// line-oriented format parsed by ParseProbe. One script per host keeps it to a single
// SSH round trip. Directories are passed as positional arguments after "--".
const probeScript = `
for d in "$@"; do
  printf '@@dir\t%s\n' "$d"
  if [ ! -d "$d" ]; then printf 'exists\t0\n'; continue; fi
  printf 'exists\t1\n'
  top=$(git -C "$d" rev-parse --show-toplevel 2>/dev/null) || { printf 'repo\t0\n'; continue; }
  printf 'repo\t1\ntop\t%s\n' "$top"
  printf 'branch\t%s\n' "$(git -C "$d" branch --show-current 2>/dev/null)"
  printf 'head\t%s\n' "$(git -C "$d" rev-parse --short HEAD 2>/dev/null)"
  r=$(git -C "$d" remote 2>/dev/null | head -n1)
  [ -n "$r" ] && printf 'remote\t%s\n' "$(git -C "$d" config --get "remote.$r.url" 2>/dev/null)"
  up=$(git -C "$d" rev-parse --abbrev-ref --symbolic-full-name '@{u}' 2>/dev/null)
  if [ -n "$up" ]; then
    printf 'upstream\t%s\n' "$up"
    printf 'aheadbehind\t%s\n' "$(git -C "$d" rev-list --left-right --count '@{u}...HEAD' 2>/dev/null)"
  else
    printf 'unpushed\t%s\n' "$(git -C "$d" rev-list --count HEAD --not --remotes 2>/dev/null)"
  fi
  printf 'dirty\t%s\n' "$(git -C "$d" status --porcelain -- ':/'@EXCLUDES@ 2>/dev/null | wc -l | tr -d ' ')"
  gd=$(git -C "$d" rev-parse --absolute-git-dir 2>/dev/null)
  cd_=$(git -C "$d" rev-parse --path-format=absolute --git-common-dir 2>/dev/null)
  [ -z "$cd_" ] && cd_=$(cd "$d" && cd "$(git rev-parse --git-common-dir 2>/dev/null)" 2>/dev/null && pwd)
  printf 'gitdir\t%s\ncommondir\t%s\n' "$gd" "$cd_"
  printf 'root\t%s\n' "$(git -C "$d" rev-list --max-parents=0 HEAD 2>/dev/null | tail -n1)"
  git -C "$d" worktree list --porcelain 2>/dev/null | sed 's/^/wt\t/'
done
`

// ProbeScript returns the shell program and its arguments for the given directories.
// excl are agent-managed worktree folders (".claude/worktrees") that never count as
// uncommitted changes.
func ProbeScript(dirs, excl []string) (script string, args []string) {
	var b strings.Builder
	for _, p := range excludes(excl) {
		b.WriteString(" '" + strings.ReplaceAll(p, "'", `'"'"'`) + "'")
	}
	return strings.Replace(probeScript, "@EXCLUDES@", b.String(), 1), append([]string{"--"}, dirs...)
}

// excludes turns agent worktree folders into git pathspecs.
func excludes(excl []string) []string {
	out := make([]string, len(excl))
	for i, e := range excl {
		out[i] = ":(top,exclude)" + e
	}
	return out
}

// isAgentWorktree reports whether a checkout is inside an agent-managed worktree folder.
func isAgentWorktree(top string, excl []string) bool {
	t := strings.ReplaceAll(top, `\`, "/")
	for _, e := range excl {
		if strings.Contains(t, "/"+e+"/") {
			return true
		}
	}
	return false
}

// ParseProbe parses ProbeScript output.
func ParseProbe(out []byte, excl []string) []GitState {
	var res []GitState
	var cur *GitState
	var gitDir, commonDir string
	var wt *Worktree
	flushWT := func() {
		if cur != nil && wt != nil {
			cur.Worktrees = append(cur.Worktrees, *wt)
		}
		wt = nil
	}
	finish := func() {
		flushWT()
		if cur == nil {
			return
		}
		if len(cur.Worktrees) > 0 {
			cur.Worktrees[0].Main = true
			cur.MainWorktree = cur.Worktrees[0].Path
			cur.MainBranch = cur.Worktrees[0].Branch
		}
		if gitDir != "" && commonDir != "" && !samePath(gitDir, commonDir) {
			cur.LinkedWorktree = true
		}
		cur.AgentWorktree = isAgentWorktree(cur.Toplevel, excl)
		cur.Identity = Identity(cur.Remote)
		res = append(res, *cur)
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		line := sc.Text()
		k, v, _ := strings.Cut(line, "\t")
		switch k {
		case "@@dir":
			finish()
			cur = &GitState{Dir: v}
			gitDir, commonDir = "", ""
		case "exists":
			cur.Exists = v == "1"
		case "repo":
			cur.IsRepo = v == "1"
		case "top":
			cur.Toplevel = v
		case "branch":
			cur.Branch = v
		case "head":
			cur.Head = v
		case "remote":
			cur.Remote = v
		case "upstream":
			cur.Upstream = v
		case "aheadbehind":
			f := strings.Fields(v)
			if len(f) == 2 {
				cur.Behind, _ = strconv.Atoi(f[0])
				cur.Ahead, _ = strconv.Atoi(f[1])
			}
		case "unpushed":
			cur.Unpushed, _ = strconv.Atoi(strings.TrimSpace(v))
		case "dirty":
			cur.Dirty, _ = strconv.Atoi(strings.TrimSpace(v))
		case "gitdir":
			gitDir = v
		case "commondir":
			commonDir = v
		case "root":
			cur.RootCommit = v
		case "wt":
			wk, wv, _ := strings.Cut(v, " ")
			switch wk {
			case "worktree":
				flushWT()
				wt = &Worktree{Path: wv}
			case "HEAD":
				if wt != nil {
					wt.Head = wv
				}
			case "branch":
				if wt != nil {
					wt.Branch = strings.TrimPrefix(wv, "refs/heads/")
				}
			case "detached":
				if wt != nil {
					wt.Detached = true
				}
			}
		}
	}
	finish()
	return res
}

// ProbeLocal runs the probe on this machine.
func ProbeLocal(ctx context.Context, dirs, excl []string) ([]GitState, error) {
	if len(dirs) == 0 {
		return nil, nil
	}
	sh, err := findSh()
	if err != nil {
		return nil, err
	}
	script, args := ProbeScript(dirs, excl)
	cmd := exec.CommandContext(ctx, sh, append([]string{"-c", script, "hopsesh-probe"}, args[1:]...)...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git probe: %w", err)
	}
	return ParseProbe(out, excl), nil
}

func findSh() (string, error) {
	if p, err := exec.LookPath("sh"); err == nil {
		return p, nil
	}
	if runtime.GOOS == "windows" {
		for _, p := range []string{`C:\Program Files\Git\bin\sh.exe`, `C:\Program Files\Git\usr\bin\sh.exe`} {
			if _, err := exec.LookPath(p); err == nil {
				return p, nil
			}
		}
	}
	return "", errors.New("no POSIX shell found (install Git for Windows)")
}
