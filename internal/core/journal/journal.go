// Package journal makes every write hopsesh does undoable. Before a file is created,
// replaced, appended to or moved, the journal records how to reverse it (and keeps a copy
// of what is replaced), on disk, so an interrupted move can still be undone. Beyond files,
// it records a branch pushed to a git remote, a session started in a vendor's cloud and a
// session file a vendor's CLI wrote; their undo runs git or the module, through Reach.
package journal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Op is a journaled operation.
type Op string

const (
	OpCreate  Op = "create"  // undo: remove
	OpReplace Op = "replace" // undo: put the backup back
	OpAppend  Op = "append"  // undo: cut out the appended bytes (Size, Len, Sum)
	OpRename  Op = "rename"  // undo: move back
	// OpPushRef is a ref pushed to a git remote. Undo deletes it, with a lease: only while it
	// still points at what hopsesh pushed (Sha).
	OpPushRef Op = "push-ref"
	// OpCloud is a session started in a vendor's cloud. Undo archives it where the cloud
	// can; otherwise archiving it is a step the user owes (Journal.Manual).
	OpCloud Op = "cloud"
	// OpAdopt is a session file a vendor's CLI wrote for the operation (a teleported
	// transcript). Undo sets it aside in the journal, and refuses when it grew since.
	OpAdopt Op = "adopt"
	// OpWorktree is a worktree hopsesh added to a checkout on this machine (Path: the
	// worktree; From: the checkout). Undo removes it, and refuses while it holds changes.
	OpWorktree Op = "worktree"
	// OpRef is a ref hopsesh made or moved in a checkout on this machine (Path: the
	// checkout; Ref; Sha: what hopsesh set it to; Prev: what it was, "" for a new one). A
	// branch hopsesh renamed has From (its name before); Created says the operation made
	// that branch too. Undo puts it back while it still points at Sha.
	OpRef Op = "ref"
	// OpDeletedRef is a branch hopsesh deleted on a git remote once its work was merged (a
	// clean-up): Path is the checkout, Remote the remote's name there, Ref the full ref and
	// Sha the commit it pointed at. Undo pushes it back, and refuses when the remote has a
	// branch of that name again.
	OpDeletedRef Op = "deleted-ref"
)

// Entry is one journaled write.
type Entry struct {
	Op        Op        `json:"op"`
	Machine   string    `json:"machine"`
	Path      string    `json:"path"`
	From      string    `json:"from,omitempty"`   // OpRename
	Size      int64     `json:"size,omitempty"`   // OpAppend: size before
	Len       int64     `json:"len,omitempty"`    // OpAppend: bytes appended
	Sum       string    `json:"sum,omitempty"`    // OpAppend: their SHA-256
	Mtime     time.Time `json:"mtime,omitempty"`  // OpAppend: time before
	Backup    string    `json:"backup,omitempty"` // OpReplace: copy of the old file
	KeepMtime bool      `json:"keepMtime,omitempty"`
	Created   bool      `json:"created,omitempty"` // OpAppend: the append created the file
	// OpAppend: lines written later do not build on these bytes (AppendOptions.Standalone).
	Standalone bool `json:"standalone,omitempty"`
	// OpPushRef: Path is the checkout it was pushed from, Remote the remote's name there,
	// Ref the full ref name and Sha the commit pushed.
	Remote string `json:"remote,omitempty"`
	Ref    string `json:"ref,omitempty"`
	Sha    string `json:"sha,omitempty"`
	Prev   string `json:"prev,omitempty"` // OpRef: what the ref was before ("" for a new one)
	// OpCloud: the cloud, the session (its key: the module and the vendor's id), its page,
	// and when it last changed as hopsesh left it. Machine is the one that ran the driver.
	Cloud   string            `json:"cloud,omitempty"`
	Key     *agent.SessionKey `json:"key,omitempty"`
	URL     string            `json:"url,omitempty"`
	Updated time.Time         `json:"updated,omitempty"`
	// OpAdopt uses Size and Sum for the whole file as adopted.
	// Keep (OpPushRef): the user chose to keep the branch (delete_branch = "never"), so undo
	// leaves it where it is and says so.
	Keep bool `json:"keep,omitempty"`
}

