// Package agenttest is the conformance kit for agent modules: an in-memory Host and a
// suite every module must pass.
package agenttest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// FakeHost is an in-memory POSIX machine.
type FakeHost struct {
	mu    sync.Mutex
	files map[string]*memFile
	facts agent.Facts
	// Programs answer Exec.Run by binary name (argv[0] after resolution is the fake path
	// "/bin/<name>").
	Programs map[string]func(argv []string, o agent.RunOptions) agent.Result
	// LocksHeld and PIDs simulate lock files and processes; LockHolders and PIDNames say
	// which process holds a lock and what it is (a terminated process releases its locks).
	LocksHeld   map[string]bool
	PIDs        map[int]bool
	LockHolders map[string][]int
	PIDNames    map[int]string
	// Writes records every write, in order.
	Writes []string
}

type memFile struct {
	data  []byte
	dir   bool
	mode  fs.FileMode
	mtime time.Time
}

// NewFakeHost returns an empty machine with the given home folder.
func NewFakeHost(home string) *FakeHost {
	h := &FakeHost{
		files:       map[string]*memFile{},
		facts:       agent.Facts{Machine: "fake", Local: true, OS: "linux", Arch: "amd64", Home: home, Env: map[string]string{}, Binaries: map[string]agent.BinaryFact{}},
		Programs:    map[string]func([]string, agent.RunOptions) agent.Result{},
		LocksHeld:   map[string]bool{},
		PIDs:        map[int]bool{},
		LockHolders: map[string][]int{},
		PIDNames:    map[int]string{},
	}
	h.mkdirAll(home)
	return h
}

// SetEnv sets a variable the module's Spec declares.
func (h *FakeHost) SetEnv(k, v string) { h.facts.Env[k] = v }

// AddBinary makes a binary present with a version line.
func (h *FakeHost) AddBinary(name, versionLine string) {
	h.facts.Binaries[name] = agent.BinaryFact{Path: "/bin/" + name, Version: versionLine}
}

// Put creates a file (and its folders) with a modification time.
func (h *FakeHost) Put(p string, data []byte, mtime time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.mkdirAll(path.Dir(p))
	h.files[path.Clean(p)] = &memFile{data: append([]byte(nil), data...), mode: 0o600, mtime: mtime}
}

// Get returns a file's content.
func (h *FakeHost) Get(p string) ([]byte, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f, ok := h.files[path.Clean(p)]
	if !ok || f.dir {
		return nil, false
	}
	return append([]byte(nil), f.data...), true
}

// Load copies a folder from disk (test data) to dest, keeping relative paths.
func (h *FakeHost) Load(dir, dest string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		fi, _ := d.Info()
		h.Put(path.Join(dest, filepath.ToSlash(rel)), b, fi.ModTime())
		return nil
	})
}

