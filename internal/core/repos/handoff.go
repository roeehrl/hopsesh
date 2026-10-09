package repos

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/proc"
)

// Handing a session's code to a cloud: the cloud clones a branch, so anything it would not
// find there (unpushed commits, changed files, untracked files the user chose) goes onto a
// handoff branch built from a snapshot commit. The snapshot is made with a temporary
// index, the way `git stash create` leaves things: the user's index, working tree and
// branch stay exactly as they were. Credential-like files never go up, whatever the user
// chose.

// DenyPatterns are the files that never go to a cloud, matched against each path's last
// element: Claude Code's own bundle exclusions (.env, *.tfvars, id_rsa, *.pem) widened to
// their families, and *.key, *.p12, .npmrc and the other private SSH key names.
var DenyPatterns = []string{".env*", "*.tfvars", "*.tfvars.json", "id_rsa*", "id_dsa*", "id_ecdsa*", "id_ed25519*", "*.pem", "*.key", "*.p12", ".npmrc"}

// MaxSnapshotFile is the largest file a snapshot carries (GitHub warns above it, and
// refuses files twice that size).
const MaxSnapshotFile = 50 << 20

// Why a file stays on the machine.
const (
	WithheldCredential = "credential" // its name looks like a credential's
	WithheldLFS        = "lfs"        // Git LFS manages it
	WithheldSize       = "size"       // larger than MaxSnapshotFile
)

// Withheld is a file a snapshot leaves out, and why.
type Withheld struct {
	Path string `json:"path"`
	Why  string `json:"why"`
}

// Candidate is an untracked file the user may choose to carry.
type Candidate struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// SnapshotPlan is what a snapshot will carry, worked out without changing anything.
type SnapshotPlan struct {
	Head   string `json:"head"`             // the commit the snapshot builds on
	Branch string `json:"branch,omitempty"` // the branch checked out ("" when detached)
	// Tracked are changed tracked files it carries (deletions too); Untracked, the
	// untracked files the user chose.
	Tracked   []string `json:"tracked,omitempty"`
	Untracked []string `json:"untracked,omitempty"`
	// Offered are untracked files it does not carry unless chosen.
	Offered  []Candidate `json:"offered,omitempty"`
	Withheld []Withheld  `json:"withheld,omitempty"`
	Bytes    int64       `json:"bytes"`
}

// Changes reports whether the snapshot carries anything beyond Head.
func (p SnapshotPlan) Changes() bool { return len(p.Tracked)+len(p.Untracked) > 0 }

// SnapshotOptions narrow a snapshot plan.
type SnapshotOptions struct {
	// Include are globs of untracked files to carry (a path or its last element matches).
	Include []string
	// Deny are globs of files never carried (DenyPatterns when nil); they win over Include.
	Deny []string
	// Exclude are agent worktree folders (".claude/worktrees"), never part of a snapshot.
	Exclude []string
	MaxFile int64 // MaxSnapshotFile when 0
}

// Git runs git in checkouts on one machine: this one (Here), or another one over SSH.
type Git interface {
	// Run runs git in dir with extra environment and returns its standard output as it is
	// (a failure is a *GitError with git's explanation).
	Run(ctx context.Context, dir string, env []string, args ...string) (string, error)
	// Sizes returns the sizes of files under dir (-1 for one that is not a regular file).
	Sizes(ctx context.Context, dir string, paths []string) ([]int64, error)
	// WriteFile writes a temporary file; Remove removes one (gone already is fine).
	WriteFile(ctx context.Context, path string, b []byte) error
	Remove(ctx context.Context, path string) error
}

// Here is git on this machine.
type Here struct{}

// Run runs git here without ever prompting.
func (Here) Run(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := proc.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = proc.PipeEnvironment(append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never"), env...))
	if os.Getenv("GIT_SSH_COMMAND") == "" && !hasEnv(env, "GIT_SSH_COMMAND") {
		cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", &GitError{Args: args, Stderr: stderr.String(), Err: err}
	}
	return string(out), nil
}

// Sizes stats files here.
func (Here) Sizes(_ context.Context, dir string, paths []string) ([]int64, error) {
	out := make([]int64, len(paths))
	for i, p := range paths {
		fi, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(p)))
		switch {
		case err != nil:
			out[i] = -1
		case fi.Mode()&os.ModeSymlink != 0:
			out[i] = 0
		case fi.Mode().IsRegular():
			out[i] = fi.Size()
		default:
			out[i] = -1
		}
	}
	return out, nil
}

