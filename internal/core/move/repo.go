package move

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Repository actions.
const (
	RepoUse        = "use"         // an existing checkout here
	RepoClone      = "clone"       // clone it
	RepoNeedsClone = "needs-clone" // not here and cloning was not asked for
	RepoDir        = "dir"         // the folder the user chose
	RepoNone       = "none"        // not a git repository: the same path
)

// RepoPlan is how the session's repository is found or made here.
type RepoPlan struct {
	Identity     string   `json:"identity,omitempty"`
	Remote       string   `json:"remote,omitempty"`
	SourceTop    string   `json:"sourceTop,omitempty"`
	SourceMain   string   `json:"sourceMain,omitempty"`
	SourceBranch string   `json:"sourceBranch,omitempty"`
	MainBranch   string   `json:"sourceMainBranch,omitempty"`
	InWorktree   bool     `json:"sourceInWorktree,omitempty"`
	AgentWT      bool     `json:"sourceAgentWorktree,omitempty"`
	Unpushed     int      `json:"unpushed,omitempty"`
	Dirty        int      `json:"dirty,omitempty"`
	Action       string   `json:"action"`
	LocalPath    string   `json:"localPath,omitempty"`
	Alternatives []string `json:"alternatives,omitempty"`
	LocalBranch  string   `json:"localBranch,omitempty"`
	Worktree     string   `json:"worktree,omitempty"` // to create
	SourceHead   string   `json:"sourceHead,omitempty"`
	HasUpstream  bool     `json:"sourceUpstream,omitempty"`
}

// planRepo decides the target folder and fills p.Repo; it returns the target cwd.
func planRepo(ctx context.Context, p *Plan, in Input, opt Options) (string, error) {
	g, s := in.Git, in.Session
	if opt.TargetDir != "" {
		abs, err := filepath.Abs(opt.TargetDir)
		if err != nil {
			return "", err
		}
		p.Repo.Action, p.Repo.LocalPath = RepoDir, abs
		if g != nil && g.IsRepo {
			fillSource(&p.Repo, g)
		}
		return abs, nil
	}
	if g == nil || !g.IsRepo || g.Identity == "" {
		p.Repo.Action = RepoNone
		if in.Source.Machine.Name != in.Target.Machine.Name {
			if _, err := os.Stat(s.CWD); err != nil {
				p.Blockers = append(p.Blockers, "the session's folder is not a git repository with a remote, so hopsesh cannot match or clone it; choose a folder here with --to")
			}
		}
		return s.CWD, nil
	}
	fillSource(&p.Repo, g)
	if in.Source.Machine.Name == in.Target.Machine.Name {
		// The same machine (another agent here): the session's own checkout, as it is.
		// Nothing travels, so nothing is left behind or brought along.
		r := &p.Repo
		r.Action, r.LocalPath, r.LocalBranch = RepoUse, g.Toplevel, g.Branch
		r.Unpushed, r.Dirty, r.SourceHead, r.InWorktree = 0, 0, "", false
		return s.CWD, nil
	}
	roots := append([]string{opt.ReposDir}, opt.ExtraRoots...)
	roots = append(roots, repos.DefaultRoots(in.Target.Machine.Facts.Home)...)
	found := repos.FindLocal(g.Identity, roots)
	// Coming home: the checkout a copy of this session here already uses comes first,
	// even outside the usual folders.
	if prev := previousCheckout(ctx, in, g.Identity); prev != "" {
		kept := []repos.Checkout{{Path: prev}}
		for _, f := range found {
			if filepath.Clean(f.Path) != filepath.Clean(prev) {
				kept = append(kept, f)
			}
		}
		found = kept
	}
	var top string
	if len(found) > 0 {
		top = found[0].Path
		p.Repo.Action, p.Repo.LocalPath = RepoUse, top
		for _, f := range found[1:] {
			p.Repo.Alternatives = append(p.Repo.Alternatives, f.Path)
		}
		p.Repo.LocalBranch = repos.CurrentBranch(ctx, top)
	} else {
		top = repos.CloneDest(opt.ReposDir, g.Identity, opt.GHQLayout)
		p.Repo.LocalPath = top
		if opt.Clone {
			p.Repo.Action = RepoClone
		} else {
			p.Repo.Action = RepoNeedsClone
			p.Blockers = append(p.Blockers, fmt.Sprintf("%s is not cloned on this machine; clone it into %s (pass --clone, or clone it yourself and pass --to)", g.Identity, top))
		}
	}
	wantWT := false
	switch opt.Worktree {
	case WorktreeCreate:
		wantWT = g.Branch != ""
	case WorktreeAuto:
		wantWT = g.LinkedWorktree && g.Branch != ""
	}
	base := top
	if wantWT {
		p.Repo.Worktree = worktreePath(top, g, in.Target.Module.Spec().Worktrees)
		base = p.Repo.Worktree
	} else if g.Branch != "" && p.Repo.LocalBranch != "" && p.Repo.LocalBranch != g.Branch {
		p.Warnings = append(p.Warnings, fmt.Sprintf("the local checkout is on %s but the session was on %s; use --worktree create to work on %s in a separate worktree", p.Repo.LocalBranch, g.Branch, g.Branch))
	}
	return joinLocal(base, relUnder(s.CWD, g.Toplevel)), nil
}

