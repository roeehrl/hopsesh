// Package host gives the core and its agent modules access to a machine: this one, or
// another over SSH. A Machine is probed once (one round trip, generated from every
// module's Spec); For returns the agent.Host a module sees, confined to its Spec and with
// every write journaled.
package host

import (
	"context"
	"io/fs"
	"log/slog"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/internal/core/transport"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// FS is the core's filesystem: the module FS plus what the journal and the installer
// need.
type FS interface {
	agent.FS
	Remove(p string) error // a file or an empty folder
	Truncate(p string, size int64) error
	Chtimes(p string, t time.Time) error
	MkdirAll(p string) error
}

// Facts is what one probe learned about a machine.
type Facts struct {
	OS       string                      `json:"os"`
	Arch     string                      `json:"arch"`
	Home     string                      `json:"home"`
	Env      map[string]string           `json:"env"`
	Binaries map[string]agent.BinaryFact `json:"binaries"`
	HasGit   bool                        `json:"hasGit"`
}

// Machine is a place sessions live: this machine, one reached over SSH, or a snapshot
// another machine's hopsesh sent.
type Machine struct {
	Name  string
	Local bool
	Conn  *transport.Conn // nil for this machine
	Facts Facts
	Log   *slog.Logger

	mu   sync.Mutex
	fs   FS
	rfs  *transport.RemoteFS
	snap *memFS // a snapshot another machine's hopsesh sent (NewSnapshot)
}

// FS returns the machine's filesystem (opening SFTP on first use for a remote machine).
func (m *Machine) FS(ctx context.Context) (FS, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fs != nil {
		return m.fs, nil
	}
	if m.Local {
		m.fs = localFS{}
		return m.fs, nil
	}
	r, err := m.Conn.OpenSFTP(ctx, m.Facts.OS == "windows")
	if err != nil {
		return nil, err
	}
	m.rfs = r
	m.fs = remoteFS{r}
	return m.fs, nil
}

// Path is the machine's path form.
func (m *Machine) Path() agent.Path { return agent.PathFor(m.Facts.OS) }

// Exec runs programs on the machine.
func (m *Machine) Exec() agent.Exec {
	if m.snap != nil {
		return snapExec{}
	}
	if m.Local {
		return localExec{}
	}
	return remoteExec{m}
}

// Procs checks and stops processes on the machine.
func (m *Machine) Procs() agent.Procs {
	if m.snap != nil {
		return snapProcs{}
	}
	if m.Local {
		return localProcs{}
	}
	return remoteProcs{m}
}

// Locks probes lock files on the machine.
func (m *Machine) Locks() agent.Locks {
	if m.snap != nil {
		return snapLocks{}
	}
	if m.Local {
		return localLocks{}
	}
	return remoteLocks{m}
}

// Close ends the machine's SFTP session and shared SSH connection.
func (m *Machine) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rfs != nil {
		_ = m.rfs.Close()
		m.rfs, m.fs = nil, nil
	}
	if m.Conn != nil {
		m.Conn.Close()
	}
}

// For returns the Host a module sees on this machine: facts limited to what its Spec
// declares, writes through w (the journal, or nil for read-only work), confined to the
// install's roots and away from its secrets.
func (m *Machine) For(ctx context.Context, s agent.Spec, in agent.Install, w Writes) (agent.Host, error) {
	fsys, err := m.FS(ctx)
	if err != nil {
		return nil, err
	}
	h := &moduleHost{m: m, fs: fsys, facts: m.specFacts(s), w: w}
	if in.Agent == "" { // before Detect: nothing may be written
		return h, nil
	}
	return agent.Confine(h, s, in), nil
}

// Writes is where module writes go: the journal records how to undo each before it
// happens.
type Writes interface {
	WriteFile(fsys FS, machine, p string, b []byte, perm fs.FileMode) error
	Append(fsys FS, machine, p string, b []byte, o agent.AppendOptions) error
	Rename(fsys FS, machine, from, to string) error
}

