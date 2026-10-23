package move

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/repos"
)

// Repository actions.
const (
	RepoUse        = "use"         // an existing checkout here
	RepoClone      = "clone"       // clone it
	RepoNeedsClone = "needs-clone" // not here and cloning was not asked for
	RepoDir        = "dir"         // the folder the user chose
	RepoNone       = "none"        // not a git repository: the same folder, under this home
	RepoWorkspace  = "workspace"   // a folder of repositories: Members say each one's action
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
	// Members are the repositories of a workspace folder (Action RepoWorkspace), each found
	// here or cloned into the folder.
	Members []RepoPlan `json:"members,omitempty"`
}

// Name is the repository's folder name on the source (course-core).
func (r RepoPlan) Name() string {
	return path.Base(strings.ReplaceAll(nonEmpty(r.SourceMain, r.SourceTop), `\`, "/"))
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
	if g == nil && in.GitErr != "" {
		// Unknown is not "no repository": guessing would place the session without its code.
		p.Repo.Action = RepoNone
		p.Blockers = append(p.Blockers, fmt.Sprintf("hopsesh could not read the session's git checkout on %s (%s); try again, or choose a folder here with --to", in.Source.Machine.Name, in.GitErr))
		return s.CWD, nil
	}
	if g == nil || !g.IsRepo || g.Identity == "" {
		p.Repo.Action = RepoNone
		if in.Source.Machine.Name == in.Target.Machine.Name {
			return s.CWD, nil
		}
		// The same folder under this machine's home: /Users/sam/notes on the laptop is
		// /Users/kim/notes here, never a path in another user's home.
		cwd := underHome(s.CWD, in.Source.Machine.Facts.Home, in.Target.Machine.Facts.Home)
		if planWorkspace(ctx, p, in, opt, cwd) {
			return cwd, nil
		}
		if _, err := os.Stat(cwd); err != nil {
			p.Blockers = append(p.Blockers, fmt.Sprintf("the session's folder is not a git repository with a remote, so hopsesh cannot match or clone it, and %s is not on this machine; choose a folder here with --to", cwd))
		}
		return cwd, nil
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

// planWorkspace plans a folder that holds repositories rather than being one (~/git/course
// with course-core and course-sdk in it): it continues in the same folder here, and each
// repository is the checkout already here (in that folder first, then the usual places)
// or a clone into the folder. It reports whether the folder holds any repository with a
// remote.
func planWorkspace(ctx context.Context, p *Plan, in Input, opt Options, dir string) bool {
	var members []RepoPlan
	var missing []string
	roots := append([]string{dir, opt.ReposDir}, opt.ExtraRoots...)
	roots = append(roots, repos.DefaultRoots(in.Target.Machine.Facts.Home)...)
	for i := range in.Workspace {
		g := &in.Workspace[i]
		if !g.IsRepo || g.Identity == "" {
			continue
		}
		var m RepoPlan
		fillSource(&m, g)
		want := filepath.Join(dir, m.Name())
		found := repos.FindLocal(g.Identity, roots)
		for i, f := range found {
			if filepath.Clean(f.Path) == want {
				found[0], found[i] = found[i], found[0]
			}
		}
		switch {
		case len(found) > 0:
			m.Action, m.LocalPath = RepoUse, found[0].Path
			for _, f := range found[1:] {
				m.Alternatives = append(m.Alternatives, f.Path)
			}
			m.LocalBranch = repos.CurrentBranch(ctx, m.LocalPath)
		case opt.Clone:
			m.Action, m.LocalPath = RepoClone, want
		default:
			m.Action, m.LocalPath = RepoNeedsClone, want
			missing = append(missing, m.Name())
		}
		members = append(members, m)
	}
	if len(members) == 0 {
		return false
	}
	p.Repo.Action, p.Repo.LocalPath, p.Repo.Members = RepoWorkspace, dir, members
	if len(missing) > 0 {
		p.Blockers = append(p.Blockers, fmt.Sprintf("%s %s not cloned on this machine; clone %s into %s (pass --clone, or clone %s yourself)",
			strings.Join(missing, ", "), pick(len(missing), "is", "are"), pick(len(missing), "it", "them"), dir, pick(len(missing), "it", "them")))
	}
	src := in.Source.Machine.Name
	for _, m := range members {
		if m.Action == RepoUse && filepath.Dir(m.LocalPath) != dir {
			p.Warnings = append(p.Warnings, fmt.Sprintf("%s is at %s here, outside %s; paths in the conversation point there", m.Name(), m.LocalPath, dir))
		}
		if m.Unpushed > 0 || m.Dirty > 0 {
			p.Warnings = append(p.Warnings, fmt.Sprintf("on %s %s has %d unpushed commit(s) and %d uncommitted file(s) that a checkout here will not contain", src, m.Name(), m.Unpushed, m.Dirty))
		}
	}
	p.Warnings = append(p.Warnings, fmt.Sprintf("%s holds %d repositories on %s; hopsesh brings each one, not other folders in it (such as worktrees)", in.Session.CWD, len(members), src))
	return true
}

func pick(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// underHome is p moved from one home folder to another (both given in their machine's
// form); p itself when it is not under from.
func underHome(p, from, to string) string {
	if from == "" || to == "" {
		return p
	}
	norm := func(s string) string { return strings.TrimRight(strings.ReplaceAll(s, `\`, "/"), "/") }
	if norm(p) == norm(from) {
		return to
	}
	if rel := relUnder(p, from); rel != "" {
		return joinLocal(to, rel)
	}
	return p
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
