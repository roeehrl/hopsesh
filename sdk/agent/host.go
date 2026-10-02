package agent

import (
	"context"
	"io"
	"io/fs"
	"log/slog"
	"time"
)

// Host is a module's only way to reach a machine: this one, or another over SSH. The core
// confines it to the module's Spec: writes only under its roots (and journaled for undo),
// no reads of its Secrets, and only its Binaries run.
type Host interface {
	Facts() Facts
	FS() FS
	Exec() Exec
	Path() Path
	Locks() Locks
	Procs() Procs
	Log() *slog.Logger
}

// Facts is what the core learned about a machine in one probe.
type Facts struct {
	Machine string `json:"machine"` // the name hopsesh knows it by
	Local   bool   `json:"local"`   // this machine
	OS      string `json:"os"`      // darwin | linux | windows
	Arch    string `json:"arch"`
	Home    string `json:"home"`
	// Env holds the variables the module's Spec declares (Roots[].Env, LoginEnv), as set on
	// that machine ("" when unset).
	Env map[string]string `json:"env"`
	// Binaries are the module's Spec binaries as found there.
	Binaries map[string]BinaryFact `json:"binaries"`
}

// BinaryFact is a binary found on a machine.
type BinaryFact struct {
	Path    string `json:"path"`
	Version string `json:"version,omitempty"` // first line of VersionArgs output
}

// FS is a machine's filesystem, with that machine's absolute paths.
type FS interface {
	Stat(p string) (fs.FileInfo, error)
	ReadDir(p string) ([]fs.FileInfo, error)
	Open(p string) (File, error)
	// RealPath resolves symbolic links (agents key sessions by the real working directory).
	RealPath(p string) (string, error)
	// ReadFile reads a whole file, failing if it is larger than limit bytes.
	ReadFile(p string, limit int64) ([]byte, error)
	// WriteFile replaces a file atomically, creating parent folders. Journaled.
	WriteFile(p string, b []byte, perm fs.FileMode) error
	// Append adds bytes at the end of a file. Journaled.
	Append(p string, b []byte, o AppendOptions) error
	// Rename moves a file within the module's roots. Journaled.
	Rename(from, to string) error
}

// AppendOptions control FS.Append.
type AppendOptions struct {
	NewLine   bool // start on a new line if the file does not end with one
	KeepMtime bool // restore the modification time (marks must not look like activity)
}

// File is an open file with random access.
type File interface {
	io.Reader
	io.ReaderAt
	io.Closer
	Stat() (fs.FileInfo, error)
}

// Exec runs the module's Spec binaries on the machine.
type Exec interface {
	Run(ctx context.Context, argv []string, o RunOptions) (Result, error)
}

// RunOptions control Exec.Run.
type RunOptions struct {
	Dir   string
	Env   []string // extra KEY=value pairs
	Stdin []byte
	// HoldStdin keeps standard input open this long after Stdin is written (or until the
	// program exits, or its output contains StdinUntil), for programs that stop at the end
	// of their input before answering it.
	HoldStdin  time.Duration
	StdinUntil []byte
	Timeout    time.Duration
}

// Result is a finished command. A non-zero exit is not an error.
type Result struct {
	Stdout []byte
	Stderr []byte
	Code   int
}

// Path manipulates paths in the machine's own form (a Windows machine seen from a Mac
// uses backslashes and drive letters).
type Path interface {
	Join(elem ...string) string
	Base(p string) string
	Dir(p string) string
	Clean(p string) string
	IsAbs(p string) bool
	// Rel returns target relative to base, or ok=false when it is not inside it.
	Rel(base, target string) (string, bool)
	Sep() string
}

// LockState is what a lock probe found.
type LockState string

const (
	LockHeld    LockState = "held"
	LockFree    LockState = "free"
	LockUnknown LockState = "unknown"
)

// Locks probes advisory file locks without taking them.
type Locks interface {
	Probe(ctx context.Context, paths []string) (map[string]LockState, error)
}

// Procs checks and stops processes on the machine.
type Procs interface {
	Alive(ctx context.Context, pids []int) (map[int]bool, error)
	// Terminate asks a process to exit (SIGTERM; unsupported on Windows).
	Terminate(ctx context.Context, pid int) error
}