// Journal is the undo record of one operation (a move, a continuation, a hand-off).
type Journal struct {
	UndoTime   time.Time          `json:"undoTime,omitempty"`
	Receipts   []Receipt          `json:"receipts,omitempty"`
	TransferID string             `json:"transferId,omitempty"`
	ID         string             `json:"id"`
	Kind       string             `json:"kind"` // what kind of operation (Kind*)
	Title      string             `json:"title"`
	Time       time.Time          `json:"time"`
	Keys       []agent.SessionKey `json:"keys"` // the sessions it created or changed
	Entries    []Entry            `json:"entries"`
	Undone     bool               `json:"undone,omitempty"`
	// Remote are journals of the same operation kept by hopsesh on other machines (a push):
	// undoing this one undoes them too.
	Remote []Remote `json:"remote,omitempty"`
	// After is the state the operation left each file it created or replaced in (Seal), so
	// undo can tell when one was used afterwards and refuse to lose that work.
	After []State `json:"after,omitempty"`
	// Manual are steps undo could not take, which the user owes: a cloud session hopsesh
	// cannot archive stays in the vendor's list until the user archives it there.
	Manual []Manual `json:"manual,omitempty"`
	// Kept are the branches undo left on their remotes because the user chose to keep them
	// ("origin hopsesh/handoff/…").
	Kept []string `json:"kept,omitempty"`
	// Parts are the journals of the legs of a composite operation (a cloud-to-cloud hop:
	// the fetch, then the hand-off), in order; undoing it undoes them, last first. PartOf
	// is the composite operation a leg belongs to.
	Parts  []string `json:"parts,omitempty"`
	PartOf string   `json:"partOf,omitempty"`

	dir string
	mu  sync.Mutex
}

// Dir is where journals live under the state folder.
func Dir(stateDir string) string { return filepath.Join(stateDir, "journal") }

// Kinds of operations.
const (
	KindMove     = "move"     // a session brought here in its own agent
	KindContinue = "continue" // a session continued in another agent
	KindPush     = "push"     // a session sent to another machine (its lineage here)
	KindHandoff  = "handoff"  // a session handed off to a cloud (a branch, a cloud session, lineage)
	KindFetch    = "fetch"    // a session brought from a cloud (a worktree, an adopted session)
	KindHop      = "hop"      // a cloud session handed on to another cloud through this machine (Parts)
	KindCleanup  = "cleanup"  // branches deleted on a remote once their work was merged
	KindRename   = "rename"   // a session given a new title in its agent's own data
)

// New starts a journal of one operation.
func New(stateDir, kind, title string) (*Journal, error) {
	base := strings.ReplaceAll(time.Now().UTC().Format("20060102T150405.000Z"), ".", "")
	if err := os.MkdirAll(Dir(stateDir), 0o700); err != nil {
		return nil, err
	}
	// Two operations in the same millisecond (the legs of a hop) get ids of their own; a
	// later one sorts after an earlier one.
	id := base
	for n := 2; ; n++ {
		err := os.Mkdir(filepath.Join(Dir(stateDir), id), 0o700)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) || n > 999 {
			return nil, err
		}
		id = fmt.Sprintf("%s-%03d", base, n)
	}
	j := &Journal{ID: id, Kind: kind, Title: title, Time: time.Now().UTC(), dir: filepath.Join(Dir(stateDir), id)}
	if err := os.MkdirAll(filepath.Join(j.dir, "backup"), 0o700); err != nil {
		return nil, err
	}
	return j, j.save()
}

// AddKey records a session the journal concerns.
func (j *Journal) AddKey(k agent.SessionKey) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, x := range j.Keys {
		if x == k {
			return
		}
	}
	j.Keys = append(j.Keys, k)
	_ = j.saveLocked()
}

// Save writes the journal as it is now.
func (j *Journal) Save() error { return j.save() }

func (j *Journal) save() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.saveLocked()
}

func (j *Journal) saveLocked() error {
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	return host.LocalFS().WriteFile(filepath.Join(j.dir, "journal.json"), b, 0o600)
}

