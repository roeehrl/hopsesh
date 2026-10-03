// Package journal makes every write hopsesh does undoable. Before a file is created,
// replaced, appended to or moved, the journal records how to reverse it (and keeps a copy
// of what is replaced), on disk, so an interrupted move can still be undone.
package journal

import (
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
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Op is a journaled operation.
type Op string

const (
	OpCreate  Op = "create"  // undo: remove
	OpReplace Op = "replace" // undo: put the backup back
	OpAppend  Op = "append"  // undo: cut out the appended bytes (Size, Len, Sum)
	OpRename  Op = "rename"  // undo: move back
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
}

// Journal is the undo record of one operation (a move, a continuation, a mark).
type Journal struct {
	ID      string             `json:"id"`
	Title   string             `json:"title"`
	Time    time.Time          `json:"time"`
	Keys    []agent.SessionKey `json:"keys"` // the sessions it created or changed
	Entries []Entry            `json:"entries"`
	Undone  bool               `json:"undone,omitempty"`
	// Remote are journals of the same operation kept by hopsesh on other machines (a push):
	// undoing this one undoes them too.
	Remote []Remote `json:"remote,omitempty"`
	// After is the state the operation left each file it created or replaced in (Seal), so
	// undo can tell when one was used afterwards and refuse to lose that work.
	After []State `json:"after,omitempty"`

	dir string
	mu  sync.Mutex
}

// Dir is where journals live under the state folder.
func Dir(stateDir string) string { return filepath.Join(stateDir, "journal") }

// New starts a journal.
func New(stateDir, title string) (*Journal, error) {
	id := time.Now().UTC().Format("20060102T150405.000Z")
	id = strings.ReplaceAll(id, ".", "")
	j := &Journal{ID: id, Title: title, Time: time.Now().UTC(), dir: filepath.Join(Dir(stateDir), id)}
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
	tmp := filepath.Join(j.dir, "journal.json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(j.dir, "journal.json"))
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

// Changed reports the first file that changed since the operation (ErrChanged with what
// changed), or nil. A file that is gone, or a machine fsFor cannot reach, does not count.
func (j *Journal) Changed(fsFor func(machine string) (host.FS, error)) error {
	for _, a := range j.After {
		fsys, err := fsFor(a.Machine)
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

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
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

// Adopt records a file another program just created for this operation (an agent's own
// importer), so undo removes it.
func (j *Journal) Adopt(machine, p string) error {
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
// bytes even after the agent appended more.
func (j *Journal) Append(fsys host.FS, machine, p string, b []byte, o agent.AppendOptions) error {
	fi, err := fsys.Stat(p)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	if err := j.record(Entry{Op: OpAppend, Machine: machine, Path: p, Size: fi.Size(), Len: int64(len(b)), Sum: hex.EncodeToString(sum[:]),
		Mtime: fi.ModTime(), KeepMtime: o.KeepMtime}); err != nil {
		return err
	}
	return fsys.Append(p, b, o)
}

// undoAppend removes the bytes an Append added (and the newline Append may have put
// before them). Whatever was written after them stays; if they changed, nothing is.
func undoAppend(fsys host.FS, e Entry) error {
	fi, err := fsys.Stat(e.Path)
	if err != nil {
		return err
	}
	data, err := fsys.ReadFile(e.Path, fi.Size()+1)
	if err != nil {
		return err
	}
	if int64(len(data)) < e.Size+e.Len {
		return fmt.Errorf("%s is shorter than when hopsesh added to it; left as it is", e.Path)
	}
	rest := data[e.Size:]
	n, lead := int64(-1), int64(0)
	for _, lead = range []int64{0, 1} {
		if lead == 1 && (len(rest) == 0 || rest[0] != '\n') {
			continue
		}
		if e.Size+lead+e.Len > int64(len(data)) {
			continue
		}
		if sum := sha256.Sum256(rest[lead : lead+e.Len]); hex.EncodeToString(sum[:]) == e.Sum {
			n = lead + e.Len
			break
		}
	}
	if n < 0 {
		return fmt.Errorf("what hopsesh added to %s has changed since; left as it is", e.Path)
	}
	if e.Size+n == int64(len(data)) {
		if err := fsys.Truncate(e.Path, e.Size); err != nil {
			return err
		}
	} else {
		// Keep the newline Append put in front: it ends the line before, which later lines
		// now follow.
		kept := append(append([]byte{}, data[:e.Size+lead]...), rest[n:]...)
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
	src, err := fsys.Open(p)
	if err != nil {
		return "", err
	}
	defer src.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		return "", err
	}
	return dst, out.Close()
}

// Undo reverses a journal's entries, newest first. fsFor returns the filesystem of a
// machine by name (an error when it cannot be reached; such entries are reported).
// Unless force, it refuses (ErrChanged) when a file the operation wrote changed since:
// undoing would lose that later work.
func (j *Journal) Undo(fsFor func(machine string) (host.FS, error), force bool) error {
	if !force {
		if err := j.Changed(fsFor); err != nil {
			return err
		}
	}
	var problems []string
	for i := len(j.Entries) - 1; i >= 0; i-- {
		e := j.Entries[i]
		fsys, err := fsFor(e.Machine)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s on %s: %v", e.Path, e.Machine, err))
			continue
		}
		if err := undoEntry(fsys, e); err != nil && !errors.Is(err, fs.ErrNotExist) {
			problems = append(problems, fmt.Sprintf("%s: %v", e.Path, err))
		}
	}
	j.Undone = true
	if err := j.save(); err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return fmt.Errorf("undo was incomplete:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
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
