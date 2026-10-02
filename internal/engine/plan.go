// Package engine plans and performs a session transport: it decides where a session goes
// on this machine, which files move, how paths map, what to do about the repository and
// worktrees, and then stages, rewrites, verifies and commits the copy with an undo record.
package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/fsys"
	"github.com/roeehrl/hopsesh/internal/core/link"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/internal/core/rewrite"
	"github.com/roeehrl/hopsesh/internal/core/sessions"
)

// Source is the machine a session is copied from (possibly this machine).
type Source struct {
	Host      string
	OS        string // darwin | linux | windows
	FS        fsys.FS
	ConfigDir string
	Home      string
	Auth      *link.Auth // the login there, when it could be read
	// Push pushes the current branch of a directory there to its upstream (nil when the
	// machine cannot run commands, e.g. a plain file import). It returns git's message.
	Push func(ctx context.Context, dir string) (string, error)
}

// Target is this machine.
type Target struct {
	Host      string
	OS        string
	ConfigDir string
	Home      string
	// ClaudeVersion is `claude --version` here, if known ("" = not installed or unknown).
	ClaudeVersion string
	ClaudePath    string     // the claude binary here, if found
	Auth          *link.Auth // the login here, when it could be read
}

// WorktreeMode says how to handle a session that ran in a git worktree (or on a branch the
// local checkout is not on).
type WorktreeMode string

const (
	WorktreeAuto   WorktreeMode = "auto"   // recreate the worktree if the session used one
	WorktreeCreate WorktreeMode = "create" // always use a worktree on the session's branch
	WorktreeMain   WorktreeMode = "main"   // use the main checkout as it is
)

// Options are the user's choices for one transport.
type Options struct {
	TargetDir    string // explicit local directory to resume in (skips repo matching)
	ReposDir     string
	GHQLayout    bool
	Clone        bool // clone the repository if it is not found locally
	Worktree     WorktreeMode
	Fork         bool // keep the source session running (fork) instead of handing off
	RemoteCtl    bool
	NotifyOld    bool
	Redact       bool
	DropThinking bool // the target uses a different Anthropic account
	CopyMemory   bool // merge the project's auto-memory folder
	ExtraRoots   []string
	// Round trips.
	MarkSource bool // after a handoff, title the copy left behind "↪ moved to <this host> · <title>"
	SyncCode   bool // fetch the session's commit here and fast-forward a clean checkout to it
	PushSource bool // first push the source branch's unpushed commits from the source machine
	StopLocal  bool // quit a copy of this session that is running on this machine
}

// FileItem is one file to copy.
type FileItem struct {
	Src     string `json:"src"`  // path on the source filesystem
	Rel     string `json:"rel"`  // path relative to the target project dir
	Kind    string `json:"kind"` // transcript | subagent | tool-result | sidecar | file-history | memory
	Size    int64  `json:"size"`
	Rewrite string `json:"rewrite"` // jsonl | text | none
}

// RepoPlan is what happens to the repository on this machine.
type RepoPlan struct {
	Identity     string   `json:"identity,omitempty"`
	Remote       string   `json:"remote,omitempty"`
	SourceTop    string   `json:"sourceTop,omitempty"`
	SourceMain   string   `json:"sourceMain,omitempty"`
	SourceBranch string   `json:"sourceBranch,omitempty"`
	MainBranch   string   `json:"sourceMainBranch,omitempty"` // branch checked out in the source's main folder
	InWorktree   bool     `json:"sourceInWorktree,omitempty"`
	ClaudeWT     bool     `json:"sourceClaudeWorktree,omitempty"`
	Action       string   `json:"action"` // use | clone | needs-clone | dir | none
	LocalPath    string   `json:"localPath,omitempty"`
	Alternatives []string `json:"alternatives,omitempty"`
	LocalBranch  string   `json:"localBranch,omitempty"` // branch currently checked out locally
	Worktree     string   `json:"worktree,omitempty"`    // worktree to create
	Unpushed     int      `json:"unpushed"`
	Dirty        int      `json:"dirty"`
	SourceHead   string   `json:"sourceHead,omitempty"`     // commit the session's checkout was at
	HasUpstream  bool     `json:"sourceUpstream,omitempty"` // the source branch tracks a remote branch
}

