package agent

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"strings"
)

// Confine wraps a Host so a module can write only under its install's roots, never opens
// its Spec's Secrets (paths, or patterns with * ? [), and runs only its Spec's Binaries.
// When the Host reads databases (SQLHost), the module reads only its Spec's SQLTables, from
// the files its SQLFiles name, which are Secrets for every other use. The core gives
// every module a confined Host; the conformance kit does the same.
func Confine(h Host, s Spec, in Install) Host {
	c := &confined{h: h, spec: s}
	for _, r := range s.Roots {
		if p := in.Roots[r.Name]; p != "" {
			c.roots = append(c.roots, p)
		}
	}
	for _, sec := range s.Secrets {
		c.secrets = append(c.secrets, Expand(sec, h.Facts().Home, in.Roots, h.Path()))
	}
	for _, f := range s.SQLFiles {
		p := Expand(f, h.Facts().Home, in.Roots, h.Path())
		if strings.Contains(p, "{") || !h.Path().IsAbs(p) {
			continue // a root this install does not have
		}
		c.dbs = append(c.dbs, p)
		c.secrets = append(c.secrets, p, p+"-*")
	}
	return c
}

type confined struct {
	h       Host
	spec    Spec
	roots   []string
	secrets []string
	dbs     []string // SQLFiles, expanded
}

func (c *confined) Facts() Facts      { return c.h.Facts() }
func (c *confined) Path() Path        { return c.h.Path() }
func (c *confined) Locks() Locks      { return c.h.Locks() }
func (c *confined) Procs() Procs      { return c.h.Procs() }
func (c *confined) Log() *slog.Logger { return c.h.Log() }
func (c *confined) FS() FS            { return confinedFS{c} }
func (c *confined) Exec() Exec        { return confinedExec{c} }

// SQL is the Host's reader limited to the Spec's tables, or nil when the Host has none or
// the Spec names no tables.
func (c *confined) SQL() SQL {
	inner, ok := HostSQL(c.h)
	if !ok || len(c.spec.SQLTables) == 0 {
		return nil
	}
	if l, ok := inner.(SQLTableLimiter); ok {
		inner = l.Only(c.spec.SQLTables, c.spec.SQLDenied)
	}
	return confinedSQL{c, inner}
}

func (c *confined) under(p string, dirs []string) bool {
	pa := c.h.Path()
	for _, d := range dirs {
		if _, ok := pa.Rel(d, pa.Clean(p)); ok {
			return true
		}
	}
	return false
}

func (c *confined) readable(p string) error {
	if c.matches(p, c.secrets, true) {
		return fmt.Errorf("%w: %s holds credentials", ErrDenied, p)
	}
	return nil
}