// WriteFile writes a file here.
func (Here) WriteFile(_ context.Context, p string, b []byte) error { return os.WriteFile(p, b, 0o600) }

// Remove removes a file here.
func (Here) Remove(_ context.Context, p string) error {
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func trimmed(s string, err error) (string, error) { return strings.TrimSpace(s), err }

// Denied reports whether a path's name is credential-like (deny: globs; DenyPatterns when
// nil).
func Denied(p string, deny []string) bool {
	if deny == nil {
		deny = DenyPatterns
	}
	base := path.Base(strings.ReplaceAll(p, `\`, "/"))
	for _, d := range deny {
		if ok, _ := path.Match(d, base); ok {
			return true
		}
		if ok, _ := path.Match(d, p); ok {
			return true
		}
	}
	return false
}

// matches reports whether a path or its last element matches one of the globs.
func matches(p string, globs []string) bool {
	base := path.Base(p)
	for _, g := range globs {
		if g == p || strings.HasSuffix(g, "/") && strings.HasPrefix(p, g) {
			return true
		}
		if ok, _ := path.Match(g, p); ok {
			return true
		}
		if ok, _ := path.Match(g, base); ok {
			return true
		}
	}
	return false
}

// PlanSnapshot works out what a snapshot of the checkout dir would carry. It only reads:
// the changed tracked files go, untracked files go only when Include names them, and a
// credential-like name, a file Git LFS manages or one over the size limit stays, whatever
// Include says.
func PlanSnapshot(ctx context.Context, g Git, dir string, o SnapshotOptions) (SnapshotPlan, error) {
	var p SnapshotPlan
	head, err := trimmed(g.Run(ctx, dir, nil, "rev-parse", "--verify", "--quiet", "HEAD"))
	if err != nil || head == "" {
		return p, fmt.Errorf("%s has no commit to build a snapshot on", dir)
	}
	p.Head = head
	p.Branch, _ = trimmed(g.Run(ctx, dir, nil, "branch", "--show-current"))
	args := append([]string{"--no-optional-locks", "-c", "core.quotePath=false", "status", "--porcelain=v1", "-z", "--untracked-files=all", "--", ":/"}, excludes(o.Exclude)...)
	out, err := g.Run(ctx, dir, nil, args...)
	if err != nil {
		return p, err
	}
	maxFile := o.MaxFile
	if maxFile == 0 {
		maxFile = MaxSnapshotFile
	}
	type entry struct {
		path      string
		untracked bool
		deleted   bool
	}
	var es []entry
	recs := strings.Split(out, "\x00")
	for i := 0; i < len(recs); i++ {
		r := recs[i]
		if len(r) < 4 {
			continue
		}
		xy, pth := r[:2], r[3:]
		if strings.HasSuffix(pth, "/") {
			continue // a nested repository: never part of a snapshot
		}
		switch {
		case xy == "??":
			es = append(es, entry{path: pth, untracked: true})
		case xy[0] == 'R' || xy[0] == 'C':
			es = append(es, entry{path: pth})
			if i+1 < len(recs) && xy[0] == 'R' {
				i++
				es = append(es, entry{path: recs[i], deleted: true}) // the old name goes
			}
		default:
			es = append(es, entry{path: pth, deleted: xy[0] == 'D' || xy[1] == 'D'})
		}
	}
	var live []string
	for _, e := range es {
		if !e.deleted {
			live = append(live, e.path)
		}
	}
	sizes := map[string]int64{}
	lfs := map[string]bool{}
	for start := 0; start < len(live); start += 200 {
		chunk := live[start:min(start+200, len(live))]
		ss, err := g.Sizes(ctx, dir, chunk)
		if err != nil {
			return p, err
		}
		for i, s := range ss {
			sizes[chunk[i]] = s
		}
		attrs, err := g.Run(ctx, dir, nil, append([]string{"check-attr", "-z", "filter", "--"}, chunk...)...)
		if err == nil {
			f := strings.Split(attrs, "\x00")
			for i := 0; i+2 < len(f); i += 3 {
				if f[i+2] == "lfs" {
					lfs[f[i]] = true
				}
			}
		}
	}
	seen := map[string]bool{}
	for _, e := range es {
		if seen[e.path] {
			continue
		}
		seen[e.path] = true
		size := sizes[e.path]
		switch {
		case Denied(e.path, o.Deny):
			p.Withheld = append(p.Withheld, Withheld{Path: e.path, Why: WithheldCredential})
		case lfs[e.path]:
			p.Withheld = append(p.Withheld, Withheld{Path: e.path, Why: WithheldLFS})
		case !e.deleted && size > maxFile:
			p.Withheld = append(p.Withheld, Withheld{Path: e.path, Why: WithheldSize})
		case e.untracked && size < 0:
			// gone since git listed it
		case e.untracked && !matches(e.path, o.Include):
			p.Offered = append(p.Offered, Candidate{Path: e.path, Size: size})
		case e.untracked:
			p.Untracked = append(p.Untracked, e.path)
			p.Bytes += max(size, 0)
		default:
			p.Tracked = append(p.Tracked, e.path)
			p.Bytes += max(size, 0)
		}
	}
	sort.Strings(p.Tracked)
	sort.Strings(p.Untracked)
	sort.Slice(p.Offered, func(i, j int) bool { return p.Offered[i].Path < p.Offered[j].Path })
	sort.Slice(p.Withheld, func(i, j int) bool { return p.Withheld[i].Path < p.Withheld[j].Path })
	return p, nil
}

// SnapshotMessage is a snapshot commit's message: a line and one trailer, an opaque id
// that lets any machine tie the cloud's work back to the session (no link, no names).
func SnapshotMessage(id string) string {
	return "hopsesh handoff snapshot\n\nHopsesh-Handoff: " + id + "\n"
}

// SnapshotTrailer is the trailer's key.
const SnapshotTrailer = "Hopsesh-Handoff"

// ExtraFile is a file a snapshot adds that is not in the working tree (the conversation
// as .hopsesh/handoff.md, when the user asks for it).
type ExtraFile struct {
	Path string // slash-separated, in the repository
	Data []byte
}

// Snapshot builds a commit on p.Head holding the working tree's version of the planned
// files (and extra), with msg, and returns it. It uses a temporary index (read-tree HEAD,
// add the planned paths, write-tree, commit-tree), so the index, the working tree and
// every branch stay as they are; the commit is reachable from no ref until it is pushed.
func Snapshot(ctx context.Context, g Git, dir string, p SnapshotPlan, msg string, extra ...ExtraFile) (string, error) {
	gitDir, err := trimmed(g.Run(ctx, dir, nil, "rev-parse", "--absolute-git-dir"))
	if err != nil {
		return "", err
	}
	var rb [6]byte
	_, _ = rand.Read(rb[:])
	tmp := gitDir + "/hopsesh-handoff-" + hex.EncodeToString(rb[:])
	index := tmp + ".index"
	defer func() { _ = g.Remove(context.WithoutCancel(ctx), index) }()
	env := []string{"GIT_INDEX_FILE=" + index}
	if _, err := g.Run(ctx, dir, env, "read-tree", p.Head); err != nil {
		return "", err
	}
	paths := append(append([]string(nil), p.Tracked...), p.Untracked...)
	for start := 0; start < len(paths); start += 200 {
		chunk := paths[start:min(start+200, len(paths))]
		args := append([]string{"add", "-A", "--"}, chunk...)
		if _, err := g.Run(ctx, dir, env, args...); err != nil {
			return "", err
		}
	}
	for i, f := range extra {
		file := fmt.Sprintf("%s-%d", tmp, i)
		if err := g.WriteFile(ctx, file, f.Data); err != nil {
			return "", err
		}
		blob, err := trimmed(g.Run(ctx, dir, nil, "hash-object", "-w", "--no-filters", file))
		_ = g.Remove(context.WithoutCancel(ctx), file)
		if err != nil {
			return "", err
		}
		if _, err := g.Run(ctx, dir, env, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+f.Path); err != nil {
			return "", err
		}
	}
	tree, err := trimmed(g.Run(ctx, dir, env, "write-tree"))
	if err != nil {
		return "", err
	}
	var id []string
	if _, err := g.Run(ctx, dir, nil, "var", "GIT_COMMITTER_IDENT"); err != nil {
		// No identity configured there: the snapshot is still the user's, under a plain name.
		id = []string{"GIT_AUTHOR_NAME=hopsesh", "GIT_AUTHOR_EMAIL=hopsesh@localhost", "GIT_COMMITTER_NAME=hopsesh", "GIT_COMMITTER_EMAIL=hopsesh@localhost"}
	}
	return trimmed(g.Run(ctx, dir, id, "commit-tree", tree, "-p", p.Head, "--no-gpg-sign", "-m", strings.TrimRight(msg, "\n")))
}

// DiffCommits is the binary-safe unified diff from one commit to another (a snapshot's
// changes, sent with a cloud task as its starting diff).
func DiffCommits(ctx context.Context, g Git, dir, from, to string) ([]byte, error) {
	out, err := g.Run(ctx, dir, nil, "diff", "--binary", "--no-color", "--no-ext-diff", from, to)
	if err != nil {
		return nil, err
	}
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return []byte(out), nil
}

// Errors of pushing and deleting a ref.
var (
	// ErrRefExists means the remote already has a ref of that name (PushRef never
	// overwrites one).
	ErrRefExists = errors.New("the remote already has a branch of that name")
	// ErrRefMoved means the ref no longer points at what hopsesh pushed (DeleteRef leaves it).
	ErrRefMoved = errors.New("the branch moved on since hopsesh pushed it")
	// ErrPushRefused means the remote refused the push (branch protection or permissions).
	ErrPushRefused = errors.New("the remote refused the push")
)

// pushEnv keeps a push from ever asking: no terminal prompts, no credential manager
// dialogs, SSH in batch mode.
var pushEnv = []string{"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never"}

// PushRef pushes sha to ref (a full ref name) on remote from the checkout dir, without
// prompting. It never overwrites: a ref of that name already there is ErrRefExists.
func PushRef(ctx context.Context, g Git, dir, remote, sha, ref string) error {
	_, err := g.Run(ctx, dir, pushEnv, "push", "--quiet", "--force-with-lease="+ref+":", remote, sha+":"+ref)
	if err == nil {
		return nil
	}
	return pushError(err, ErrRefExists)
}

// DeleteRef deletes ref on remote while it still points at expect (a lease), and returns
// ErrRefMoved when it moved on. A ref that is gone already is fine.
func DeleteRef(ctx context.Context, g Git, dir, remote, ref, expect string) error {
	now, err := RemoteRef(ctx, g, dir, remote, ref)
	if err != nil {
		return err
	}
	if now == "" {
		return nil
	}
	if now != expect {
		return fmt.Errorf("%w (%s is now %s)", ErrRefMoved, strings.TrimPrefix(ref, "refs/heads/"), shortSha(now))
	}
	_, err = g.Run(ctx, dir, pushEnv, "push", "--quiet", "--force-with-lease="+ref+":"+expect, remote, ":"+ref)
	if err == nil {
		return nil
	}
	return pushError(err, ErrRefMoved)
}

// RemoteRef returns the commit ref points at on remote ("" when it is not there).
func RemoteRef(ctx context.Context, g Git, dir, remote, ref string) (string, error) {
	out, err := g.Run(ctx, dir, pushEnv, "ls-remote", remote, ref)
	if err != nil {
		return "", err
	}
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) == 2 && f[1] == ref {
			return f[0], nil
		}
	}
	return "", nil
}

// pushError says why a push failed: stale is what a lost lease means.
func pushError(err error, stale error) error {
	var ge *GitError
	if !errors.As(err, &ge) {
		return err
	}
	msg := strings.ToLower(ge.Stderr)
	switch {
	case strings.Contains(msg, "stale info") || strings.Contains(msg, "already exists"):
		return fmt.Errorf("%w: %s", stale, firstLineOf(ge.Stderr))
	case strings.Contains(msg, "pre-receive hook declined") || strings.Contains(msg, "protected branch") || strings.Contains(msg, "permission") ||
		strings.Contains(msg, "denied") || strings.Contains(msg, " 403") || strings.Contains(msg, "refused") || strings.Contains(msg, "rejected"):
		return fmt.Errorf("%w: %s", ErrPushRefused, firstLineOf(ge.Stderr))
	}
	return err
}

func firstLineOf(s string) string {
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "To ") {
			return l
		}
	}
	return strings.TrimSpace(s)
}

func shortSha(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// HandoffBranch names a handoff branch: <prefix><yyyymmdd>-<the session id's first 8>.
func HandoffBranch(prefix, date, session string) string {
	if prefix == "" {
		prefix = "hopsesh/handoff/"
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	id := safeRefPart(session)
	if len(id) > 8 {
		id = id[:8]
	}
	return prefix + date + "-" + id
}

// FreeRemoteBranch is name, or name-2, name-3, … when the remote has a branch of that name
// (or a local one exists).
func FreeRemoteBranch(ctx context.Context, g Git, dir, remote, name string) string {
	taken := map[string]bool{}
	if out, err := g.Run(ctx, dir, pushEnv, "ls-remote", "--heads", remote, "refs/heads/"+name+"*"); err == nil {
		for _, l := range strings.Split(out, "\n") {
			if f := strings.Fields(l); len(f) == 2 {
				taken[strings.TrimPrefix(f[1], "refs/heads/")] = true
			}
		}
	}
	n := name
	for i := 2; i < 100; i++ {
		_, noLocal := g.Run(ctx, dir, nil, "rev-parse", "--verify", "--quiet", "refs/heads/"+n)
		if !taken[n] && noLocal != nil {
			return n
		}
		n = name + "-" + strconv.Itoa(i)
	}
	return n
}

// ErrNoCheckout means there is no checkout here to start a cloud's driver from.
var ErrNoCheckout = errors.New("no checkout of the repository here")

// DriverOptions say where a cloud's driver starts and on which branch.
type DriverOptions struct {
	// Folder is the repository's hand-off folder (HandoffFolder): the same folder hand-off
	// after hand-off, so a driver that asks whether the folder is trusted (Claude Code)
	// asks once per repository. "" is a new temporary folder each time.
	Folder string
	// Checkout is a checkout of the repository here ("": none, so the folder is a clone of
	// RemoteURL's branch).
	Checkout  string
	RemoteURL string
	Branch    string
	// Start is where a branch made for the driver starts ("": the fetched remote branch);
	// Keep keeps such a branch afterwards (an upload's, which undo deletes).
	Start string
	Keep  bool
	// Waiting is called once when another hand-off of the repository holds the folder,
	// before this one waits for it.
	Waiting func()
}

// DriverDir is a folder whose current branch is o.Branch, for a cloud driver that starts
// from "your current branch" (claude --cloud), and done, which the caller runs once the
// driver ended. With a checkout here it is a worktree of it: a local branch of that name
// is used as it is (even when another worktree has it checked out: nothing is committed
// in this one); otherwise one is made at Start (the fetched remote branch when it is
// there) and, unless Keep, deleted again by done. Without a checkout here, it is a shallow
// clone of RemoteURL's branch. The user's checkout, index and branch never change.
//
// A stable o.Folder is reset to the branch each time (anything the last driver left in it
// is removed), and done leaves its worktree detached, so it holds no branch. Only one
// hand-off of a repository uses its folder at a time: another waits until done.
func DriverDir(ctx context.Context, o DriverOptions) (dir string, done func(), err error) {
	if o.Folder == "" {
		return tempDriverDir(ctx, o)
	}
	if err := os.MkdirAll(filepath.Dir(o.Folder), 0o700); err != nil {
		return "", nil, err
	}
	unlock, err := lockFolder(ctx, o.Folder+".lock", o.Waiting)
	if err != nil {
		return "", nil, err
	}
	dir, reset, err := stableDriverDir(ctx, o)
	if err != nil {
		unlock()
		return "", nil, err
	}
	return dir, func() { reset(); unlock() }, nil
}

// HandoffFolder is a repository's hand-off folder under hopsesh's state folder:
// <state>/handoff/<host>/<owner>/<repo>, from the repository's identity, so the folder a
// driver names (Claude Code's trust question shows it) says whose it is.
func HandoffFolder(stateDir, identity string) string {
	parts := []string{stateDir, "handoff"}
	for _, p := range strings.Split(identity, "/") {
		p = unsafeFolder.ReplaceAllString(p, "-")
		if p == "" || p == "." || p == ".." {
			continue
		}
		parts = append(parts, p)
	}
	if len(parts) == 2 {
		parts = append(parts, "repo")
	}
	return filepath.Join(parts...)
}

var unsafeFolder = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// stableDriverDir puts the repository's hand-off folder on the branch.
func stableDriverDir(ctx context.Context, o DriverOptions) (string, func(), error) {
	g, dir := Here{}, o.Folder
	if o.Checkout == "" {
		return dir, func() {}, cloneDriverDir(ctx, o)
	}
	start := o.Start
	if _, err := g.Run(ctx, o.Checkout, pushEnv, "fetch", "--quiet", "--no-tags", "origin", "+refs/heads/"+o.Branch+":refs/remotes/origin/"+o.Branch); err == nil && start == "" {
		start = "refs/remotes/origin/" + o.Branch
	}
	exists := BranchExists(ctx, o.Checkout, o.Branch)
	if !exists && start == "" {
		return "", nil, fmt.Errorf("branch %s is neither here nor on origin", o.Branch)
	}
	if !worktreeOf(ctx, dir, o.Checkout) {
		if err := dropFolder(ctx, dir); err != nil {
			return "", nil, err
		}
		_, _ = g.Run(ctx, o.Checkout, nil, "worktree", "prune")
		at := start
		if exists {
			at = "refs/heads/" + o.Branch
		}
		if _, err := g.Run(ctx, o.Checkout, nil, "worktree", "add", "--detach", "--quiet", "--", dir, at); err != nil {
			return "", nil, err
		}
	}
	// Whatever the last driver left, the folder is the branch as it is.
	for _, args := range [][]string{{"reset", "--hard", "--quiet"}, {"clean", "-ffdxq"}} {
		if _, err := g.Run(ctx, dir, nil, args...); err != nil {
			return "", nil, err
		}
	}
	created := false
	if exists {
		if _, err := g.Run(ctx, dir, nil, "checkout", "--force", "--quiet", "--ignore-other-worktrees", o.Branch); err != nil {
			return "", nil, err
		}
	} else {
		if _, err := g.Run(ctx, dir, nil, "checkout", "--force", "--quiet", "-b", o.Branch, start); err != nil {
			return "", nil, err
		}
		created = true
		_, _ = g.Run(ctx, dir, nil, "branch", "--quiet", "--set-upstream-to=origin/"+o.Branch, o.Branch)
	}
	done := func() {
		c := context.WithoutCancel(ctx)
		_, _ = g.Run(c, dir, nil, "checkout", "--detach", "--quiet")
		if created && !o.Keep {
			_, _ = g.Run(c, o.Checkout, nil, "branch", "--quiet", "-D", o.Branch)
		}
	}
	return dir, done, nil
}

// cloneDriverDir makes the folder a shallow clone of o.RemoteURL on o.Branch, reusing the
// clone of an earlier hand-off.
func cloneDriverDir(ctx context.Context, o DriverOptions) error {
	g, dir := Here{}, o.Folder
	if url, err := g.Run(ctx, dir, nil, "remote", "get-url", "origin"); err == nil && strings.TrimSpace(url) == o.RemoteURL && isTop(ctx, dir) {
		ref := "refs/remotes/origin/" + o.Branch
		if _, err := g.Run(ctx, dir, pushEnv, "fetch", "--quiet", "--depth", "1", "--no-tags", "origin", "+refs/heads/"+o.Branch+":"+ref); err == nil {
			for _, args := range [][]string{{"reset", "--hard", "--quiet"}, {"clean", "-ffdxq"}, {"checkout", "--force", "--quiet", "-B", o.Branch, ref}} {
				if _, err = g.Run(ctx, dir, nil, args...); err != nil {
					break
				}
			}
			if err == nil {
				return nil
			}
		}
	}
	if err := dropFolder(ctx, dir); err != nil {
		return err
	}
	_, err := g.Run(ctx, filepath.Dir(dir), pushEnv, "clone", "--quiet", "--depth", "1", "--single-branch", "--branch", o.Branch, "--", o.RemoteURL, dir)
	return err
}

// worktreeOf reports whether dir is a worktree (at its top) of checkout's repository.
func worktreeOf(ctx context.Context, dir, checkout string) bool {
	if !isTop(ctx, dir) {
		return false
	}
	a, b := commonDir(ctx, dir), commonDir(ctx, checkout)
	return a != "" && a == b
}

// isTop reports whether dir is the top of a git working tree.
func isTop(ctx context.Context, dir string) bool {
	if _, err := os.Stat(dir); err != nil {
		return false
	}
	top, err := Here{}.Run(ctx, dir, nil, "rev-parse", "--show-toplevel")
	return err == nil && sameFolder(strings.TrimSpace(top), dir)
}

// commonDir is the repository's own .git folder (shared by its worktrees), resolved.
func commonDir(ctx context.Context, dir string) string {
	out, err := Here{}.Run(ctx, dir, nil, "rev-parse", "--git-common-dir")
	if err != nil {
		return ""
	}
	p := strings.TrimSpace(out)
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return filepath.Clean(p)
}

func sameFolder(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return filepath.Clean(ra) == filepath.Clean(rb)
}

// dropFolder removes a hand-off folder that is not what it should be (a worktree of
// another checkout, a broken one), and the registration its repository has of it.
func dropFolder(ctx context.Context, dir string) error {
	if _, err := os.Stat(dir); err != nil {
		return nil
	}
	old := commonDir(ctx, dir)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if old != "" {
		_, _ = Here{}.Run(ctx, filepath.Dir(dir), nil, "--git-dir="+old, "worktree", "prune")
	}
	return nil
}

// tempDriverDir is a new worktree (or shallow clone) in a temporary folder, removed by
// done.
func tempDriverDir(ctx context.Context, o DriverOptions) (dir string, done func(), err error) {
	checkout, remoteURL, branch, start, keep := o.Checkout, o.RemoteURL, o.Branch, o.Start, o.Keep
	tmp, err := os.MkdirTemp("", "hopsesh-handoff-")
	if err != nil {
		return "", nil, err
	}
	name := "repo"
	if checkout != "" {
		name = filepath.Base(checkout)
	}
	dir = filepath.Join(tmp, name)
	clean := func() { _ = os.RemoveAll(tmp) }
	g := Here{}
	if checkout == "" {
		if _, err := g.Run(ctx, tmp, pushEnv, "clone", "--quiet", "--depth", "1", "--single-branch", "--branch", branch, "--", remoteURL, dir); err != nil {
			clean()
			return "", nil, err
		}
		return dir, clean, nil
	}
	if _, err := g.Run(ctx, checkout, pushEnv, "fetch", "--quiet", "--no-tags", "origin", "+refs/heads/"+branch+":refs/remotes/origin/"+branch); err == nil && start == "" {
		start = "refs/remotes/origin/" + branch
	}
	created := false
	if BranchExists(ctx, checkout, branch) {
		_, err = g.Run(ctx, checkout, nil, "worktree", "add", "--force", "--quiet", "--", dir, branch)
	} else {
		if start == "" {
			clean()
			return "", nil, fmt.Errorf("branch %s is neither here nor on origin", branch)
		}
		_, err = g.Run(ctx, checkout, nil, "worktree", "add", "--quiet", "-b", branch, "--", dir, start)
		created = err == nil
		if created {
			_, _ = g.Run(ctx, checkout, nil, "branch", "--quiet", "--set-upstream-to=origin/"+branch, branch)
		}
	}
	if err != nil {
		clean()
		return "", nil, err
	}
	done = func() {
		c := context.WithoutCancel(ctx)
		_, _ = g.Run(c, checkout, nil, "worktree", "remove", "--force", "--", dir)
		_, _ = g.Run(c, checkout, nil, "worktree", "prune")
		if created && !keep {
			_, _ = g.Run(c, checkout, nil, "branch", "--quiet", "-D", branch)
		}
		clean()
	}
	return dir, done, nil
}

// Refs reaches git remotes through checkouts on any machine, for the journal's undo of a
// pushed branch (it implements journal.Refs). Machines returns git on a machine by name.
type Refs struct {
	Machines func(ctx context.Context, machine string) (Git, error)
}

// RemoteRef returns the commit ref points at on remote, as the checkout dir on machine
// sees it ("" when it is not there).
func (r Refs) RemoteRef(ctx context.Context, machine, dir, remote, ref string) (string, error) {
	g, err := r.Machines(ctx, machine)
	if err != nil {
		return "", err
	}
	return RemoteRef(ctx, g, dir, remote, ref)
}

// RestoreRef pushes sha to ref on remote again (never over a ref of that name).
func (r Refs) RestoreRef(ctx context.Context, machine, dir, remote, ref, sha string) error {
	g, err := r.Machines(ctx, machine)
	if err != nil {
		return err
	}
	return PushRef(ctx, g, dir, remote, sha, ref)
}

// DeleteRef deletes ref on remote while it points at expect.
func (r Refs) DeleteRef(ctx context.Context, machine, dir, remote, ref, expect string) error {
	g, err := r.Machines(ctx, machine)
	if err != nil {
		return err
	}
	return DeleteRef(ctx, g, dir, remote, ref, expect)
}