// Plan is everything a transport will do. Front-ends render it before Apply.
type Plan struct {
	SessionID     string            `json:"sessionId"`
	Title         string            `json:"title"`
	SourceHost    string            `json:"sourceHost"`
	SourceOS      string            `json:"sourceOs"`
	SourceFile    string            `json:"sourceFile"`
	SourceCWD     string            `json:"sourceCwd"`
	SourceVersion string            `json:"sourceVersion,omitempty"`
	Live          bool              `json:"live"`
	OldName       string            `json:"oldName,omitempty"`
	Repo          RepoPlan          `json:"repo"`
	TargetCWD     string            `json:"targetCwd"`
	TargetDir     string            `json:"targetProjectDir"`
	TargetFile    string            `json:"targetFile"`
	Mappings      []rewrite.Mapping `json:"mappings"`
	Files         []FileItem        `json:"files"`
	TotalBytes    int64             `json:"totalBytes"`
	Duplicates    []string          `json:"duplicates,omitempty"` // existing copies to set aside
	Warnings      []string          `json:"warnings,omitempty"`
	Blockers      []string          `json:"blockers,omitempty"` // must be resolved before Apply
	Options       Options           `json:"options"`
	NewName       string            `json:"newName"`
	Resume        link.Resume       `json:"resume"`
	StartContext  link.Context      `json:"-"`
	// Round trips.
	Mark    string `json:"mark"`              // what happens to the copy left behind: now | when-stopped | off
	StopPID int    `json:"stopPid,omitempty"` // a local process of this session to quit first
	Push    bool   `json:"push,omitempty"`    // push the source branch first
	Sync    string `json:"sync,omitempty"`    // what code sync will do, in words ("" when not applicable)
}

// Input bundles what BuildPlan needs about the session.
type Input struct {
	Summary *sessions.Summary
	Git     *repos.GitState // source git state of the session's directory (nil if unknown)
	Live    *sessions.LiveEntry
}

