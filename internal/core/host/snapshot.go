package host

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// SnapshotFile is one file of a snapshot: its path on the machine it came from.
type SnapshotFile struct {
	Path    string      `json:"path"`
	Data    []byte      `json:"data"`
	Mode    fs.FileMode `json:"mode"`
	ModTime time.Time   `json:"modTime"`
}

// SnapshotWrite is a write made to a snapshot. It is not applied anywhere: the machine
// the snapshot came from replays it (journaled there).
type SnapshotWrite struct {
	Op     string      `json:"op"` // write | append | rename
	Path   string      `json:"path"`
	From   string      `json:"from,omitempty"` // rename
	Data   []byte      `json:"data,omitempty"`
	Perm   fs.FileMode `json:"perm,omitempty"`
	Append agent.AppendOptions
}

// NewSnapshot is another machine seen only through files its hopsesh sent (a push): the
// session's files, nothing else. Nothing runs there and no process is alive; writes are
// recorded for the sender (Writes).
func NewSnapshot(name string, facts Facts, files []SnapshotFile) *Machine {
	m := &Machine{Name: name, Facts: facts, snap: &memFS{files: map[string]*SnapshotFile{}, pa: agent.PathFor(facts.OS)}}
	for i := range files {
		f := files[i]
		m.snap.files[m.snap.pa.Clean(f.Path)] = &f
	}
	m.fs = m.snap
	return m
}

// Writes returns the writes made to a snapshot machine, in order.
func (m *Machine) Writes() []SnapshotWrite {
	if m.snap == nil {
		return nil
	}
	m.snap.mu.Lock()
	defer m.snap.mu.Unlock()
	return append([]SnapshotWrite(nil), m.snap.writes...)
}

// IsSnapshot reports whether the machine is a snapshot.
func (m *Machine) IsSnapshot() bool { return m.snap != nil }

// memFS is a snapshot's filesystem. Folders are implied by the files' paths.
type memFS struct {
	mu     sync.Mutex
	files  map[string]*SnapshotFile
	writes []SnapshotWrite
	pa     agent.Path
}

type memInfo struct {
	name string
	size int64
	mode fs.FileMode
	mod  time.Time
	dir  bool
}

func (i memInfo) Name() string       { return i.name }
func (i memInfo) Size() int64        { return i.size }
func (i memInfo) Mode() fs.FileMode  { return i.mode }
func (i memInfo) ModTime() time.Time { return i.mod }
func (i memInfo) IsDir() bool        { return i.dir }
func (i memInfo) Sys() any           { return nil }

func notExist(op, p string) error { return &fs.PathError{Op: op, Path: p, Err: fs.ErrNotExist} }

func (f *memFS) info(p string, sf *SnapshotFile) memInfo {
	mode := sf.Mode
	if mode == 0 {
		mode = 0o600
	}
	return memInfo{name: f.pa.Base(p), size: int64(len(sf.Data)), mode: mode, mod: sf.ModTime}
}

