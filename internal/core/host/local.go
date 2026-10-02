package host

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

var errReadOnly = fmt.Errorf("%w: this step does not write", agent.ErrDenied)

// localFS is this machine's filesystem.
type localFS struct{}

func (localFS) Stat(p string) (fs.FileInfo, error) { return os.Stat(p) }

func (localFS) ReadDir(p string) ([]fs.FileInfo, error) {
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	out := make([]fs.FileInfo, 0, len(entries))
	for _, e := range entries {
		if fi, err := e.Info(); err == nil {
			out = append(out, fi)
		}
	}
	return out, nil
}

func (localFS) Open(p string) (agent.File, error) { return os.Open(p) }

func (localFS) RealPath(p string) (string, error) { return filepath.EvalSymlinks(p) }

func (localFS) ReadFile(p string, limit int64) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", p, limit)
	}
	return b, nil
}

func (localFS) WriteFile(p string, b []byte, perm fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), "."+filepath.Base(p)+".part-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), p)
}

func (localFS) Append(p string, b []byte, o agent.AppendOptions) error {
	fi, err := os.Stat(p)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	size := fi.Size()
	if o.NewLine && size > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, size-1); err == nil && last[0] != '\n' {
			b = append([]byte{'\n'}, b...)
		}
	}
	if _, err := f.WriteAt(b, size); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if o.KeepMtime {
		return os.Chtimes(p, fi.ModTime(), fi.ModTime())
	}
	return nil
}

func (localFS) Rename(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	return os.Rename(from, to)
}

func (localFS) Remove(p string) error               { return os.Remove(p) }
func (localFS) Truncate(p string, size int64) error { return os.Truncate(p, size) }
func (localFS) Chtimes(p string, t time.Time) error { return os.Chtimes(p, t, t) }
func (localFS) MkdirAll(p string) error             { return os.MkdirAll(p, 0o700) }

// localExec runs programs on this machine.
type localExec struct{}

func (localExec) Run(ctx context.Context, argv []string, o agent.RunOptions) (agent.Result, error) {
	if o.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = o.Dir
	if len(o.Env) > 0 {
		cmd.Env = append(os.Environ(), o.Env...)
	}
	var hold io.WriteCloser
	if o.Stdin != nil && o.HoldStdin > 0 {
		w, err := cmd.StdinPipe()
		if err != nil {
			return agent.Result{}, err
		}
		hold = w
	} else if o.Stdin != nil {
		cmd.Stdin = bytes.NewReader(o.Stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Start(); err != nil {
		return agent.Result{}, err
	}
	if hold != nil {
		exited := make(chan struct{})
		defer close(exited)
		go func() {
			_, _ = hold.Write(o.Stdin)
			select {
			case <-time.After(o.HoldStdin):
			case <-exited:
			case <-ctx.Done():
			}
			hold.Close()
		}()
	}
	err := cmd.Wait()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return agent.Result{Stdout: out.Bytes(), Stderr: errb.Bytes(), Code: ee.ExitCode()}, nil
	}
	if err != nil {
		return agent.Result{}, err
	}
	return agent.Result{Stdout: out.Bytes(), Stderr: errb.Bytes()}, nil
}

// localProcs checks and stops processes on this machine.
type localProcs struct{}

func (localProcs) Alive(_ context.Context, pids []int) (map[int]bool, error) {
	out := make(map[int]bool, len(pids))
	for _, pid := range pids {
		out[pid] = processExists(pid)
	}
	return out, nil
}

func (localProcs) Terminate(_ context.Context, pid int) error { return terminate(pid) }

// localLocks probes advisory locks on this machine.
type localLocks struct{}

func (localLocks) Probe(_ context.Context, paths []string) (map[string]agent.LockState, error) {
	out := make(map[string]agent.LockState, len(paths))
	for _, p := range paths {
		out[p] = probeLock(p)
	}
	return out, nil
}

// LocalFS is this machine's filesystem.
func LocalFS() FS { return localFS{} }