// BuildPlan works out a transport without changing anything.
func BuildPlan(ctx context.Context, src Source, tgt Target, in Input, opt Options) (*Plan, error) {
	s := in.Summary
	if s == nil {
		return nil, errors.New("no session")
	}
	if opt.Worktree == "" {
		opt.Worktree = WorktreeAuto
	}
	var authNotes []string
	if opt.RemoteCtl {
		if ok, why := tgt.Auth.RemoteControl(); !ok {
			opt.RemoteCtl = false
			authNotes = append(authNotes, "Remote Control stays off: "+why)
		}
	}
	if same, known := link.SameAccount(src.Auth, tgt.Auth); known && !same && !opt.DropThinking {
		// Thinking blocks carry signatures the API checks against the original account.
		opt.DropThinking = true
		authNotes = append(authNotes, fmt.Sprintf("this machine is logged in to a different Claude account than %s; thinking blocks will be left out of the copy (they are signed for the original account)", src.Host))
	}
	p := &Plan{
		SessionID: s.ID, Title: s.Title, SourceHost: src.Host, SourceOS: src.OS, SourceFile: s.File,
		SourceCWD: s.CWD, SourceVersion: s.Version, Options: opt,
	}
	if in.Live != nil {
		p.Live = true
		p.OldName = in.Live.Name
	}
	if p.OldName == "" {
		p.OldName = s.Title
	}

	targetCWD, err := planRepo(ctx, p, src, tgt, in, opt)
	if err != nil {
		return nil, err
	}
	p.TargetCWD = targetCWD
	resolved := resolveIntended(targetCWD)
	p.TargetDir = filepath.Join(tgt.ConfigDir, "projects", sessions.Slug(resolved))
	p.TargetFile = filepath.Join(p.TargetDir, s.ID+".jsonl")

	p.Mappings = buildMappings(p, src, tgt)
	if err := planFiles(p, src, s, opt); err != nil {
		return nil, err
	}

	loc := sessions.Locator{FS: fsys.Local{}, ConfigDir: tgt.ConfigDir}
	if dups, err := loc.Find(s.ID); err == nil {
		p.Duplicates = dups
	}
	if sameFile(p.SourceFile, p.TargetFile) && src.Host == tgt.Host {
		p.Blockers = append(p.Blockers, "this session is already here, in this folder; to move it to another folder on this machine use --to <dir>")
	}
	if len(p.Duplicates) > 0 {
		// Never move a transcript out from under a running Claude Code process.
		if live, err := loc.LiveRegistry(sessions.LocalAlive); err == nil {
			for _, le := range live {
				if le.SessionID == s.ID {
					if opt.StopLocal && src.Host != tgt.Host {
						p.StopPID = le.PID
					} else {
						p.Blockers = append(p.Blockers, fmt.Sprintf("this session is running on this machine (pid %d, %s); quit it first, or let hopsesh quit it (--stop-local)", le.PID, nonEmpty(le.Status, "live")))
					}
					break
				}
			}
		}
	}
	planWarnings(p, src, tgt, in, opt)
	p.Warnings = append(p.Warnings, authNotes...)
	planRoundTrip(p, src, tgt, in, opt)

	p.NewName = sessionName(s.Title, tgt.Host)
	p.StartContext = link.Context{
		SourceHost: src.Host, SourceOS: osName(src.OS), SourceVersion: s.Version, SourceCWD: s.CWD,
		TargetHost: tgt.Host, TargetOS: osName(tgt.OS), TargetCWD: p.TargetCWD,
		Branch: p.Repo.SourceBranch, WorktreeNote: worktreeNote(p), Unpushed: p.Repo.Unpushed, Dirty: p.Repo.Dirty,
		Redacted: opt.Redact, ThinkingDrop: opt.DropThinking, Live: p.Live, Fork: opt.Fork && p.Live,
		NotifyOld: opt.NotifyOld, OldName: p.OldName, NewName: p.NewName,
	}
	p.Resume = link.Resume{
		Dir: p.TargetCWD, SessionID: s.ID, Fork: opt.Fork && p.Live, RemoteCtl: opt.RemoteCtl, Name: p.NewName,
		StartPrompt: link.StartPrompt(p.StartContext),
	}
	return p, nil
}

// planRepo decides the local directory and fills p.Repo. It returns the target cwd.
func planRepo(ctx context.Context, p *Plan, src Source, tgt Target, in Input, opt Options) (string, error) {
	g := in.Git
	s := in.Summary
	if opt.TargetDir != "" {
		abs, err := filepath.Abs(opt.TargetDir)
		if err != nil {
			return "", err
		}
		p.Repo.Action = "dir"
		p.Repo.LocalPath = abs
		if g != nil && g.IsRepo {
			fillSourceRepo(&p.Repo, g)
		}
		return abs, nil
	}
	if g == nil || !g.IsRepo || g.Identity == "" {
		// Not a git repository (or no remote): same path if it exists here, else ask.
		if src.Host == tgt.Host {
			p.Repo.Action = "none"
			return s.CWD, nil
		}
		if _, err := os.Stat(s.CWD); err == nil {
			p.Repo.Action = "none"
			return s.CWD, nil
		}
		p.Repo.Action = "none"
		p.Blockers = append(p.Blockers, "the session's directory is not a git repository with a remote, so hopsesh cannot match or clone it; choose a local directory with --to")
		return s.CWD, nil
	}
	fillSourceRepo(&p.Repo, g)
	roots := append([]string{opt.ReposDir}, opt.ExtraRoots...)
	roots = append(roots, repos.DefaultRoots(tgt.Home)...)
	found := repos.FindLocal(g.Identity, roots)
	var top string
	switch {
	case len(found) > 0:
		top = found[0].Path
		p.Repo.Action = "use"
		p.Repo.LocalPath = top
		for _, f := range found[1:] {
			p.Repo.Alternatives = append(p.Repo.Alternatives, f.Path)
		}
		p.Repo.LocalBranch = repos.CurrentBranch(ctx, top)
	default:
		top = repos.CloneDest(opt.ReposDir, g.Identity, opt.GHQLayout)
		p.Repo.LocalPath = top
		if opt.Clone {
			p.Repo.Action = "clone"
		} else {
			p.Repo.Action = "needs-clone"
			p.Blockers = append(p.Blockers, fmt.Sprintf("%s is not cloned on this machine; clone it into %s (pass --clone, or clone it yourself and pass --to)", g.Identity, top))
		}
	}

	// Worktree decision.
	wantWT := false
	switch opt.Worktree {
	case WorktreeCreate:
		wantWT = g.Branch != ""
	case WorktreeAuto:
		wantWT = g.LinkedWorktree && g.Branch != ""
	}
	base := top
	if wantWT {
		p.Repo.Worktree = worktreePath(top, g)
		base = p.Repo.Worktree
	} else if g.Branch != "" && p.Repo.LocalBranch != "" && p.Repo.LocalBranch != g.Branch {
		p.Warnings = append(p.Warnings, fmt.Sprintf("the local checkout is on %s but the session was on %s; use --worktree create to work on %s in a separate worktree", p.Repo.LocalBranch, g.Branch, g.Branch))
	}
	return joinLocal(base, relSub(s.CWD, g.Toplevel, src.OS)), nil
}

