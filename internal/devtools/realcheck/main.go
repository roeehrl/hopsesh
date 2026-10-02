// Command realcheck validates the session reader against the real ~/.claude on this
// machine: every project folder name must equal Slug(cwd) for the cwd its transcripts
// record (or the relocated cwd), and summaries must have titles. Read-only.
package main

import (
	"fmt"
	"os"

	"github.com/roeehrl/hopsesh/internal/core/fsys"
	"github.com/roeehrl/hopsesh/internal/core/sessions"
)

func main() {
	cfg, err := sessions.LocalConfigDir()
	if err != nil {
		panic(err)
	}
	loc := sessions.Locator{FS: fsys.Local{}, ConfigDir: cfg}
	list, err := loc.List(sessions.ListOptions{})
	if err != nil {
		panic(err)
	}
	match, mismatch, noTitle, noPrompt := 0, 0, 0, 0
	seenDirs := map[string]bool{}
	for _, s := range list {
		if s.Title == "" {
			noTitle++
		}
		if s.LastPrompt == "" {
			noPrompt++
		}
		if s.CWD == "" || seenDirs[s.ProjectDir+"|"+s.CWD] {
			continue
		}
		seenDirs[s.ProjectDir+"|"+s.CWD] = true
		if ok(s) {
			match++
		} else {
			mismatch++
			fmt.Printf("MISMATCH dir=%s slug(cwd)=%s cwd=%s\n", s.ProjectDir, sessions.Slug(s.CWD), s.CWD)
		}
	}
	live, _ := loc.LiveRegistry(sessions.LocalAlive)
	fmt.Printf("sessions=%d dir/cwd pairs: match=%d mismatch=%d  noTitle=%d noLastPrompt=%d  live=%d\n",
		len(list), match, mismatch, noTitle, noPrompt, len(live))
	if mismatch > 0 {
		os.Exit(1)
	}
}

// ok accepts the two layouts Claude Code uses: the folder of the project cwd, or, for a
// session that entered a Claude worktree later, the folder of the worktree's repo root.
func ok(s *sessions.Summary) bool {
	for _, c := range []string{s.CWD, s.WorktreeRoot} {
		if c != "" && (sessions.ResolvedSlug(c) == s.ProjectDir || sessions.Slug(c) == s.ProjectDir) {
			return true
		}
	}
	return false
}
