package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/proc"
	"github.com/roeehrl/hopsesh/internal/core/repos"
)

// Cleaning up the branches cloud hand-offs leave on the remote: the handoff branch hopsesh
// pushed (hopsesh/handoff/…) and the cloud's own branch it brought home (claude/…,
// copilot/…). Once their work is merged into the remote's default branch, hopsesh offers
// to delete them, as the cloud's delete_branch setting allows (after-merge; on-undo and
// never keep them). It never deletes one by itself: the user picks, and each deletion is a
// lease (only while the branch is where hopsesh saw it) and journaled, so undo pushes it
// back.

// Branch kinds.
const (
	BranchHandoff = "handoff" // the branch hopsesh pushed for a hand-off
	BranchVendor  = "vendor"  // the cloud's own branch, brought home
)

// BranchCandidate is a branch a hand-off or a bring-back left on a remote, and whether
// hopsesh offers to delete it now.
type BranchCandidate struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"` // BranchHandoff | BranchVendor
	Branch     string `json:"branch"`
	Repo       string `json:"repo,omitempty"`
	Checkout   string `json:"checkout"`
	Remote     string `json:"remote"`
	Cloud      string `json:"cloud"`
	CloudTitle string `json:"cloudTitle"`
	Session    string `json:"session,omitempty"`
	Title      string `json:"title,omitempty"`
	Source     string `json:"source"` // the journal of the hand-off or bring-back
	// Policy is the cloud's delete_branch for it (after-merge | on-undo | never).
	Policy string `json:"policy"`
	// Sha is where the branch is on the remote ("" : gone); Into, the default branch it was
	// checked against; Merged and By say whether and how its work is in it ("history": its
	// commit is in the default branch's history; "pull request #12": GitHub says so).
	Sha    string `json:"sha,omitempty"`
	Into   string `json:"into,omitempty"`
	Merged bool   `json:"merged"`
	By     string `json:"by,omitempty"`
	// Brought is when the cloud's work came here, when it did.
	Brought time.Time `json:"brought,omitzero"`
	// Offer: hopsesh offers to delete it now; Why says why (not), for people.
	Offer bool   `json:"offer"`
	Why   string `json:"why"`
}

// BranchFacts are what git and the remote say about a candidate.
type BranchFacts struct {
	Exists bool   // on the remote
	Merged bool   // its work is in the default branch
	By     string // how that is known
	Into   string // the default branch
	Error  string // the remote could not be asked
}

// CleanupDecision decides whether hopsesh offers to delete a branch, and says why (not).
func CleanupDecision(c BranchCandidate, f BranchFacts) (offer bool, why string) {
	switch {
	case f.Error != "":
		return false, "hopsesh could not ask the remote: " + f.Error
	case !f.Exists:
		return false, "gone from the remote already"
	case c.Policy == move.CleanupNever:
		return false, "kept: delete_branch is never for " + c.CloudTitle
	case c.Policy == move.CleanupOnUndo:
		return false, "kept until you undo it: delete_branch is on-undo for " + c.CloudTitle
	case !f.Merged:
		return false, "not merged into " + nonEmpty(f.Into, "the default branch") + " yet"
	}
	why = "merged into " + nonEmpty(f.Into, "the default branch")
	if f.By != "" {
		why += " (" + f.By + ")"
	}
	if !c.Brought.IsZero() {
		why += "; its work came here " + c.Brought.Local().Format("2 Jan")
	}
	return true, why
}

func candidateID(checkout, remote, branch string) string {
	h := sha256.Sum256([]byte(checkout + "\x00" + remote + "\x00" + branch))
	return hex.EncodeToString(h[:5])
}