func (m *Machine) specFacts(s agent.Spec) agent.Facts {
	f := agent.Facts{Machine: m.Name, Local: m.Local, OS: m.Facts.OS, Arch: m.Facts.Arch, Home: m.Facts.Home,
		Env: map[string]string{}, Binaries: map[string]agent.BinaryFact{}}
	for _, k := range SpecEnv(s) {
		f.Env[k] = m.Facts.Env[k]
	}
	for _, b := range s.Binaries {
		if bf, ok := m.Facts.Binaries[b.Name]; ok {
			f.Binaries[b.Name] = bf
		}
	}
	return f
}

// SpecEnv lists the variables a Spec reads (roots and login environment).
func SpecEnv(s agent.Spec) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range s.Roots {
		for _, e := range r.Env {
			if !seen[e] {
				seen[e] = true
				out = append(out, e)
			}
		}
	}
	for _, e := range s.LoginEnv {
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	return out
}

type moduleHost struct {
	m     *Machine
	fs    FS
	facts agent.Facts
	w     Writes
}

func (h *moduleHost) Facts() agent.Facts { return h.facts }
func (h *moduleHost) FS() agent.FS       { return moduleFS{h} }
func (h *moduleHost) Exec() agent.Exec   { return h.m.Exec() }
func (h *moduleHost) Path() agent.Path   { return h.m.Path() }
func (h *moduleHost) Locks() agent.Locks { return h.m.Locks() }
func (h *moduleHost) Procs() agent.Procs { return h.m.Procs() }
func (h *moduleHost) Log() *slog.Logger {
	if h.m.Log != nil {
		return h.m.Log
	}
	return slog.Default()
}

type moduleFS struct{ h *moduleHost }

func (f moduleFS) Stat(p string) (fs.FileInfo, error)             { return f.h.fs.Stat(p) }
func (f moduleFS) ReadDir(p string) ([]fs.FileInfo, error)        { return f.h.fs.ReadDir(p) }
func (f moduleFS) Open(p string) (agent.File, error)              { return f.h.fs.Open(p) }
func (f moduleFS) RealPath(p string) (string, error)              { return f.h.fs.RealPath(p) }
func (f moduleFS) ReadFile(p string, limit int64) ([]byte, error) { return f.h.fs.ReadFile(p, limit) }

func (f moduleFS) WriteFile(p string, b []byte, perm fs.FileMode) error {
	if f.h.w == nil {
		return errReadOnly
	}
	return f.h.w.WriteFile(f.h.fs, f.h.m.Name, p, b, perm)
}

func (f moduleFS) Append(p string, b []byte, o agent.AppendOptions) error {
	if f.h.w == nil {
		return errReadOnly
	}
	return f.h.w.Append(f.h.fs, f.h.m.Name, p, b, o)
}

func (f moduleFS) Rename(from, to string) error {
	if f.h.w == nil {
		return errReadOnly
	}
	return f.h.w.Rename(f.h.fs, f.h.m.Name, from, to)
}

// GitProbe returns the git state of folders on the machine. excl are agent-managed
// worktree folders.
func (m *Machine) GitProbe(ctx context.Context, dirs, excl []string) ([]repos.GitState, error) {
	if len(dirs) == 0 || !m.Facts.HasGit {
		return nil, nil
	}
	if m.Local {
		return repos.ProbeLocal(ctx, dirs, excl)
	}
	if m.Facts.OS == "windows" {
		out, err := m.Conn.RunPowerShell(ctx, repos.PowerShellProbe(dirs, excl))
		if err != nil {
			return nil, err
		}
		return repos.ParseProbe(out, excl), nil
	}
	script, args := repos.ProbeScript(dirs, excl)
	out, err := m.Conn.RunSh(ctx, script, args[1:]...)
	if err != nil {
		return nil, err
	}
	return repos.ParseProbe(out, excl), nil
}