// State is a file's size and SHA-256.
type State struct {
	Machine string `json:"machine"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Sum     string `json:"sum"`
}

// ErrChanged means a file the operation wrote changed since: undoing it would lose that
// later work.
var ErrChanged = errors.New("changed since")

func fileState(fsys host.FS, machine, p string) (State, error) {
	f, err := fsys.Open(p)
	if err != nil {
		return State{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return State{}, err
	}
	return State{Machine: machine, Path: p, Size: n, Sum: hex.EncodeToString(h.Sum(nil))}, nil
}

// Seal records the state the operation left each file it created or replaced in. Files on
// machines fsFor cannot reach are left out (their undo is not checked).
func (j *Journal) Seal(fsFor func(machine string) (host.FS, error)) error {
	j.mu.Lock()
	entries := append([]Entry(nil), j.Entries...)
	j.mu.Unlock()
	var after []State
	seen := map[string]bool{}
	for _, e := range entries {
		if e.Op != OpCreate && e.Op != OpReplace || seen[e.Machine+"\x00"+e.Path] {
			continue
		}
		seen[e.Machine+"\x00"+e.Path] = true
		fsys, err := fsFor(e.Machine)
		if err != nil {
			continue
		}
		if st, err := fileState(fsys, e.Machine, e.Path); err == nil {
			after = append(after, st)
		}
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.After = after
	return j.saveLocked()
}

// Changed reports the first thing that changed since the operation (ErrChanged with what
// changed), or nil: a file it created or replaced, or one it appended to (not Standalone)
// that has more after its bytes; a file a vendor's CLI wrote that grew; a ref it pushed
// that moved on; a cloud session with activity since. What r cannot reach (a machine, a
// remote, a cloud) does not count, nor does a file or a ref that is gone.
func (j *Journal) Changed(ctx context.Context, r Reach) error { return j.changed(ctx, r, nil) }

// ChangedBesides is Changed for an operation later ones built on (the first leg of a hop):
// a file a later journal wrote too is left to that journal's own check, since undoing the
// later one first puts it back.
func (j *Journal) ChangedBesides(ctx context.Context, r Reach, later []*Journal) error {
	touched := map[string]bool{}
	for _, l := range later {
		for _, e := range l.Entries {
			touched[e.Machine+"\x00"+e.Path] = true
		}
	}
	return j.changed(ctx, r, func(machine, p string) bool { return touched[machine+"\x00"+p] })
}

func (j *Journal) changed(ctx context.Context, r Reach, skip func(machine, p string) bool) error {
	if skip == nil {
		skip = func(string, string) bool { return false }
	}
	for i, e := range j.Entries {
		if (e.Op == OpAppend || e.Op == OpAdopt) && skip(e.Machine, e.Path) {
			continue
		}
		switch e.Op {
		case OpAppend:
			if e.Standalone || j.appendedAgain(i) {
				continue
			}
			fsys, err := r.fs(e.Machine)
			if err != nil {
				continue
			}
			after, err := bytesAfter(fsys, e)
			if err != nil {
				continue
			}
			if after != 0 {
				what := "what hopsesh added was changed"
				if after > 0 {
					what = fmt.Sprintf("%s were added after this", humanBytes(after))
				}
				return fmt.Errorf("%w: %s on %s (%s)", ErrChanged, filepath.Base(e.Path), e.Machine, what)
			}
		case OpAdopt:
			fsys, err := r.fs(e.Machine)
			if err != nil {
				continue
			}
			if now, err := fileState(fsys, e.Machine, e.Path); err == nil && now.Sum != e.Sum {
				what := "it was used after this"
				if now.Size > e.Size {
					what = fmt.Sprintf("%s were added after this", humanBytes(now.Size-e.Size))
				}
				return fmt.Errorf("%w: %s on %s (%s)", ErrChanged, filepath.Base(e.Path), e.Machine, what)
			}
		case OpPushRef:
			if r.Refs == nil || e.Keep {
				continue
			}
			if now, err := r.Refs.RemoteRef(ctx, e.Machine, e.Path, e.Remote, e.Ref); err == nil && now != "" && now != e.Sha {
				return fmt.Errorf("%w: %s on %s moved on from what hopsesh pushed (%s is now %s)", ErrChanged, shortRef(e.Ref), e.Remote, short(e.Sha), short(now))
			}
		case OpDeletedRef:
			if r.Refs == nil {
				continue
			}
			if now, err := r.Refs.RemoteRef(ctx, e.Machine, e.Path, e.Remote, e.Ref); err == nil && now != "" {
				return fmt.Errorf("%w: %s on %s is there again (at %s)", ErrChanged, shortRef(e.Ref), e.Remote, short(now))
			}
		case OpCloud:
			if r.Clouds == nil || e.Key == nil {
				continue
			}
			if now, err := r.Clouds.Updated(ctx, e.Cloud, *e.Key); err == nil && now.After(e.Updated) {
				return fmt.Errorf("%w: the %s session %s has new activity", ErrChanged, e.Cloud, e.Key.Session)
			}
		case OpWorktree:
			if r.Git == nil {
				continue
			}
			if dirty, err := r.Git.WorktreeDirty(ctx, e.Path); err == nil && dirty {
				return fmt.Errorf("%w: the worktree %s has changes that are not committed", ErrChanged, e.Path)
			}
		case OpRef:
			if r.Git == nil {
				continue
			}
			if now, err := r.Git.Ref(ctx, e.Path, e.Ref); err == nil && now != "" && now != e.Sha {
				return fmt.Errorf("%w: %s moved on from what hopsesh left (%s is now %s)", ErrChanged, shortRef(e.Ref), short(e.Sha), short(now))
			}
		}
	}
	for _, a := range j.After {
		if skip(a.Machine, a.Path) {
			continue
		}
		fsys, err := r.fs(a.Machine)
		if err != nil {
			continue
		}
		now, err := fileState(fsys, a.Machine, a.Path)
		if err != nil {
			continue
		}
		if now.Sum != a.Sum {
			what := "it was used after this"
			if now.Size > a.Size {
				what = fmt.Sprintf("%s were added after this", humanBytes(now.Size-a.Size))
			}
			return fmt.Errorf("%w: %s on %s (%s)", ErrChanged, filepath.Base(a.Path), a.Machine, what)
		}
	}
	return nil
}

func (r Reach) fs(machine string) (host.FS, error) {
	if r.FS == nil {
		return nil, errors.New("no machine can be reached")
	}
	return r.FS(machine)
}

// shortRef is a ref without refs/heads/.
func shortRef(ref string) string { return strings.TrimPrefix(ref, "refs/heads/") }

// Git reaches checkouts on this machine, for the worktrees and refs an operation made; the
// repos package implements it.
type Git interface {
	// Ref returns the commit ref points at in the checkout dir ("" when there is no such
	// ref, or no such checkout any more).
	Ref(ctx context.Context, dir, ref string) (string, error)
	// SetRef sets ref to sha ("" deletes it) if it still points at expect (a lease).
	SetRef(ctx context.Context, dir, ref, sha, expect string) error
	// RenameBranch renames a branch (full ref names).
	RenameBranch(ctx context.Context, dir, from, to string) error
	// WorktreeDirty reports whether a worktree has changes that are not committed (false
	// when it is gone).
	WorktreeDirty(ctx context.Context, worktree string) (bool, error)
	// RemoveWorktree removes a worktree of the checkout dir (force: with its changes).
	RemoveWorktree(ctx context.Context, dir, worktree string, force bool) error
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// appendedAgain reports whether a later entry of the journal appends to the same file: the
// later one's bytes follow these, and its own check covers what comes after.
func (j *Journal) appendedAgain(i int) bool {
	for _, e := range j.Entries[i+1:] {
		if e.Op == OpAppend && e.Machine == j.Entries[i].Machine && e.Path == j.Entries[i].Path {
			return true
		}
	}
	return false
}

// bytesAfter is how many bytes follow what an Append added, or -1 when those bytes are no
// longer there.
func bytesAfter(fsys host.FS, e Entry) (int64, error) {
	fi, err := fsys.Stat(e.Path)
	if err != nil {
		return 0, err
	}
	data, err := fsys.ReadFile(e.Path, fi.Size()+1)
	if err != nil {
		return 0, err
	}
	at, lead, ok := findAppended(data, e)
	if !ok {
		return -1, nil
	}
	return int64(len(data)) - (at + lead + e.Len), nil
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}

// Manual is a cloud session undo could not archive: the user archives it on its page.
type Manual struct {
	Cloud string           `json:"cloud"`
	Key   agent.SessionKey `json:"key"`
	URL   string           `json:"url,omitempty"`
}

// Reach is how undo gets at what an operation changed.
type Reach struct {
	// FS returns a machine's filesystem by name (an error when it cannot be reached; such
	// entries are reported).
	FS func(machine string) (host.FS, error)
	// Refs reaches git remotes (nil: pushed refs are reported, not deleted).
	Refs Refs
	// Clouds reaches vendor clouds (nil: their sessions become steps the user owes).
	Clouds Clouds
	// Git reaches checkouts on this machine (nil: worktrees and refs are reported, not
	// undone).
	Git Git
}

// Files is a Reach of files only.
func Files(fsFor func(machine string) (host.FS, error)) Reach { return Reach{FS: fsFor} }

// Refs reads and deletes refs on a checkout's git remote, for undo; the repos package
// implements it.
type Refs interface {
	// RemoteRef returns the commit ref points at on remote, as the checkout dir on machine
	// sees it ("" when there is no such ref).
	RemoteRef(ctx context.Context, machine, dir, remote, ref string) (string, error)
	// DeleteRef deletes ref on remote if it still points at expect (a lease), and refuses
	// otherwise.
	DeleteRef(ctx context.Context, machine, dir, remote, ref, expect string) error
	// RestoreRef pushes sha to ref on remote again, and refuses when the remote has a ref of
	// that name.
	RestoreRef(ctx context.Context, machine, dir, remote, ref, sha string) error
}

// Clouds reaches the sessions an operation started in vendor clouds, through the modules'
// cloud capabilities.
type Clouds interface {
	// Updated is when the session last changed, as the cloud lists it (zero when the
	// cloud does not say).
	Updated(ctx context.Context, cloud string, key agent.SessionKey) (time.Time, error)
	// Archive archives the session, or returns ErrManual when the cloud cannot.
	Archive(ctx context.Context, cloud string, key agent.SessionKey) error
}

// ErrManual means a cloud cannot archive its sessions from hopsesh: the user does it on
// the session's page.
var ErrManual = errors.New("only the user can archive it, on its page")

// PushRef records a ref about to be pushed from the checkout dir on machine to remote, so
// undo can delete it again (only while it still points at sha).
func (j *Journal) PushRef(machine, dir, remote, ref, sha string) error {
	return j.record(Entry{Op: OpPushRef, Machine: machine, Path: dir, Remote: remote, Ref: ref, Sha: sha})
}

// KeepPushed records a ref about to be pushed that undo must leave where it is: the user
// chose to keep it (it is reported in Kept).
func (j *Journal) KeepPushed(machine, dir, remote, ref, sha string) error {
	return j.record(Entry{Op: OpPushRef, Machine: machine, Path: dir, Remote: remote, Ref: ref, Sha: sha, Keep: true})
}

// DeletedRef records a branch about to be deleted on remote (it points at sha), so undo
// pushes it back.
func (j *Journal) DeletedRef(machine, dir, remote, ref, sha string) error {
	return j.record(Entry{Op: OpDeletedRef, Machine: machine, Path: dir, Remote: remote, Ref: ref, Sha: sha})
}

// AddPart records the journal of a leg of this composite operation (in order).
func (j *Journal) AddPart(id string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Parts = append(j.Parts, id)
	return j.saveLocked()
}

// SetPartOf records the composite operation this journal is a leg of.
func (j *Journal) SetPartOf(id string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.PartOf = id
	return j.saveLocked()
}

// Cloud records a session the operation started in a cloud (machine ran the driver).
func (j *Journal) Cloud(machine string, s agent.CloudSession) error {
	key := s.Key
	return j.record(Entry{Op: OpCloud, Machine: machine, Cloud: s.Cloud, Key: &key, URL: s.URL, Updated: s.Updated})
}

// Adopt records a session file a vendor's CLI just wrote on machine for this operation
// (its state now), so undo sets it aside, unless it grew since.
func (j *Journal) Adopt(fsys host.FS, machine, p string) error {
	st, err := fileState(fsys, machine, p)
	if err != nil {
		return err
	}
	return j.record(Entry{Op: OpAdopt, Machine: machine, Path: p, Size: st.Size, Sum: st.Sum})
}

// Worktree records a worktree hopsesh added at path to the checkout dir on machine, so
// undo removes it.
func (j *Journal) Worktree(machine, dir, path string) error {
	return j.record(Entry{Op: OpWorktree, Machine: machine, Path: path, From: dir})
}

// Ref records a ref hopsesh set to sha in the checkout dir on machine (prev: what it was,
// "" when new), so undo puts it back.
func (j *Journal) Ref(machine, dir, ref, sha, prev string) error {
	return j.record(Entry{Op: OpRef, Machine: machine, Path: dir, Ref: ref, Sha: sha, Prev: prev})
}

// RenamedBranch records a branch renamed from from to ref (both full ref names) at sha in
// the checkout dir; created: the operation made the branch too, so undo deletes it rather
// than giving back the old name.
func (j *Journal) RenamedBranch(machine, dir, from, ref, sha string, created bool) error {
	return j.record(Entry{Op: OpRef, Machine: machine, Path: dir, From: from, Ref: ref, Sha: sha, Created: created})
}

// Remote names a journal on another machine's hopsesh.
type Remote struct {
	Machine string `json:"machine"` // as this machine's configuration names it
	ID      string `json:"id"`
}

// AddRemote records a journal the same operation left on another machine.
func (j *Journal) AddRemote(machine, id string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Remote = append(j.Remote, Remote{Machine: machine, ID: id})
	return j.saveLocked()
}

// RecordCreated records a file another program just created for this operation (an
// agent's own importer), so undo removes it.
func (j *Journal) RecordCreated(machine, p string) error {
	return j.record(Entry{Op: OpCreate, Machine: machine, Path: p})
}

// Forget drops the entries for a machine whose own hopsesh journals those writes (the
// sender of a push replays and journals them there).
func (j *Journal) Forget(machine string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	kept := j.Entries[:0]
	for _, e := range j.Entries {
		if e.Machine != machine {
			kept = append(kept, e)
		}
	}
	j.Entries = kept
	return j.saveLocked()
}

// record writes an entry ahead of the operation it describes.
func (j *Journal) record(e Entry) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Entries = append(j.Entries, e)
	return j.saveLocked()
}

// WriteFile replaces or creates a file, keeping a copy of the old one.
func (j *Journal) WriteFile(fsys host.FS, machine, p string, b []byte, perm fs.FileMode) error {
	e := Entry{Op: OpCreate, Machine: machine, Path: p}
	if _, err := fsys.Stat(p); err == nil {
		backup, err := j.backup(fsys, p)
		if err != nil {
			return err
		}
		e.Op, e.Backup = OpReplace, backup
	}
	if err := j.record(e); err != nil {
		return err
	}
	return fsys.WriteFile(p, b, perm)
}

// Append adds to a file, remembering where and what, so undo can take out exactly these
// bytes, even from between lines written later when they are Standalone.
func (j *Journal) Append(fsys host.FS, machine, p string, b []byte, o agent.AppendOptions) error {
	sum := sha256.Sum256(b)
	e := Entry{Op: OpAppend, Machine: machine, Path: p, Len: int64(len(b)), Sum: hex.EncodeToString(sum[:]), KeepMtime: o.KeepMtime, Standalone: o.Standalone}
	fi, err := fsys.Stat(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		e.Created, e.KeepMtime = true, false // undo removes it once nothing else is in it
	case err != nil:
		return err
	default:
		e.Size, e.Mtime = fi.Size(), fi.ModTime()
	}
	if err := j.record(e); err != nil {
		return err
	}
	return fsys.Append(p, b, o)
}

// undoAppend removes the bytes an Append added (and the newline Append may have put
// before them); if they changed, nothing is. What was written after Standalone bytes
// stays; after other bytes it built on them, so a forced undo cuts the file there.
func undoAppend(fsys host.FS, e Entry) error {
	fi, err := fsys.Stat(e.Path)
	if err != nil {
		return err
	}
	data, err := fsys.ReadFile(e.Path, fi.Size()+1)
	if err != nil {
		return err
	}
	at, lead, ok := findAppended(data, e)
	if !ok {
		return fmt.Errorf("what hopsesh added to %s has changed since; left as it is", e.Path)
	}
	end := at + lead + e.Len
	if !e.Standalone {
		end = int64(len(data)) // Undo refused this unless forced
	}
	if end == int64(len(data)) {
		if e.Created && at == 0 {
			return fsys.Remove(e.Path) // nothing else is in it
		}
		if err := fsys.Truncate(e.Path, at); err != nil {
			return err
		}
	} else {
		// Keep the newline Append put in front: it ends the line before, which later lines
		// now follow.
		kept := append(append([]byte{}, data[:at+lead]...), data[end:]...)
		if err := fsys.WriteFile(e.Path, kept, fi.Mode().Perm()); err != nil {
			return err
		}
		return nil // the file has newer content: its time stays current
	}
	if e.KeepMtime {
		return fsys.Chtimes(e.Path, e.Mtime)
	}
	return nil
}

// findAppended locates the bytes an Append added: where it put them (after a newline
// Append may have added first), else at the start of a line, for when an earlier append
// to the same file was taken out since. It returns their offset and whether that
// newline comes first.
func findAppended(data []byte, e Entry) (at, lead int64, ok bool) {
	match := func(from int64) bool {
		if from < 0 || from+e.Len > int64(len(data)) {
			return false
		}
		sum := sha256.Sum256(data[from : from+e.Len])
		return hex.EncodeToString(sum[:]) == e.Sum
	}
	if match(e.Size) {
		return e.Size, 0, true
	}
	if e.Size < int64(len(data)) && data[e.Size] == '\n' && match(e.Size+1) {
		return e.Size, 1, true
	}
	for from := int64(0); from+e.Len <= int64(len(data)); from++ {
		if (from == 0 || data[from-1] == '\n') && match(from) {
			return from, 0, true
		}
	}
	return 0, 0, false
}

// Rename moves a file, remembering where from.
func (j *Journal) Rename(fsys host.FS, machine, from, to string) error {
	if _, err := fsys.Stat(to); err == nil {
		return fmt.Errorf("%s already exists", to)
	}
	if err := j.record(Entry{Op: OpRename, Machine: machine, Path: to, From: from}); err != nil {
		return err
	}
	return fsys.Rename(from, to)
}

// Place installs a staged local file at dst on this machine (moving, not copying, when
// both are on one volume). An existing dst is kept in the journal.
func (j *Journal) Place(machine, staged, dst string, perm fs.FileMode) error {
	e := Entry{Op: OpCreate, Machine: machine, Path: dst}
	if _, err := os.Lstat(dst); err == nil {
		backup := j.backupName(dst)
		if err := moveFile(dst, backup); err != nil {
			return err
		}
		e.Op, e.Backup = OpReplace, backup
	}
	if err := j.record(e); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if err := moveFile(staged, dst); err != nil {
		return err
	}
	return os.Chmod(dst, perm)
}

// SetAside moves a local file into the journal (it comes back on undo).
func (j *Journal) SetAside(machine, p string) error {
	if _, err := os.Lstat(p); err != nil {
		return nil
	}
	backup := j.backupName(p)
	if err := j.record(Entry{Op: OpRename, Machine: machine, Path: backup, From: p}); err != nil {
		return err
	}
	return moveFile(p, backup)
}

func (j *Journal) backupName(p string) string {
	j.mu.Lock()
	n := len(j.Entries)
	j.mu.Unlock()
	return filepath.Join(j.dir, "backup", fmt.Sprintf("%04d-%s", n, filepath.Base(p)))
}

func (j *Journal) backup(fsys host.FS, p string) (string, error) {
	dst := j.backupName(p)
	return dst, copyOut(fsys, p, dst)
}

// copyOut copies a file from a machine to a local file.
func copyOut(fsys host.FS, p, dst string) error {
	src, err := fsys.Open(p)
	if err != nil {
		return err
	}
	defer src.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Undo reverses a journal's entries, newest first, reaching machines, remotes and clouds
// through r (what it cannot reach is reported). Unless force, it refuses (ErrChanged) when
// something the operation did changed since: undoing would lose that later work. A forced
// undo deletes a pushed ref wherever it points now. A cloud session undo cannot archive is
// recorded in Manual, for the user.
func (j *Journal) Undo(ctx context.Context, r Reach, force bool) error {
	return j.undo(ctx, r, force, nil)
}

// UndoAfter permits only lineage compensation written by an already undone leg of
// the same composite transaction. Native transcript and code checks remain active.
func (j *Journal) UndoAfter(ctx context.Context, r Reach, force bool, later []*Journal) error {
	touched := map[string]bool{}
	for _, l := range later {
		if !l.Undone {
			return fmt.Errorf("later leg is not undone")
		}
		for _, e := range l.Entries {
			if strings.HasSuffix(e.Path, lineage.Suffix) {
				touched[e.Machine+"\x00"+e.Path] = true
			}
		}
	}
	return j.undo(ctx, r, force, func(machine, path string) bool { return touched[machine+"\x00"+path] })
}
func (j *Journal) undo(ctx context.Context, r Reach, force bool, skip func(string, string) bool) error {
	if !force {
		if err := j.changed(ctx, r, skip); err != nil {
			return err
		}
	}
	if j.UndoTime.IsZero() {
		j.UndoTime = time.Now().UTC()
		if err := j.Save(); err != nil {
			return err
		}
	}
	captured := j.captureLineage(r)
	var problems, kept []string
	var manual []Manual
	// Worktrees go first: a branch is only renamed or deleted once no worktree hopsesh made
	// has it checked out.
	for i := len(j.Entries) - 1; i >= 0; i-- {
		if e := j.Entries[i]; e.Op == OpWorktree {
			if err := undoWorktree(ctx, r, e, force); err != nil {
				problems = append(problems, fmt.Sprintf("the worktree %s: %v", e.Path, err))
			}
		}
	}
	for i := len(j.Entries) - 1; i >= 0; i-- {
		e := j.Entries[i]
		switch e.Op {
		case OpWorktree:
			continue
		case OpRef:
			if err := undoRef(ctx, r, e, force); err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v", shortRef(e.Ref), err))
			}
			continue
		case OpPushRef:
			if e.Keep {
				kept = append(kept, e.Remote+" "+shortRef(e.Ref))
				continue
			}
			if err := undoPushRef(ctx, r, e, force); err != nil {
				problems = append(problems, fmt.Sprintf("%s on %s: %v", shortRef(e.Ref), e.Remote, err))
			}
			continue
		case OpDeletedRef:
			if r.Refs == nil {
				problems = append(problems, fmt.Sprintf("%s on %s: the remote cannot be reached from here", shortRef(e.Ref), e.Remote))
			} else if err := r.Refs.RestoreRef(ctx, e.Machine, e.Path, e.Remote, e.Ref, e.Sha); err != nil {
				problems = append(problems, fmt.Sprintf("%s on %s: %v", shortRef(e.Ref), e.Remote, err))
			}
			continue
		case OpCloud:
			if e.Key == nil {
				continue
			}
			err := ErrManual
			if r.Clouds != nil {
				err = r.Clouds.Archive(ctx, e.Cloud, *e.Key)
			}
			switch {
			case errors.Is(err, ErrManual):
				manual = append(manual, Manual{Cloud: e.Cloud, Key: *e.Key, URL: e.URL})
			case err != nil:
				problems = append(problems, fmt.Sprintf("the %s session %s: %v", e.Cloud, e.Key.Session, err))
			}
			continue
		}
		fsys, err := r.fs(e.Machine)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s on %s: %v", e.Path, e.Machine, err))
			continue
		}
		if e.Op == OpAdopt {
			err = j.setAside(fsys, i, e.Path)
		} else {
			err = undoEntry(fsys, e)
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			problems = append(problems, fmt.Sprintf("%s: %v", e.Path, err))
		}
	}
	if len(problems) == 0 {
		if err := j.compensateLineage(r, captured); err != nil {
			problems = append(problems, err.Error())
		}
	}
	j.mu.Lock()
	j.Undone, j.Manual, j.Kept = len(problems) == 0, manual, kept
	err := j.saveLocked()
	j.mu.Unlock()
	if err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return fmt.Errorf("undo was incomplete:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}

// undoPushRef deletes a pushed ref while it still points at what was pushed; forced, at
// whatever it points at now. A ref that is gone already is fine.
func undoPushRef(ctx context.Context, r Reach, e Entry, force bool) error {
	if r.Refs == nil {
		return errors.New("the remote cannot be reached from here")
	}
	expect := e.Sha
	if force {
		now, err := r.Refs.RemoteRef(ctx, e.Machine, e.Path, e.Remote, e.Ref)
		if err != nil {
			return err
		}
		if now == "" {
			return nil
		}
		expect = now
	}
	return r.Refs.DeleteRef(ctx, e.Machine, e.Path, e.Remote, e.Ref, expect)
}

// undoWorktree removes a worktree hopsesh added (one already gone is fine).
func undoWorktree(ctx context.Context, r Reach, e Entry, force bool) error {
	if r.Git == nil {
		return errors.New("git cannot be reached from here")
	}
	return r.Git.RemoveWorktree(ctx, e.From, e.Path, force)
}

// undoRef puts a ref back while it points at what hopsesh left; forced, wherever it points.
// A ref that is gone already is fine.
func undoRef(ctx context.Context, r Reach, e Entry, force bool) error {
	if r.Git == nil {
		return errors.New("git cannot be reached from here")
	}
	now, err := r.Git.Ref(ctx, e.Path, e.Ref)
	if err != nil || now == "" {
		return err
	}
	if now != e.Sha && !force {
		return fmt.Errorf("it moved on to %s", short(now))
	}
	switch {
	case e.From != "" && !e.Created:
		return r.Git.RenameBranch(ctx, e.Path, e.Ref, e.From)
	case e.Prev == "":
		return r.Git.SetRef(ctx, e.Path, e.Ref, "", now)
	}
	return r.Git.SetRef(ctx, e.Path, e.Ref, e.Prev, now)
}

// setAside moves the file of entry i (on any machine) into the journal's backup folder.
func (j *Journal) setAside(fsys host.FS, i int, p string) error {
	dst := filepath.Join(j.dir, "backup", fmt.Sprintf("adopted-%04d-%s", i, filepath.Base(p)))
	if err := copyOut(fsys, p, dst); err != nil {
		return err
	}
	return fsys.Remove(p)
}

func undoEntry(fsys host.FS, e Entry) error {
	switch e.Op {
	case OpCreate:
		return fsys.Remove(e.Path)
	case OpReplace:
		b, err := os.ReadFile(e.Backup)
		if err != nil {
			return err
		}
		return fsys.WriteFile(e.Path, b, 0o600)
	case OpAppend:
		return undoAppend(fsys, e)
	case OpRename:
		return fsys.Rename(e.Path, e.From)
	}
	return fmt.Errorf("unknown journal operation %q", e.Op)
}

// List returns the journals, newest first.
func List(stateDir string) ([]*Journal, error) {
	entries, err := os.ReadDir(Dir(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Journal
	for _, e := range entries {
		if j, err := Load(stateDir, e.Name()); err == nil {
			out = append(out, j)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID > out[b].ID })
	return out, nil
}

// Load reads one journal.
func Load(stateDir, id string) (*Journal, error) {
	dir := filepath.Join(Dir(stateDir), filepath.Base(id))
	b, err := os.ReadFile(filepath.Join(dir, "journal.json"))
	if err != nil {
		return nil, err
	}
	var j Journal
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, err
	}
	j.dir = dir
	return &j, nil
}

func moveFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Remove(src)
}