func fillSource(r *RepoPlan, g *repos.GitState) {
	r.Identity, r.Remote = g.Identity, g.Remote
	r.SourceTop, r.SourceMain = g.Toplevel, g.MainWorktree
	r.SourceBranch, r.MainBranch = g.Branch, g.MainBranch
	r.InWorktree, r.AgentWT = g.LinkedWorktree, g.AgentWorktree
	r.Unpushed, r.Dirty = g.LeftBehind()
	r.SourceHead, r.HasUpstream = g.Head, g.Upstream != ""
	if r.SourceMain == "" {
		r.SourceMain = g.Toplevel
	}
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// worktreePath mirrors the source worktree's place relative to its main checkout; a new
// one goes in the agent's own worktree folder, or beside the checkout.
func worktreePath(top string, g *repos.GitState, agentDirs []string) string {
	if g.LinkedWorktree && g.MainWorktree != "" {
		if rel := relUnder(g.Toplevel, g.MainWorktree); rel != "" {
			return joinLocal(top, rel)
		}
	}
	name := unsafeName.ReplaceAllString(g.Branch, "-")
	if len(agentDirs) > 0 {
		return filepath.Join(top, filepath.FromSlash(agentDirs[0]), name)
	}
	return filepath.Join(filepath.Dir(top), filepath.Base(top)+"-worktrees", name)
}

// previousCheckout is the main checkout a copy of this session here already uses, when it
// belongs to the same repository.
func previousCheckout(ctx context.Context, in Input, identity string) string {
	for _, c := range in.Copies {
		if c.Summary.CWD == "" {
			continue
		}
		states, err := repos.ProbeLocal(ctx, []string{c.Summary.CWD}, in.Worktrees)
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

// relUnder returns p relative to root (slash-separated), or "" when it is root or outside.
func relUnder(p, root string) string {
	norm := func(s string) string { return strings.TrimRight(strings.ReplaceAll(s, `\`, "/"), "/") }
	pp, rr := norm(p), norm(root)
	if rr != "" && strings.HasPrefix(pp, rr+"/") {
		return strings.TrimPrefix(pp, rr+"/")
	}
	return ""
}

func joinLocal(base, relSlash string) string {
	if relSlash == "" {
		return base
	}
	return filepath.Join(base, filepath.FromSlash(relSlash))
}

// realIntended resolves symbolic links of the deepest existing ancestor and appends the
// rest, so the folder is what the agent will see once it exists.
func realIntended(p string) string {
	cur := filepath.Clean(p)
	var rest []string
	for {
		if rp, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(rest) - 1; i >= 0; i-- {
				rp = filepath.Join(rp, rest[i])
			}
			return rp
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return filepath.Clean(p)
		}
		rest = append(rest, filepath.Base(cur))
		cur = parent
	}
}

// newID is a random UUID for a keep-both copy.
func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func agentCursor(head ir.NodeID) ir.Cursor { return ir.Cursor{Head: head} }
