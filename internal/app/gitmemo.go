package app

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/repos"
)

// gitRefresh is how long a folder's git state is reused when nothing points to a
// change: an agent working there or (on this machine) git's own files changing.
const gitRefresh = 10 * time.Minute

type gitProbed struct {
	state  repos.GitState
	at     time.Time
	marker string // this machine: git's HEAD, index and refs versions
}

var gitStates = &boundedMemo[gitProbed]{limit: 2048}

// gitMarker versions what changes a repository's state on this machine. It cannot see
// edits to tracked files that nothing has staged yet; the refresh interval covers those.
func gitMarker(st repos.GitState) string {
	if !st.IsRepo || st.Toplevel == "" {
		return ""
	}
	dot := filepath.Join(st.Toplevel, ".git")
	gitDir := dot
	if b, err := os.ReadFile(dot); err == nil { // a linked worktree's .git file
		gitDir = strings.TrimSpace(strings.TrimPrefix(string(b), "gitdir:"))
	}
	common := gitDir
	if b, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		if c := strings.TrimSpace(string(b)); filepath.IsAbs(c) {
			common = c
		} else {
			common = filepath.Join(gitDir, c)
		}
	}
	var b strings.Builder
	for _, p := range []string{st.Toplevel, filepath.Join(gitDir, "HEAD"), filepath.Join(gitDir, "index"), filepath.Join(common, "packed-refs"), filepath.Join(common, "refs", "heads"), filepath.Join(common, "refs", "remotes")} {
		if info, err := os.Stat(p); err == nil {
			b.WriteString(p + "\x00" + info.ModTime().String() + "\x00" + string(rune(info.Size())) + "\n")
		}
	}
	return b.String()
}

// reusableGit returns a folder's earlier git state when nothing suggests it changed.
func reusableGit(machine string, local bool, dir string, activity time.Time) (repos.GitState, bool) {
	p, ok := gitStates.get(machine + "\x00" + dir)
	if !ok || p.state.Error != "" || time.Since(p.at) >= gitRefresh || activity.After(p.at) {
		return repos.GitState{}, false
	}
	if local && gitMarker(p.state) != p.marker {
		return repos.GitState{}, false
	}
	return p.state, true
}

func rememberGit(machine string, local bool, st repos.GitState, at time.Time) {
	p := gitProbed{state: st, at: at}
	if local {
		p.marker = gitMarker(st)
	}
	gitStates.put(machine+"\x00"+st.Dir, p)
}