// matches reports whether p is one of list's paths or patterns (* ? [), or, with
// inside, under one of its paths.
func (c *confined) matches(p string, list []string, inside bool) bool {
	pa := c.h.Path()
	clean := pa.Clean(p)
	for _, sec := range list {
		hit := false
		if strings.ContainsAny(sec, "*?[") {
			hit, _ = path.Match(strings.ReplaceAll(sec, `\`, "/"), strings.ReplaceAll(clean, `\`, "/"))
		} else if inside {
			hit = c.under(clean, []string{sec})
		} else {
			hit = clean == pa.Clean(sec)
		}
		if hit {
			return true
		}
	}
	return false
}

func (c *confined) writable(p string) error {
	if err := c.readable(p); err != nil {
		return err
	}
	if !c.under(p, c.roots) {
		return fmt.Errorf("%w: %s is outside %s's folders", ErrDenied, p, c.spec.Name)
	}
	return nil
}

type confinedFS struct{ c *confined }

func (f confinedFS) Stat(p string) (fs.FileInfo, error) {
	if err := f.c.readable(p); err != nil {
		return nil, err
	}
	return f.c.h.FS().Stat(p)
}

func (f confinedFS) ReadDir(p string) ([]fs.FileInfo, error) {
	if err := f.c.readable(p); err != nil {
		return nil, err
	}
	return f.c.h.FS().ReadDir(p)
}

func (f confinedFS) RealPath(p string) (string, error) {
	if err := f.c.readable(p); err != nil {
		return "", err
	}
	return f.c.h.FS().RealPath(p)
}

func (f confinedFS) Open(p string) (File, error) {
	if err := f.c.readable(p); err != nil {
		return nil, err
	}
	return f.c.h.FS().Open(p)
}

// FreeSpace passes through when the confined filesystem can tell (see SpaceReporter).
func (f confinedFS) FreeSpace(p string) (int64, error) {
	if sr, ok := f.c.h.FS().(SpaceReporter); ok {
		return sr.FreeSpace(p)
	}
	return 0, ErrUnsupported
}

func (f confinedFS) ReadFile(p string, limit int64) ([]byte, error) {
	if err := f.c.readable(p); err != nil {
		return nil, err
	}
	return f.c.h.FS().ReadFile(p, limit)
}

func (f confinedFS) WriteFile(p string, b []byte, perm fs.FileMode) error {
	if err := f.c.writable(p); err != nil {
		return err
	}
	return f.c.h.FS().WriteFile(p, b, perm)
}

func (f confinedFS) Append(p string, b []byte, o AppendOptions) error {
	if err := f.c.writable(p); err != nil {
		return err
	}
	return f.c.h.FS().Append(p, b, o)
}

func (f confinedFS) Rename(from, to string) error {
	if err := f.c.writable(from); err != nil {
		return err
	}
	if err := f.c.writable(to); err != nil {
		return err
	}
	return f.c.h.FS().Rename(from, to)
}

// confinedSQL checks every query's text and database before the Host's reader runs it.
type confinedSQL struct {
	c     *confined
	inner SQL
}

func (s confinedSQL) database(db string) error {
	if db == "" || !s.c.h.Path().IsAbs(db) {
		return fmt.Errorf("%w: %q is not a database path", ErrDenied, db)
	}
	if !s.c.matches(db, s.c.dbs, false) {
		return fmt.Errorf("%w: %s is not a database %s reads", ErrDenied, db, s.c.spec.Name)
	}
	return nil
}

func (s confinedSQL) check(db, q string) error {
	if err := s.database(db); err != nil {
		return err
	}
	return CheckSQLQuery(q, s.c.spec.SQLTables, s.c.spec.SQLDenied)
}

func (s confinedSQL) Query(ctx context.Context, db, q string, args ...any) (Rows, error) {
	if err := s.check(db, q); err != nil {
		return Rows{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, DefaultSQLTimeout)
	defer cancel()
	return s.inner.Query(ctx, db, q, args...)
}

func (s confinedSQL) Batch(ctx context.Context, db string, stmts []Statement) ([]Rows, error) {
	for _, st := range stmts {
		if err := s.check(db, st.Query); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, DefaultSQLTimeout)
	defer cancel()
	return s.inner.Batch(ctx, db, stmts)
}

func (s confinedSQL) Columns(ctx context.Context, db, table string) ([]string, error) {
	if err := s.database(db); err != nil {
		return nil, err
	}
	if !containsFold(s.c.spec.SQLTables, table) || containsFold(s.c.spec.SQLDenied, table) {
		return nil, deniedSQL("the table " + table + " is not one this agent reads")
	}
	ctx, cancel := context.WithTimeout(ctx, DefaultSQLTimeout)
	defer cancel()
	return s.inner.Columns(ctx, db, table)
}

type confinedExec struct{ c *confined }

func (e confinedExec) Run(ctx context.Context, argv []string, o RunOptions) (Result, error) {
	if len(argv) == 0 {
		return Result{}, fmt.Errorf("%w: empty command", ErrDenied)
	}
	facts := e.c.h.Facts()
	for _, b := range e.c.spec.Binaries {
		bf, found := facts.Binaries[b.Name]
		if argv[0] != b.Name && !(found && argv[0] == bf.Path) {
			continue
		}
		if !found || bf.Path == "" {
			return Result{}, fmt.Errorf("%w: %s", ErrNotInstalled, b.Name)
		}
		run := append([]string{bf.Path}, argv[1:]...)
		return e.c.h.Exec().Run(ctx, run, o)
	}
	return Result{}, fmt.Errorf("%w: %s is not one of %s's programs", ErrDenied, argv[0], e.c.spec.Name)
}

// IdentityFS reads public identity fields out of a credentials file on this machine. The
// file's bytes go only to pick, which returns the few fields it names (an account id, a
// user id, an email); nothing else leaves the call. A remote machine's credentials are
// never read: there it fails with ErrUnsupported.
type IdentityFS interface {
	ReadIdentity(p string, pick func([]byte) (map[string]string, error)) (map[string]string, error)
}

func (f confinedFS) ReadIdentity(p string, pick func([]byte) (map[string]string, error)) (map[string]string, error) {
	if !f.c.h.Facts().Local {
		return nil, ErrUnsupported
	}
	b, err := f.c.h.FS().ReadFile(p, 1<<20)
	if err != nil {
		return nil, err
	}
	defer clear(b)
	return pick(b)
}