// cleanupSources are the branches hand-offs and bring-backs on this machine left on their
// remotes (not undone), one candidate per branch, with no facts yet.
func (a *App) cleanupSources() []BranchCandidate {
	js, _ := journal.List(a.StateDir)
	undone := map[string]bool{}
	for _, j := range js {
		undone[j.ID] = j.Undone
	}
	byID := map[string]*BranchCandidate{}
	var out []*BranchCandidate
	add := func(c BranchCandidate) *BranchCandidate {
		c.ID = candidateID(c.Checkout, c.Remote, c.Branch)
		if x := byID[c.ID]; x != nil {
			return x
		}
		byID[c.ID] = &c
		out = append(out, &c)
		return &c
	}
	title := func(cloud string) string {
		if _, cl, ok := a.cloudModule(cloud); ok {
			return cl.Title
		}
		return cloud
	}
	files, _ := filepath.Glob(filepath.Join(a.StateDir, "handoffs", "*.json"))
	for _, f := range files {
		h, err := move.LoadHandoff(a.StateDir, strings.TrimSuffix(filepath.Base(f), ".json"))
		if err != nil || !h.Pushed || h.Branch == "" || undone[h.Journal] || h.Machine != LocalName() || h.Checkout == "" {
			continue
		}
		add(BranchCandidate{Kind: BranchHandoff, Branch: h.Branch, Repo: h.Repo, Checkout: h.Checkout, Remote: nonEmpty(h.Remote, "origin"), Cloud: h.Cloud,
			CloudTitle: title(h.Cloud), Session: string(h.Session.Session), Title: h.Title, Source: h.Journal, Policy: nonEmpty(h.Cleanup, move.CleanupAfterMerge)})
	}
	fetches, _ := move.LoadFetches(a.StateDir)
	for _, f := range fetches {
		if undone[f.Journal] || f.Adopted == nil || f.Machine != LocalName() || f.Checkout == "" {
			continue
		}
		b := ""
		switch {
		case f.Adopted.Renamed != "":
			b = f.Adopted.Renamed
		case f.VendorPrefix != "" && strings.HasPrefix(f.CloudBranch, f.VendorPrefix):
			b = f.CloudBranch
		default:
			if _, cl, ok := a.cloudModule(f.Cloud); ok && cl.VendorPrefix != "" && strings.HasPrefix(f.CloudBranch, cl.VendorPrefix) {
				b = f.CloudBranch
			}
		}
		if b != "" {
			c := add(BranchCandidate{Kind: BranchVendor, Branch: b, Repo: f.Repo, Checkout: f.Checkout, Remote: "origin", Cloud: f.Cloud, CloudTitle: f.CloudTitle,
				Session: string(f.Session), Title: f.Title, Source: f.Journal, Policy: a.Cfg.CloudSettings(f.Cloud).DeleteBranch})
			c.Brought = f.Adopted.Time
		}
		// The hand-off this session came from: its work is home too.
		for _, c := range out {
			if c.Kind == BranchHandoff && c.Cloud == f.Cloud && c.Session == string(f.Session) && (c.Brought.IsZero() || f.Adopted.Time.Before(c.Brought)) {
				c.Brought = f.Adopted.Time
			}
		}
	}
	res := make([]BranchCandidate, len(out))
	for i, c := range out {
		res[i] = *c
	}
	return res
}

// CleanupCandidates lists the branches hand-offs and bring-backs left on their remotes,
// asks each remote (read-only: ls-remote, and a fetch of the commits it needs into no ref)
// whether their work is merged into its default branch, and says which hopsesh offers to
// delete. Where gh is installed, a pull request GitHub merged (a squash or a rebase) counts
// too. It changes nothing.
func (a *App) CleanupCandidates(ctx context.Context) []BranchCandidate {
	cands := a.cleanupSources()
	type head struct {
		branch, sha string
		err         error
	}
	heads := map[string]head{}
	g := repos.Here{}
	for i := range cands {
		c := &cands[i]
		var f BranchFacts
		key := c.Checkout + "\x00" + c.Remote
		hd, ok := heads[key]
		if !ok {
			hd.branch, hd.sha, hd.err = repos.DefaultBranch(ctx, g, c.Checkout, c.Remote)
			heads[key] = hd
		}
		sha, err := repos.RemoteRef(ctx, g, c.Checkout, c.Remote, "refs/heads/"+c.Branch)
		switch {
		case err != nil:
			f.Error = firstLineOf(err.Error())
		case hd.err != nil:
			f.Error = firstLineOf(hd.err.Error())
		default:
			f.Exists, f.Into, c.Sha, c.Into = sha != "", hd.branch, sha, hd.branch
			if f.Exists && hd.sha != "" {
				f.Merged, f.By = a.merged(ctx, *c, sha, hd.branch, hd.sha)
			}
		}
		c.Merged, c.By = f.Merged, f.By
		c.Offer, c.Why = CleanupDecision(*c, f)
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].Offer && !cands[j].Offer })
	return cands
}

