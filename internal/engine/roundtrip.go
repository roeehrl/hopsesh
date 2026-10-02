package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/fsys"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/internal/core/rewrite"
	"github.com/roeehrl/hopsesh/internal/core/sessions"
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
	case p.OriginalID != "":
		p.Mark = MarkOff // both copies stay
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
		if r.Unpushed > 0 && src.GitFetch != nil && src.Host != tgt.Host && !p.Push {
			p.SyncFromSource = true
			p.Sync += fmt.Sprintf("; the %d unpushed commit(s) are fetched straight from %s", r.Unpushed, src.Host)
		}
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
		if r.FromSource {
			return fmt.Sprintf("fast-forwarded the checkout by %d commit(s) to %s, fetched straight from %s (not pushed yet)", r.Behind, short, sourceHost)
		}
		return fmt.Sprintf("fast-forwarded the checkout by %d commit(s) to %s", r.Behind, short)
	case repos.SyncAhead:
		return "the checkout already contains the session's commit " + short + " (and more)"
	case repos.SyncMissing:
		return fmt.Sprintf("commit %s is not here, even after fetching from origin and from %s: push it on %s, then run `git pull` here", short, sourceHost, sourceHost)
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

// planConflict compares the copy already here with the incoming one. A copy that was
// left here by a handoff (marked moved, nothing added since) is simply replaced. A copy
// that kept changing after it was marked, or that is newer than the incoming one, is a
// conflict: refused unless the user chose to replace it or to keep both.
func planConflict(p *Plan, s *sessions.Summary, opt Options) {
	var conflict string
	for _, d := range p.Duplicates {
		here, err := sessions.Summarize(fsys.Local{}, d)
		if err != nil {
			continue
		}
		after, _ := sessions.TurnsAfterMoveMark(fsys.Local{}, d)
		switch {
		case after == 0:
			continue // left behind by a move: replace it
		case after > 0:
			conflict = fmt.Sprintf("the copy here was marked as moved to %s, but %d message(s) were added to it afterwards", here.MovedTo, after)
		case here.LastActivity.After(s.LastActivity.Add(time.Minute)):
			conflict = fmt.Sprintf("the copy here is newer (last active %s) than the one on %s (%s)",
				here.LastActivity.Local().Format("2 Jan 15:04"), p.SourceHost, s.LastActivity.Local().Format("2 Jan 15:04"))
		}
	}
	if conflict == "" {
		return
	}
	p.Conflict = conflict
	switch opt.Conflict {
	case "replace":
		p.Warnings = append(p.Warnings, conflict+"; it will be replaced (hopsesh undo brings it back)")
	case "keep-both":
		keepBoth(p)
	default:
		p.Blockers = append(p.Blockers, conflict+"; choose --replace (the copy here is kept for undo) or --keep-both (bring this one in as a separate session)")
	}
}

// keepBoth brings the incoming copy in under a new session id, so both copies stay.
func keepBoth(p *Plan) {
	old, nw := p.SessionID, newSessionID()
	p.OriginalID, p.SessionID = old, nw
	p.TargetFile = filepath.Join(p.TargetDir, nw+".jsonl")
	p.Duplicates = nil
	p.StopPID = 0
	kept := p.Blockers[:0]
	for _, b := range p.Blockers {
		if !strings.Contains(b, "running on this machine") {
			kept = append(kept, b)
		}
	}
	p.Blockers = kept
	for i := range p.Files {
		r := p.Files[i].Rel
		switch {
		case r == old+".jsonl":
			r = nw + ".jsonl"
		case strings.HasPrefix(r, old+"/"):
			r = nw + "/" + strings.TrimPrefix(r, old+"/")
		case strings.HasPrefix(r, "@file-history/"+old+"/"):
			r = "@file-history/" + nw + "/" + strings.TrimPrefix(r, "@file-history/"+old+"/")
		}
		p.Files[i].Rel = r
	}
	p.Mappings = append(p.Mappings, rewrite.Mapping{From: old, To: nw})
	p.Title = p.Title + " (from " + p.SourceHost + ")"
	p.Warnings = append(p.Warnings, "both copies stay: this one comes in as a separate session, \""+p.Title+"\"")
}

func newSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// previousCheckout returns the main checkout of the repository a copy of this session
// already here was using, when it is a checkout of the same repository.
func previousCheckout(ctx context.Context, tgt Target, sessionID, identity string) string {
	loc := sessions.Locator{FS: fsys.Local{}, ConfigDir: tgt.ConfigDir}
	dups, err := loc.Find(sessionID)
	if err != nil {
		return ""
	}
	for _, d := range dups {
		sum, err := sessions.Summarize(fsys.Local{}, d)
		if err != nil || sum.CWD == "" {
			continue
		}
		states, err := repos.ProbeLocal(ctx, []string{sum.CWD})
		if err != nil || len(states) == 0 || !states[0].IsRepo || states[0].Identity != identity {
			continue
		}
		if states[0].MainWorktree != "" {
			return states[0].MainWorktree
		}
		return states[0].Toplevel
	}
	return ""
}