// Paths lists every file, sorted.
func (h *FakeHost) Paths() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for p, f := range h.files {
		if !f.dir {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func (h *FakeHost) mkdirAll(p string) {
	for p = path.Clean(p); p != "/" && p != "."; p = path.Dir(p) {
		if _, ok := h.files[p]; !ok {
			h.files[p] = &memFile{dir: true, mode: fs.ModeDir | 0o700}
		}
	}
}

func (h *FakeHost) Facts() agent.Facts { return h.facts }
func (h *FakeHost) FS() agent.FS       { return memFS{h} }
func (h *FakeHost) Exec() agent.Exec   { return memExec{h} }
func (h *FakeHost) Path() agent.Path   { return agent.PosixPath{} }
func (h *FakeHost) Locks() agent.Locks { return memLocks{h} }
func (h *FakeHost) Procs() agent.Procs { return memProcs{h} }
func (h *FakeHost) Log() *slog.Logger  { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type memInfo struct {
	name string
	f    *memFile
}

func (i memInfo) Name() string       { return i.name }
func (i memInfo) Size() int64        { return int64(len(i.f.data)) }
func (i memInfo) Mode() fs.FileMode  { return i.f.mode }
func (i memInfo) ModTime() time.Time { return i.f.mtime }
func (i memInfo) IsDir() bool        { return i.f.dir }
func (i memInfo) Sys() any           { return nil }

type memFS struct{ h *FakeHost }

func (m memFS) Stat(p string) (fs.FileInfo, error) {
	m.h.mu.Lock()
	defer m.h.mu.Unlock()
	f, ok := m.h.files[path.Clean(p)]
	if !ok {
		return nil, &fs.PathError{Op: "stat", Path: p, Err: fs.ErrNotExist}
	}
	return memInfo{path.Base(p), f}, nil
}

func (m memFS) ReadDir(p string) ([]fs.FileInfo, error) {
	m.h.mu.Lock()
	defer m.h.mu.Unlock()
	p = path.Clean(p)
	if f, ok := m.h.files[p]; !ok || !f.dir {
		return nil, &fs.PathError{Op: "readdir", Path: p, Err: fs.ErrNotExist}
	}
	var out []fs.FileInfo
	for q, f := range m.h.files {
		if path.Dir(q) == p && q != p {
			out = append(out, memInfo{path.Base(q), f})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

func (m memFS) RealPath(p string) (string, error) { return path.Clean(p), nil }

type memOpen struct {
	*bytes.Reader
	info memInfo
}

func (o memOpen) Close() error               { return nil }
func (o memOpen) Stat() (fs.FileInfo, error) { return o.info, nil }

func (m memFS) Open(p string) (agent.File, error) {
	m.h.mu.Lock()
	defer m.h.mu.Unlock()
	f, ok := m.h.files[path.Clean(p)]
	if !ok || f.dir {
		return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrNotExist}
	}
	return memOpen{bytes.NewReader(append([]byte(nil), f.data...)), memInfo{path.Base(p), f}}, nil
}

func (m memFS) ReadFile(p string, limit int64) ([]byte, error) {
	b, ok := m.h.Get(p)
	if !ok {
		return nil, &fs.PathError{Op: "read", Path: p, Err: fs.ErrNotExist}
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", p, limit)
	}
	return b, nil
}

func (m memFS) WriteFile(p string, b []byte, perm fs.FileMode) error {
	m.h.mu.Lock()
	defer m.h.mu.Unlock()
	m.h.mkdirAll(path.Dir(p))
	m.h.files[path.Clean(p)] = &memFile{data: append([]byte(nil), b...), mode: perm, mtime: time.Now()}
	m.h.Writes = append(m.h.Writes, "write "+p)
	return nil
}

func (m memFS) Append(p string, b []byte, o agent.AppendOptions) error {
	m.h.mu.Lock()
	defer m.h.mu.Unlock()
	f, ok := m.h.files[path.Clean(p)]
	if ok && f.dir {
		return &fs.PathError{Op: "append", Path: p, Err: fs.ErrInvalid}
	}
	if !ok {
		m.h.mkdirAll(path.Dir(p))
		f = &memFile{mode: 0o600, mtime: time.Now()}
		m.h.files[path.Clean(p)] = f
	}
	if o.NewLine && len(f.data) > 0 && f.data[len(f.data)-1] != '\n' {
		f.data = append(f.data, '\n')
	}
	f.data = append(f.data, b...)
	if !o.KeepMtime {
		f.mtime = time.Now()
	}
	m.h.Writes = append(m.h.Writes, "append "+p)
	return nil
}

func (m memFS) Rename(from, to string) error {
	m.h.mu.Lock()
	defer m.h.mu.Unlock()
	f, ok := m.h.files[path.Clean(from)]
	if !ok {
		return &fs.PathError{Op: "rename", Path: from, Err: fs.ErrNotExist}
	}
	m.h.mkdirAll(path.Dir(to))
	delete(m.h.files, path.Clean(from))
	m.h.files[path.Clean(to)] = f
	m.h.Writes = append(m.h.Writes, "rename "+from+" "+to)
	return nil
}

type memExec struct{ h *FakeHost }

func (e memExec) Run(_ context.Context, argv []string, o agent.RunOptions) (agent.Result, error) {
	name := strings.TrimPrefix(argv[0], "/bin/")
	prog, ok := e.h.Programs[name]
	if !ok {
		return agent.Result{}, errors.New("agenttest: no fake program " + name)
	}
	return prog(argv, o), nil
}

type memLocks struct{ h *FakeHost }

func (l memLocks) Probe(_ context.Context, paths []string) (map[string]agent.LockState, error) {
	out := map[string]agent.LockState{}
	for _, p := range paths {
		switch {
		case l.h.LocksHeld[p]:
			out[p] = agent.LockHeld
		default:
			out[p] = agent.LockFree
		}
	}
	return out, nil
}

func (l memLocks) Holders(_ context.Context, paths []string) (map[string][]int, error) {
	out := map[string][]int{}
	for _, p := range paths {
		if l.h.LocksHeld[p] {
			out[p] = append([]int(nil), l.h.LockHolders[p]...)
		}
	}
	return out, nil
}

type memProcs struct{ h *FakeHost }

func (p memProcs) Names(_ context.Context, pids []int) (map[int]string, error) {
	out := map[int]string{}
	for _, id := range pids {
		if p.h.PIDs[id] {
			out[id] = p.h.PIDNames[id]
		}
	}
	return out, nil
}

func (p memProcs) Alive(_ context.Context, pids []int) (map[int]bool, error) {
	out := map[int]bool{}
	for _, id := range pids {
		out[id] = p.h.PIDs[id]
	}
	return out, nil
}

func (p memProcs) Terminate(_ context.Context, pid int) error {
	delete(p.h.PIDs, pid)
	for path, holders := range p.h.LockHolders {
		for _, h := range holders {
			if h == pid {
				delete(p.h.LocksHeld, path)
				delete(p.h.LockHolders, path)
			}
		}
	}
	return nil
}
