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
// its Spec's Secrets (paths, or patterns with * ? [), and runs only its Spec's Binaries. The core gives every module a
// confined Host; the conformance kit does the same.
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
	return c
}

type confined struct {
	h       Host
	spec    Spec
	roots   []string
	secrets []string
}

func (c *confined) Facts() Facts      { return c.h.Facts() }
func (c *confined) Path() Path        { return c.h.Path() }
func (c *confined) Locks() Locks      { return c.h.Locks() }
func (c *confined) Procs() Procs      { return c.h.Procs() }
func (c *confined) Log() *slog.Logger { return c.h.Log() }
func (c *confined) FS() FS            { return confinedFS{c} }
func (c *confined) Exec() Exec        { return confinedExec{c} }

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
	clean := c.h.Path().Clean(p)
	for _, sec := range c.secrets {
		hit := false
		if strings.ContainsAny(sec, "*?[") {
			hit, _ = path.Match(strings.ReplaceAll(sec, `\`, "/"), strings.ReplaceAll(clean, `\`, "/"))
		} else {
			hit = c.under(clean, []string{sec})
		}
		if hit {
			return fmt.Errorf("%w: %s holds credentials", ErrDenied, p)
		}
	}
	return nil
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
