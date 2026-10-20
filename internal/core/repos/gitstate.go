package repos

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/proc"
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
	// Error says why git could not read the folder (it did not answer in time); every other
	// field is then unknown, which is not the same as "no repository".
	Error string `json:"error,omitempty"`
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

// ProbeTimeout is how long one git call may go without answering when hopsesh reads a
// folder on this machine; RemoteProbeTimeout, on another machine over SSH. A folder whose
// files are offloaded to a cloud drive (iCloud Drive, OneDrive) or on a stuck disk can
// keep git waiting for minutes; past the limit the folder is reported as unreadable
// (GitState.Error) and the probe goes on to the next one. The limit is per git call, so a
// folder that is slow but answers (a cold disk, a large repository) is never cut off as
// long as each call returns in time. Variables only so tests can shorten them.
var (
	ProbeTimeout       = 20 * time.Second
	RemoteProbeTimeout = 30 * time.Second
)

// seconds is a limit in whole seconds (at least 1), what sh's sleep accepts.
func seconds(limit time.Duration) int {
	return max(1, int(math.Ceil(limit.Seconds())))
}

// TimeoutError is a folder's Error when git did not answer in time.
func TimeoutError(seconds string) string {
	return "git did not answer within " + seconds + " seconds; the folder may be offloaded to iCloud or OneDrive, or on a slow disk"
}

// probeScript is a POSIX sh script that prints the git state of each directory in a
// line-oriented format parsed by ParseProbe. One script per host keeps it to a single
// SSH round trip. Directories are passed as positional arguments after "--".
//
// Each folder is probed in a background subshell writing to a temporary file. Every git
// call ticks a second file as it returns, and a watchdog kills the subshell once no tick
// has come for the limit; a folder that runs out of time prints "timeout" instead of its
// partial output. Only sh, sleep, kill, wc and mktemp are needed (no timeout(1), which
// macOS lacks). A git that cannot be killed (waiting on a cloud drive) is left behind,
// holding none of the script's output, so the script still ends.
// The watchdog also checks completion: a fast folder can finish before the shell
// installs its TERM handler, so the signal alone cannot guarantee prompt cleanup.
const probeScript = `
hp_git() { git --no-optional-locks -c core.fsmonitor=false "$@"; hp_rc=$?; printf . >&3; return $hp_rc; }
hp_one() {
  d=$1
  if [ ! -d "$d" ]; then printf 'exists\t0\n'; return 0; fi
  printf 'exists\t1\n'
  top=$(hp_git -C "$d" rev-parse --show-toplevel 2>/dev/null) || { printf 'repo\t0\n'; return 0; }
  printf 'repo\t1\ntop\t%s\n' "$top"
  printf 'branch\t%s\n' "$(hp_git -C "$d" branch --show-current 2>/dev/null)"
  printf 'head\t%s\n' "$(hp_git -C "$d" rev-parse --short HEAD 2>/dev/null)"
  r=$(hp_git -C "$d" remote 2>/dev/null | head -n1)
  [ -n "$r" ] && printf 'remote\t%s\n' "$(hp_git -C "$d" config --get "remote.$r.url" 2>/dev/null)"
  up=$(hp_git -C "$d" rev-parse --abbrev-ref --symbolic-full-name '@{u}' 2>/dev/null)
  if [ -n "$up" ]; then
    printf 'upstream\t%s\n' "$up"
    printf 'aheadbehind\t%s\n' "$(hp_git -C "$d" rev-list --left-right --count '@{u}...HEAD' 2>/dev/null)"
  else
    printf 'unpushed\t%s\n' "$(hp_git -C "$d" rev-list --count HEAD --not --remotes 2>/dev/null)"
  fi
  printf 'dirty\t%s\n' "$(hp_git -C "$d" status --porcelain -- ':/'@EXCLUDES@ 2>/dev/null | wc -l | tr -d ' ')"
  gd=$(hp_git -C "$d" rev-parse --absolute-git-dir 2>/dev/null)
  cd_=$(hp_git -C "$d" rev-parse --path-format=absolute --git-common-dir 2>/dev/null)
  [ -z "$cd_" ] && cd_=$(cd "$d" && cd "$(hp_git rev-parse --git-common-dir 2>/dev/null)" 2>/dev/null && pwd)
  printf 'gitdir\t%s\ncommondir\t%s\n' "$gd" "$cd_"
  printf 'root\t%s\n' "$(hp_git -C "$d" rev-list --max-parents=0 HEAD 2>/dev/null | tail -n1)"
  hp_git -C "$d" worktree list --porcelain 2>/dev/null | sed 's/^/wt\t/'
}
hp_limit=@LIMIT@
hp_tmp=$(mktemp -d 2>/dev/null) || { hp_tmp=${TMPDIR:-/tmp}/hopsesh-probe.$$; mkdir -m 700 "$hp_tmp" 2>/dev/null || hp_tmp=; }
hp_n=0
for d in "$@"; do
  printf '@@dir\t%s\n' "$d"
  if [ -z "$hp_tmp" ]; then hp_one "$d" 3>/dev/null; continue; fi
  hp_n=$((hp_n + 1)); hp_o=$hp_tmp/$hp_n
  ( hp_one "$d"; : >"$hp_o.ok" ) >"$hp_o" 3>"$hp_o.tick" 2>/dev/null </dev/null &
  hp_p=$!
  (
    trap 'kill $hp_s 2>/dev/null; exit 0' TERM
    hp_idle=0; hp_last=
    while :; do
      [ -f "$hp_o.ok" ] && exit 0
      sleep 1 & hp_s=$!; wait $hp_s
      [ -f "$hp_o.ok" ] && exit 0
      hp_now=$(wc -c <"$hp_o.tick" 2>/dev/null)
      if [ "$hp_now" = "$hp_last" ]; then hp_idle=$((hp_idle + 1)); else hp_last=$hp_now; hp_idle=0; fi
      if [ "$hp_idle" -ge "$hp_limit" ]; then kill -9 $hp_p 2>/dev/null; exit 0; fi
    done
  ) >/dev/null 2>&1 </dev/null &
  hp_w=$!
  wait $hp_p 2>/dev/null
  kill $hp_w 2>/dev/null; wait $hp_w 2>/dev/null
  if [ -f "$hp_o.ok" ]; then cat "$hp_o"; else printf 'timeout\t%s\n' "$hp_limit"; fi
  rm -f "$hp_o" "$hp_o.ok" "$hp_o.tick"
done
[ -n "$hp_tmp" ] && rm -rf "$hp_tmp"
exit 0
`

