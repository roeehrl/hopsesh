package engine

import (
	"fmt"

	"github.com/roeehrl/hopsesh/internal/core/repos"
)

// Marking states in a plan.
const (
	MarkNow         = "now"          // right after the move
	MarkWhenStopped = "when-stopped" // the old session is still running: on a later scan
	MarkOff         = "off"
)

// planRoundTrip decides the round-trip steps: marking the copy left behind, pushing on
// the source, and bringing the checkout here to the session's commit.
func planRoundTrip(p *Plan, src Source, tgt Target, in Input, opt Options) {
	switch {
	case !opt.MarkSource || src.Host == tgt.Host:
		p.Mark = MarkOff
	case opt.Fork && p.Live:
		p.Mark = MarkOff // both copies continue on purpose
	case p.Live:
		p.Mark = MarkWhenStopped
	default:
		p.Mark = MarkNow
	}
	if in.Summary != nil && in.Summary.MovedTo != "" {
		p.Warnings = append(p.Warnings, fmt.Sprintf("this copy on %s was already moved to %s; the newest copy is probably there", src.Host, in.Summary.MovedTo))
	}
	r := p.Repo
	if opt.PushSource && r.Unpushed > 0 && src.Push != nil && src.Host != tgt.Host {
		if r.HasUpstream {
			p.Push = true
		} else {
			p.Warnings = append(p.Warnings, fmt.Sprintf("branch %s on %s has no upstream, so hopsesh cannot push it; push it yourself", r.SourceBranch, src.Host))
		}
	}
	if opt.SyncCode && r.SourceHead != "" && (r.Action == "use" || r.Action == "clone" || r.Action == "dir") {
		short := r.SourceHead
		if len(short) > 7 {
			short = short[:7]
		}
		p.Sync = fmt.Sprintf("bring the checkout here to %s (fetch if needed; fast-forward only if it is clean and on %s)", short, nonEmpty(r.SourceBranch, "the same branch"))
	}
}

// syncDir is the directory whose checkout should reach the session's commit.
func syncDir(p *Plan) string {
	if p.Repo.Worktree != "" {
		return p.Repo.Worktree
	}
	return p.TargetCWD
}

// describeSync turns a sync result into a line for the user.
func describeSync(r repos.SyncResult, branch, sourceHost string) string {
	short := r.Commit
	if len(short) > 7 {
		short = short[:7]
	}
	switch r.State {
	case repos.SyncUpToDate:
		return "the checkout is at the session's commit " + short
	case repos.SyncFastForwarded:
		return fmt.Sprintf("fast-forwarded the checkout by %d commit(s) to %s", r.Behind, short)
	case repos.SyncAhead:
		return "the checkout already contains the session's commit " + short + " (and more)"
	case repos.SyncMissing:
		return fmt.Sprintf("commit %s is not here, even after fetching: push it on %s, then run `git pull` here", short, sourceHost)
	case repos.SyncDiverged:
		return fmt.Sprintf("the checkout and %s have diverged from %s; merge or rebase yourself", sourceHost, short)
	case repos.SyncDirty:
		return fmt.Sprintf("the checkout is %d commit(s) behind %s but has uncommitted changes; commit or stash, then `git pull --ff-only`", r.Behind, short)
	case repos.SyncOtherBranch:
		return fmt.Sprintf("the checkout is on %s, not %s; switch branches to get commit %s", r.Branch, branch, short)
	case repos.SyncBehind:
		return fmt.Sprintf("the checkout is %d commit(s) behind %s; run `git pull --ff-only`", r.Behind, short)
	}
	return r.State
}