// merged says whether a branch's work is in the default branch: its commit is in the
// default branch's history; or the bring-back's branch here is (the work was merged from
// the copy hopsesh brought); or GitHub says a pull request from it was merged.
func (a *App) merged(ctx context.Context, c BranchCandidate, sha, into, intoSha string) (bool, string) {
	g := repos.Here{}
	if repos.HaveCommit(ctx, g, c.Checkout, c.Remote, "refs/heads/"+into, intoSha) == nil {
		if repos.HaveCommit(ctx, g, c.Checkout, c.Remote, "refs/heads/"+c.Branch, sha) == nil && repos.InHistory(ctx, g, c.Checkout, sha, intoSha) {
			return true, "in its history"
		}
		for _, f := range a.fetchesOf(c) {
			if b := f.Adopted.Branch; b != "" {
				if local, err := (repos.LocalGit{}).Ref(ctx, c.Checkout, "refs/heads/"+b); err == nil && local != "" && repos.InHistory(ctx, g, c.Checkout, local, intoSha) {
					return true, "the work brought here on " + b + " is in its history"
				}
			}
		}
	}
	if n := mergedPR(ctx, c.Repo, c.Branch); n != "" {
		return true, "pull request " + n + " was merged"
	}
	return false, ""
}

// fetchesOf are the bring-backs of the candidate's cloud session.
func (a *App) fetchesOf(c BranchCandidate) []*move.Fetch {
	fs, _ := move.LoadFetches(a.StateDir)
	var out []*move.Fetch
	for _, f := range fs {
		if f.Cloud == c.Cloud && string(f.Session) == c.Session && f.Adopted != nil {
			out = append(out, f)
		}
	}
	return out
}

// mergedPR asks gh, read-only and only where gh is installed, for a merged pull request
// from branch in a GitHub repository ("#12"; "" when there is none or gh cannot say).
var mergedPR = func(ctx context.Context, repo, branch string) string {
	owner, ok := strings.CutPrefix(repo, "github.com/")
	if !ok || strings.Count(owner, "/") != 1 {
		return ""
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := proc.CommandContext(ctx, "gh", "pr", "list", "--repo", owner, "--head", branch, "--state", "merged", "--json", "number", "--limit", "1")
	cmd.Env = append(cmd.Environ(), "GH_PROMPT_DISABLED=1", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	var prs []struct {
		Number int `json:"number"`
	}
	if json.Unmarshal(out, &prs) != nil || len(prs) == 0 || prs[0].Number <= 0 {
		return ""
	}
	return fmt.Sprintf("#%d", prs[0].Number)
}

// CleanupResult is what deleting branches did.
type CleanupResult struct {
	Journal string            `json:"journal"`
	Deleted []BranchCandidate `json:"deleted"`
	// Failed are the branches hopsesh did not delete, with why.
	Failed map[string]string `json:"failed,omitempty"`
}

// DeleteBranches deletes the branches the user picked (by id) among the ones hopsesh
// offers, each with a lease at the commit it was checked at, under one journal: undo pushes
// them back. A branch that moved, or is no longer offered, is left alone and reported.
func (a *App) DeleteBranches(ctx context.Context, ids []string) (*CleanupResult, error) {
	if len(ids) == 0 {
		return nil, errors.New("no branch chosen")
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var picked []BranchCandidate
	res := &CleanupResult{Failed: map[string]string{}}
	for _, c := range a.CleanupCandidates(ctx) {
		if !want[c.ID] {
			continue
		}
		delete(want, c.ID)
		if !c.Offer {
			res.Failed[c.Branch] = c.Why
			continue
		}
		picked = append(picked, c)
	}
	for id := range want {
		res.Failed[id] = "no such branch to clean up"
	}
	if len(picked) == 0 {
		return res, errors.New("nothing to delete")
	}
	j, err := journal.New(a.StateDir, journal.KindCleanup, fmt.Sprintf("deleted %d merged branch(es)", len(picked)))
	if err != nil {
		return nil, err
	}
	res.Journal = j.ID
	g := repos.Here{}
	for _, c := range picked {
		ref := "refs/heads/" + c.Branch
		if err := repos.DeleteRef(ctx, g, c.Checkout, c.Remote, ref, c.Sha); err != nil {
			res.Failed[c.Branch] = firstLineOf(err.Error())
			continue
		}
		// Recorded once deleted: a branch that stayed is not one undo should push back.
		if err := j.DeletedRef(LocalName(), c.Checkout, c.Remote, ref, c.Sha); err != nil {
			res.Failed[c.Branch] = "deleted, but not recorded for undo: " + err.Error()
		}
		res.Deleted = append(res.Deleted, c)
		a.Audit.Write(audit.Entry{Action: "git.cleanup", Detail: map[string]any{"branch": c.Branch, "repo": c.Repo, "commit": c.Sha, "kind": c.Kind,
			"cloud": c.Cloud, "journal": j.ID}})
	}
	if len(res.Failed) == 0 {
		res.Failed = nil
	}
	if len(res.Deleted) == 0 {
		return res, errors.New("no branch was deleted")
	}
	return res, nil
}

func firstLineOf(s string) string { return strings.SplitN(strings.TrimSpace(s), "\n", 2)[0] }