// isDir reports whether some file lies under p.
func (f *memFS) isDir(p string) bool {
	prefix := strings.TrimSuffix(p, f.pa.Sep()) + f.pa.Sep()
	for k := range f.files {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

func (f *memFS) Stat(p string) (fs.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p = f.pa.Clean(p)
	if sf, ok := f.files[p]; ok {
		return f.info(p, sf), nil
	}
	if f.isDir(p) {
		return memInfo{name: f.pa.Base(p), mode: fs.ModeDir | 0o700, dir: true}, nil
	}
	return nil, notExist("stat", p)
}

func (f *memFS) ReadDir(p string) ([]fs.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p = f.pa.Clean(p)
	prefix := strings.TrimSuffix(p, f.pa.Sep()) + f.pa.Sep()
	seen := map[string]fs.FileInfo{}
	for k, sf := range f.files {
		rest, ok := strings.CutPrefix(k, prefix)
		if !ok {
			continue
		}
		if first, _, deeper := strings.Cut(rest, f.pa.Sep()); deeper {
			seen[first] = memInfo{name: first, mode: fs.ModeDir | 0o700, dir: true}
		} else {
			seen[rest] = f.info(k, sf)
		}
	}
	if len(seen) == 0 {
		return nil, notExist("readdir", p)
	}
	out := make([]fs.FileInfo, 0, len(seen))
	for _, i := range seen {
		out = append(out, i)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

type memFile struct {
	*bytes.Reader
	info memInfo
}

func (m memFile) Close() error               { return nil }
func (m memFile) Stat() (fs.FileInfo, error) { return m.info, nil }

func (f *memFS) Open(p string) (agent.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p = f.pa.Clean(p)
	sf, ok := f.files[p]
	if !ok {
		return nil, notExist("open", p)
	}
	return memFile{Reader: bytes.NewReader(sf.Data), info: f.info(p, sf)}, nil
}

func (f *memFS) RealPath(p string) (string, error) { return f.pa.Clean(p), nil }

func (f *memFS) ReadFile(p string, limit int64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p = f.pa.Clean(p)
	sf, ok := f.files[p]
	if !ok {
		return nil, notExist("read", p)
	}
	if int64(len(sf.Data)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", p, limit)
	}
	return append([]byte(nil), sf.Data...), nil
}

func (f *memFS) WriteFile(p string, b []byte, perm fs.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p = f.pa.Clean(p)
	f.writes = append(f.writes, SnapshotWrite{Op: "write", Path: p, Data: append([]byte(nil), b...), Perm: perm})
	f.files[p] = &SnapshotFile{Path: p, Data: append([]byte(nil), b...), Mode: perm, ModTime: time.Now()}
	return nil
}

func (f *memFS) Append(p string, b []byte, o agent.AppendOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p = f.pa.Clean(p)
	sf, ok := f.files[p]
	if !ok { // a new file: the sender's append creates it too
		sf = &SnapshotFile{Path: p, Mode: 0o600, ModTime: time.Now()}
		f.files[p] = sf
	}
	f.writes = append(f.writes, SnapshotWrite{Op: "append", Path: p, Data: append([]byte(nil), b...), Append: o})
	if o.NewLine && len(sf.Data) > 0 && sf.Data[len(sf.Data)-1] != '\n' {
		sf.Data = append(sf.Data, '\n')
	}
	sf.Data = append(sf.Data, b...)
	if !o.KeepMtime {
		sf.ModTime = time.Now()
	}
	return nil
}

func (f *memFS) Rename(from, to string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	from, to = f.pa.Clean(from), f.pa.Clean(to)
	sf, ok := f.files[from]
	if !ok {
		return notExist("rename", from)
	}
	f.writes = append(f.writes, SnapshotWrite{Op: "rename", Path: to, From: from})
	delete(f.files, from)
	sf.Path = to
	f.files[to] = sf
	return nil
}

// Undo of a snapshot's writes happens on the machine that replays them.
func (f *memFS) Remove(p string) error               { return errSnapshot }
func (f *memFS) Truncate(p string, size int64) error { return errSnapshot }
func (f *memFS) Chtimes(p string, t time.Time) error { return nil }
func (f *memFS) MkdirAll(p string) error             { return nil }

var errSnapshot = errors.New("a snapshot of another machine cannot be changed here")

// snapExec, snapProcs and snapLocks: on a snapshot nothing runs, no process is alive and
// no lock is held (the sender reported liveness itself).
type snapExec struct{}

func (snapExec) Run(context.Context, []string, agent.RunOptions) (agent.Result, error) {
	return agent.Result{}, fmt.Errorf("%w: nothing runs on a snapshot of another machine", agent.ErrUnsupported)
}

type snapProcs struct{}

func (snapProcs) Alive(context.Context, []int) (map[int]bool, error)   { return map[int]bool{}, nil }
func (snapProcs) Names(context.Context, []int) (map[int]string, error) { return map[int]string{}, nil }

func (snapProcs) Terminate(context.Context, int) error {
	return fmt.Errorf("%w: no process runs on a snapshot", agent.ErrUnsupported)
}

type snapLocks struct{}

func (snapLocks) Holders(context.Context, []string) (map[string][]int, error) {
	return map[string][]int{}, nil
}

func (snapLocks) Probe(_ context.Context, paths []string) (map[string]agent.LockState, error) {
	out := map[string]agent.LockState{}
	for _, p := range paths {
		out[p] = agent.LockFree
	}
	return out, nil
}