func fillSourceRepo(r *RepoPlan, g *repos.GitState) {
	r.Identity, r.Remote = g.Identity, g.Remote
	r.SourceTop, r.SourceMain = g.Toplevel, g.MainWorktree
	r.SourceBranch, r.MainBranch = g.Branch, g.MainBranch
	r.InWorktree, r.ClaudeWT = g.LinkedWorktree, g.ClaudeWorktree
	r.Unpushed, r.Dirty = g.LeftBehind()
	r.SourceHead, r.HasUpstream = g.Head, g.Upstream != ""
	if r.SourceMain == "" {
		r.SourceMain = g.Toplevel
	}
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// worktreePath mirrors the source worktree's location relative to its main checkout
// (e.g. .claude/worktrees/<name>); otherwise it uses .claude/worktrees/<branch>.
func worktreePath(top string, g *repos.GitState) string {
	if g.LinkedWorktree && g.MainWorktree != "" {
		if rel, ok := relUnder(g.Toplevel, g.MainWorktree); ok && rel != "" {
			return joinLocal(top, rel)
		}
	}
	name := unsafeName.ReplaceAllString(g.Branch, "-")
	return filepath.Join(top, ".claude", "worktrees", name)
}

// relSub returns cwd relative to top on the source (slash-separated), or "".
func relSub(cwd, top, srcOS string) string {
	if rel, ok := relUnder(cwd, top); ok {
		return rel
	}
	return ""
}

func relUnder(p, root string) (string, bool) {
	norm := func(s string) string { return strings.TrimRight(strings.ReplaceAll(s, `\`, "/"), "/") }
	pp, rr := norm(p), norm(root)
	if pp == rr {
		return "", true
	}
	if strings.HasPrefix(pp, rr+"/") {
		return strings.TrimPrefix(pp, rr+"/"), true
	}
	return "", false
}

func joinLocal(base, relSlash string) string {
	if relSlash == "" {
		return base
	}
	return filepath.Join(base, filepath.FromSlash(relSlash))
}

// resolveIntended resolves symlinks of the deepest existing ancestor and appends the rest,
// so the project folder name matches what Claude Code will compute once the path exists.
func resolveIntended(p string) string {
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

func buildMappings(p *Plan, src Source, tgt Target) []rewrite.Mapping {
	var ms []rewrite.Mapping
	add := func(from, to string) {
		if from == "" || to == "" || from == to {
			return
		}
		for _, m := range ms {
			if m.From == from {
				return
			}
		}
		ms = append(ms, rewrite.Mapping{From: from, To: to})
	}
	r := p.Repo
	targetTop := r.LocalPath
	if r.Worktree != "" && r.InWorktree {
		add(r.SourceTop, r.Worktree) // the source worktree → the recreated worktree
	} else if r.Worktree != "" {
		add(r.SourceTop, r.Worktree)
	}
	if r.Action == "dir" {
		if r.SourceTop != "" {
			add(r.SourceTop, targetTop)
		} else {
			add(p.SourceCWD, targetTop)
		}
	}
	if r.SourceMain != "" {
		add(r.SourceMain, targetTop)
	}
	if r.SourceTop != "" && r.Worktree == "" {
		add(r.SourceTop, targetTop)
	}
	if r.Action == "none" {
		add(p.SourceCWD, p.TargetCWD)
	}
	add(src.ConfigDir, tgt.ConfigDir)
	add(src.Home, tgt.Home)
	if (src.OS == "windows") != (tgt.OS == "windows") {
		sep := "/"
		if tgt.OS == "windows" {
			sep = `\`
		}
		for i := range ms {
			ms[i].ToSep = sep
		}
	}
	return ms
}

func planFiles(p *Plan, src Source, s *sessions.Summary, opt Options) error {
	fi, err := src.FS.Stat(s.File)
	if err != nil {
		return err
	}
	p.Files = append(p.Files, FileItem{Src: s.File, Rel: s.ID + ".jsonl", Kind: "transcript", Size: fi.Size(), Rewrite: "jsonl"})
	projectDir := dirOf(s.File)
	sidecar := src.FS.Join(projectDir, s.ID)
	walk(src.FS, sidecar, s.ID, func(f FileItem) {
		switch {
		case strings.Contains(f.Rel, "/subagents/") && strings.HasSuffix(f.Rel, ".jsonl"):
			f.Kind, f.Rewrite = "subagent", "jsonl"
		case strings.Contains(f.Rel, "/tool-results/"):
			f.Kind, f.Rewrite = "tool-result", "text"
		case strings.HasSuffix(f.Rel, ".json"):
			f.Kind, f.Rewrite = "sidecar", "jsonl"
		default:
			f.Kind, f.Rewrite = "sidecar", "none"
		}
		p.Files = append(p.Files, f)
	})
	fh := src.FS.Join(src.ConfigDir, "file-history", s.ID)
	walk(src.FS, fh, "@file-history/"+s.ID, func(f FileItem) {
		f.Kind, f.Rewrite = "file-history", "none" // backups of file contents: never altered
		p.Files = append(p.Files, f)
	})
	if opt.CopyMemory {
		walk(src.FS, src.FS.Join(projectDir, "memory"), "memory", func(f FileItem) {
			f.Kind, f.Rewrite = "memory", "text"
			p.Files = append(p.Files, f)
		})
	}
	for _, f := range p.Files {
		p.TotalBytes += f.Size
	}
	return nil
}

// walk lists regular files under dir (recursively), giving each a slash-separated Rel
// rooted at relRoot.
func walk(fs fsys.FS, dir, relRoot string, emit func(FileItem)) {
	entries, err := fs.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		full := fs.Join(dir, e.Name())
		rel := path.Join(relRoot, e.Name())
		if e.IsDir() {
			walk(fs, full, rel, emit)
			continue
		}
		if e.Mode().IsRegular() {
			emit(FileItem{Src: full, Rel: rel, Size: e.Size()})
		}
	}
}

func planWarnings(p *Plan, src Source, tgt Target, in Input, opt Options) {
	if p.Live {
		if opt.Fork {
			p.Warnings = append(p.Warnings, fmt.Sprintf("the session is still running on %s; you chose fork, so both copies continue independently", src.Host))
		} else {
			p.Warnings = append(p.Warnings, fmt.Sprintf("the session is still running on %s (%s); hopsesh copies it as it is now and asks the old session to stop (handoff)", src.Host, nonEmpty(in.Live.Status, "live")))
		}
	}
	if p.Repo.Unpushed > 0 || p.Repo.Dirty > 0 {
		p.Warnings = append(p.Warnings, fmt.Sprintf("on %s the repository has %d unpushed commit(s) and %d uncommitted file(s) that a checkout here will not contain", src.Host, p.Repo.Unpushed, p.Repo.Dirty))
	}
	if p.Repo.InWorktree {
		wt := "a git worktree"
		if p.Repo.ClaudeWT {
			wt = "a Claude-managed worktree"
		}
		if p.Repo.Worktree != "" {
			p.Warnings = append(p.Warnings, fmt.Sprintf("the session ran in %s on branch %s; a matching worktree will be created at %s", wt, p.Repo.SourceBranch, p.Repo.Worktree))
		} else {
			p.Warnings = append(p.Warnings, fmt.Sprintf("the session ran in %s on branch %s; it will resume in the main checkout instead", wt, p.Repo.SourceBranch))
		}
	}
	if tgt.ClaudeVersion == "" {
		p.Warnings = append(p.Warnings, "Claude Code was not found on this machine; install it before resuming")
	} else if p.SourceVersion != "" && versionLess(tgt.ClaudeVersion, p.SourceVersion) {
		p.Warnings = append(p.Warnings, fmt.Sprintf("Claude Code here (%s) is older than the version that wrote the session (%s); update it before resuming", tgt.ClaudeVersion, p.SourceVersion))
	}
	for _, d := range p.Duplicates {
		if d != p.TargetFile {
			p.Warnings = append(p.Warnings, "another copy of this session on this machine will be set aside (two copies make claude --resume fail): "+d)
		}
	}
	if contains(p.Duplicates, p.TargetFile) {
		p.Warnings = append(p.Warnings, "this session already exists here; the existing copy will be set aside and can be restored with hopsesh undo")
	}
	if src.OS != "" && tgt.OS != "" && (src.OS == "windows") != (tgt.OS == "windows") {
		p.Warnings = append(p.Warnings, "moving between Windows and macOS/Linux: mapped paths get the new separators; paths outside the mapped folders (and names with spaces after the space) are left as they were")
	}
	if p.TotalBytes > 200<<20 {
		p.Warnings = append(p.Warnings, fmt.Sprintf("this session is large (%d MB)", p.TotalBytes>>20))
	}
}

func worktreeNote(p *Plan) string {
	r := p.Repo
	switch {
	case r.Worktree != "" && r.InWorktree:
		return "It ran in a worktree; a matching worktree was created here at " + r.Worktree + "."
	case r.Worktree != "":
		return "A worktree for this branch was created here at " + r.Worktree + "."
	case r.InWorktree:
		return "It ran in a worktree on the old machine; here it runs in the main checkout, which may be on a different branch."
	}
	return ""
}

var nameUnsafe = regexp.MustCompile(`[^A-Za-z0-9]+`)

func sessionName(title, host string) string {
	n := strings.Trim(nameUnsafe.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if len(n) > 40 {
		n = strings.Trim(n[:40], "-")
	}
	if n == "" {
		n = "session"
	}
	h := strings.Split(host, ".")[0]
	return n + "@" + strings.ToLower(h)
}

func osName(goos string) string {
	switch goos {
	case "darwin":
		return "macOS"
	case "linux":
		return "Linux"
	case "windows":
		return "Windows"
	case "":
		return ""
	}
	return goos
}

// numPrefix returns the leading digits of s ("284-beta" → "284").
func numPrefix(s string) string {
	for i, r := range s {
		if r < '0' || r > '9' {
			return s[:i]
		}
	}
	return s
}

// versionLess compares dotted numeric versions ("2.1.284").
func versionLess(a, b string) bool {
	pa, pb := strings.Split(clean(a), "."), strings.Split(clean(b), ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(numPrefix(pa[i]))
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(numPrefix(pb[i]))
		}
		if x != y {
			return x < y
		}
	}
	return false
}

func clean(v string) string {
	v = strings.TrimSpace(v)
	if i := strings.IndexByte(v, ' '); i > 0 {
		v = v[:i]
	}
	return strings.TrimPrefix(v, "v")
}

func dirOf(p string) string {
	i := strings.LastIndexAny(p, `/\`)
	if i < 0 {
		return ""
	}
	return p[:i]
}

func sameFile(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 == nil && err2 == nil {
		return ra == rb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func nonEmpty(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// LocalOS is this machine's GOOS.
func LocalOS() string { return runtime.GOOS }