// ProbeScript returns the shell program and its arguments for the given directories.
// excl are agent-managed worktree folders (".claude/worktrees") that never count as
// uncommitted changes; limit is how long one git call may go without answering
// (ProbeTimeout or RemoteProbeTimeout).
func ProbeScript(dirs, excl []string, limit time.Duration) (script string, args []string) {
	var b strings.Builder
	for _, p := range excludes(excl) {
		b.WriteString(" '" + strings.ReplaceAll(p, "'", `'"'"'`) + "'")
	}
	script = strings.Replace(probeScript, "@EXCLUDES@", b.String(), 1)
	script = strings.Replace(script, "@LIMIT@", strconv.Itoa(seconds(limit)), 1)
	return script, append([]string{"--"}, dirs...)
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
		case "timeout", "error":
			// Whatever the folder printed before is incomplete: keep only the reason.
			wt, gitDir, commonDir = nil, "", ""
			if cur != nil {
				*cur = GitState{Dir: cur.Dir, Error: v}
				if k == "timeout" {
					cur.Error = TimeoutError(v)
				}
			}
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

// WindowsPaths gives the folders in states the form Windows programs use: git reports
// C:/Users/…, agents record C:\Users\…, and the two must match when paths are mapped.
func WindowsPaths(states []GitState) []GitState {
	for i := range states {
		s := &states[i]
		s.Toplevel = windowsPath(s.Toplevel)
		s.MainWorktree = windowsPath(s.MainWorktree)
		for j := range s.Worktrees {
			s.Worktrees[j].Path = windowsPath(s.Worktrees[j].Path)
		}
	}
	return states
}

// windowsPath turns a drive-letter path with forward slashes (git's) into backslashes.
func windowsPath(p string) string {
	if len(p) >= 3 && p[1] == ':' && (p[2] == '/' || p[2] == '\\') {
		return strings.ReplaceAll(p, "/", `\`)
	}
	return p
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
	script, args := ProbeScript(dirs, excl, ProbeTimeout)
	cmd := proc.CommandContext(ctx, sh, append([]string{"-c", script, "hopsesh-probe"}, args[1:]...)...)
	configureProbeCancellation(cmd)
	// The script leaves a git it could not stop behind; should one still hold the output
	// open, stop waiting for it shortly after the shell has ended.
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git probe: %w", err)
	}
	if runtime.GOOS == "windows" {
		return WindowsPaths(ParseProbe(out, excl)), nil
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
