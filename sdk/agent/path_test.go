package agent

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"testing"
)

func TestPaths(t *testing.T) {
	p := PosixPath{}
	if r, ok := p.Rel("/home/me/.claude", "/home/me/.claude/projects/x.jsonl"); !ok || r != "projects/x.jsonl" {
		t.Fatalf("posix rel = %q %v", r, ok)
	}
	if _, ok := p.Rel("/home/me/.claude", "/home/me/.claudex/y"); ok {
		t.Fatal("a sibling with the same prefix is not inside")
	}
	if _, ok := p.Rel("/home/me/.claude", "/home/me/.claude/../.ssh/id"); ok {
		t.Fatal(".. must not escape")
	}
	w := WindowsPath{}
	if got := w.Join(`c:\Users\me`, ".claude", "projects"); got != `C:\Users\me\.claude\projects` {
		t.Fatalf("windows join = %q", got)
	}
	if r, ok := w.Rel(`C:\Users\Me`, `c:/users/me/.codex/sessions`); !ok || r != `.codex\sessions` {
		t.Fatalf("windows rel = %q %v", r, ok)
	}
	if !w.IsAbs(`D:\x`) || w.IsAbs(`x\y`) {
		t.Fatal("windows IsAbs")
	}
}

func TestConfine(t *testing.T) {
	h := newStub("/home/me")
	spec := Spec{ID: "x", Name: "X", Roots: []Root{{Name: "home", Default: map[string]string{"*": "~/.x"}}},
		Secrets: []string{"{home}/auth.json", "{home}/sessions/*.key"}, Binaries: []Binary{{Name: "x"}}}
	in := Install{Roots: map[string]string{"home": "/home/me/.x"}}
	c := Confine(h, spec, in)
	for _, p := range []string{"/home/me/.x/auth.json", "/home/me/.x/sessions/12.ab.key"} {
		if _, err := c.FS().ReadFile(p, 10); !errors.Is(err, ErrDenied) {
			t.Errorf("reading %s: %v, want denied", p, err)
		}
	}
	if _, err := c.FS().ReadFile("/home/me/.x/sessions/12.json", 10); errors.Is(err, ErrDenied) {
		t.Error("an ordinary file in the same folder must be readable")
	}
	if err := c.FS().WriteFile("/home/me/.bashrc", nil, 0o600); !errors.Is(err, ErrDenied) {
		t.Error("a write outside the roots must be denied")
	}
	if err := c.FS().WriteFile("/home/me/.x/projects/a.jsonl", nil, 0o600); err != nil {
		t.Errorf("a write inside the roots: %v", err)
	}
	if _, err := c.Exec().Run(context.Background(), []string{"curl", "x"}, RunOptions{}); !errors.Is(err, ErrDenied) {
		t.Error("an undeclared binary must be denied")
	}
}

// stub is a Host whose FS accepts everything (Confine is what is tested).
type stub struct{ facts Facts }

func newStub(home string) *stub {
	return &stub{Facts{OS: "linux", Home: home, Binaries: map[string]BinaryFact{}}}
}

func (s *stub) Facts() Facts      { return s.facts }
func (s *stub) FS() FS            { return stubFS{} }
func (s *stub) Exec() Exec        { return nil }
func (s *stub) Path() Path        { return PosixPath{} }
func (s *stub) Locks() Locks      { return nil }
func (s *stub) Procs() Procs      { return nil }
func (s *stub) Log() *slog.Logger { return slog.Default() }

type stubFS struct{}

func (stubFS) Stat(string) (fs.FileInfo, error)            { return nil, fs.ErrNotExist }
func (stubFS) ReadDir(string) ([]fs.FileInfo, error)       { return nil, nil }
func (stubFS) Open(string) (File, error)                   { return nil, fs.ErrNotExist }
func (stubFS) RealPath(p string) (string, error)           { return p, nil }
func (stubFS) ReadFile(string, int64) ([]byte, error)      { return nil, nil }
func (stubFS) WriteFile(string, []byte, fs.FileMode) error { return nil }
func (stubFS) Append(string, []byte, AppendOptions) error  { return nil }
func (stubFS) Rename(string, string) error                 { return nil }
