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
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/transport"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// remoteFS is a machine's filesystem over SFTP. Paths are the machine's own (C:\x on
// Windows); SFTP sees them in its form.
type remoteFS struct{ r *transport.RemoteFS }

func (f remoteFS) p(p string) string { return f.r.ToSFTP(p) }

func (f remoteFS) Stat(p string) (fs.FileInfo, error)      { return f.r.Client().Stat(f.p(p)) }
func (f remoteFS) ReadDir(p string) ([]fs.FileInfo, error) { return f.r.Client().ReadDir(f.p(p)) }
func (f remoteFS) Open(p string) (agent.File, error)       { return f.r.Client().Open(f.p(p)) }

func (f remoteFS) RealPath(p string) (string, error) {
	rp, err := f.r.Client().RealPath(f.p(p))
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(rp, "/") && len(rp) > 2 && rp[2] == ':' { // /C:/x on Windows
		return strings.ReplaceAll(rp[1:], "/", `\`), nil
	}
	return rp, nil
}

func (f remoteFS) ReadFile(p string, limit int64) ([]byte, error) {
	fh, err := f.r.Client().Open(f.p(p))
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	b, err := io.ReadAll(io.LimitReader(fh, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", p, limit)
	}
	return b, nil
}

func (f remoteFS) WriteFile(p string, b []byte, perm fs.FileMode) error {
	return f.r.WriteFile(p, bytes.NewReader(b), perm)
}

func (f remoteFS) Append(p string, b []byte, o agent.AppendOptions) error {
	c := f.r.Client()
	sp := f.p(p)
	fi, err := c.Stat(sp)
	if err != nil {
		return err
	}
	fh, err := c.OpenFile(sp, os.O_RDWR)
	if err != nil {
		return err
	}
	size := fi.Size()
	if o.NewLine && size > 0 {
		last := make([]byte, 1)
		if _, err := fh.ReadAt(last, size-1); err == nil && last[0] != '\n' {
			b = append([]byte{'\n'}, b...)
		}
	}
	if _, err := fh.WriteAt(b, size); err != nil {
		fh.Close()
		return err
	}
	if err := fh.Close(); err != nil {
		return err
	}
	if o.KeepMtime {
		return c.Chtimes(sp, fi.ModTime(), fi.ModTime())
	}
	return nil
}

func (f remoteFS) Rename(from, to string) error {
	c := f.r.Client()
	_ = c.MkdirAll(path.Dir(f.p(to)))
	return c.PosixRename(f.p(from), f.p(to))
}

func (f remoteFS) Remove(p string) error               { return f.r.Client().Remove(f.p(p)) }
func (f remoteFS) Truncate(p string, size int64) error { return f.r.Client().Truncate(f.p(p), size) }
func (f remoteFS) Chtimes(p string, t time.Time) error { return f.r.Client().Chtimes(f.p(p), t, t) }
func (f remoteFS) MkdirAll(p string) error             { return f.r.Client().MkdirAll(f.p(p)) }

// remoteExec runs programs on a machine over SSH.
type remoteExec struct{ m *Machine }

func (e remoteExec) Run(ctx context.Context, argv []string, o agent.RunOptions) (agent.Result, error) {
	if o.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.Timeout)
		defer cancel()
	}
	line := e.commandLine(argv, o)
	if o.Stdin != nil {
		return e.runPiped(ctx, line, o)
	}
	out, err := e.m.Conn.Run(ctx, line)
	var re *transport.RemoteError
	if errors.As(err, &re) {
		return agent.Result{Stdout: out, Stderr: []byte(re.Stderr), Code: re.Code}, nil
	}
	if err != nil {
		return agent.Result{}, err
	}
	return agent.Result{Stdout: out}, nil
}

// commandLine is the remote command line for a program run: PowerShell on Windows, POSIX
// sh elsewhere, everything quoted.
func (e remoteExec) commandLine(argv []string, o agent.RunOptions) string {
	var b strings.Builder
	if e.m.Facts.OS == "windows" {
		if o.Dir != "" {
			b.WriteString("Set-Location " + transport.PSQuote(o.Dir) + "; ")
		}
		for _, kv := range o.Env {
			k, v, _ := strings.Cut(kv, "=")
			b.WriteString("$env:" + k + "=" + transport.PSQuote(v) + "; ")
		}
		b.WriteString("&")
		for _, a := range argv {
			b.WriteString(" " + transport.PSQuote(a))
		}
		b.WriteString("; exit $LASTEXITCODE")
		return transport.PowerShellCommand(b.String())
	}
	if o.Dir != "" {
		b.WriteString("cd " + transport.ShQuote(o.Dir) + " && ")
	}
	if len(o.Env) > 0 {
		b.WriteString("env")
		for _, kv := range o.Env {
			b.WriteString(" " + transport.ShQuote(kv))
		}
		b.WriteString(" ")
	}
	for i, a := range argv {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(transport.ShQuote(a))
	}
	return b.String()
}

// runPiped runs a command with input: it writes o.Stdin, keeps the input open until the
// output contains o.StdinUntil or o.HoldStdin passes, then waits for the program.
func (e remoteExec) runPiped(ctx context.Context, line string, o agent.RunOptions) (agent.Result, error) {
	p, err := e.m.Conn.StartPipe(ctx, line)
	if err != nil {
		return agent.Result{}, err
	}
	var mu sync.Mutex
	var out bytes.Buffer
	answered := make(chan struct{})
	read := make(chan struct{})
	go func() {
		defer close(read)
		buf := make([]byte, 32<<10)
		hit := false
		for {
			n, rerr := p.Out.Read(buf)
			mu.Lock()
			out.Write(buf[:n])
			if !hit && len(o.StdinUntil) > 0 && bytes.Contains(out.Bytes(), o.StdinUntil) {
				hit = true
				close(answered)
			}
			mu.Unlock()
			if rerr != nil {
				return
			}
		}
	}()
	if _, err := p.In.Write(o.Stdin); err != nil {
		_ = p.Close()
		return agent.Result{}, err
	}
	if o.HoldStdin > 0 {
		select {
		case <-time.After(o.HoldStdin):
		case <-answered:
		case <-read:
		case <-ctx.Done():
		}
	}
	werr := p.Close() // closes the input, waits for the program
	<-read
	mu.Lock()
	res := agent.Result{Stdout: out.Bytes(), Stderr: []byte(p.Stderr())}
	mu.Unlock()
	var ee *exec.ExitError
	switch {
	case werr == nil:
	case errors.As(werr, &ee) && ee.ExitCode() != 255:
		res.Code = ee.ExitCode()
	case ctx.Err() != nil:
		return res, ctx.Err()
	default:
		return res, fmt.Errorf("%s: %w", strings.TrimSpace(p.Stderr()), werr)
	}
	return res, nil
}

// remoteProcs checks and stops processes on a machine over SSH.
type remoteProcs struct{ m *Machine }

func (p remoteProcs) Alive(ctx context.Context, pids []int) (map[int]bool, error) {
	out := map[int]bool{}
	if len(pids) == 0 {
		return out, nil
	}
	ids := make([]string, len(pids))
	for i, id := range pids {
		ids[i] = strconv.Itoa(id)
	}
	var res []byte
	var err error
	if p.m.Facts.OS == "windows" {
		res, err = p.m.Conn.RunPowerShell(ctx, "Get-Process -Id "+strings.Join(ids, ",")+" -ErrorAction SilentlyContinue | ForEach-Object { $_.Id }")
	} else {
		res, err = p.m.Conn.RunSh(ctx, `for p in "$@"; do kill -0 "$p" 2>/dev/null && echo "$p"; done; exit 0`, ids...)
	}
	if err != nil {
		return nil, err
	}
	for _, f := range strings.Fields(string(res)) {
		if n, err := strconv.Atoi(f); err == nil {
			out[n] = true
		}
	}
	return out, nil
}

func (p remoteProcs) Terminate(ctx context.Context, pid int) error {
	if p.m.Facts.OS == "windows" {
		return terminate(pid)
	}
	_, err := p.m.Conn.RunSh(ctx, `kill -TERM "$1"`, strconv.Itoa(pid))
	return err
}

// lockScript probes advisory locks with perl's flock (the same flock(2) agents use), which
// is on every macOS and nearly every Linux machine.
const lockScript = `command -v perl >/dev/null 2>&1 || { for p in "$@"; do printf 'unknown\t%s\n' "$p"; done; exit 0; }
exec perl -e 'use Fcntl qw(:flock); for my $p (@ARGV) { if (!-e $p) { print "free\t$p\n"; next } if (open(my $f, "<", $p)) { if (flock($f, LOCK_SH|LOCK_NB)) { flock($f, LOCK_UN); print "free\t$p\n" } else { print "held\t$p\n" } close $f } else { print "unknown\t$p\n" } }' "$@"`

// remoteLocks probes lock files on a machine over SSH.
type remoteLocks struct{ m *Machine }

func (l remoteLocks) Probe(ctx context.Context, paths []string) (map[string]agent.LockState, error) {
	out := map[string]agent.LockState{}
	if len(paths) == 0 {
		return out, nil
	}
	if l.m.Facts.OS == "windows" {
		for _, p := range paths {
			out[p] = agent.LockUnknown
		}
		return out, nil
	}
	res, err := l.m.Conn.RunSh(ctx, lockScript, paths...)
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(res), "\n") {
		st, p, ok := strings.Cut(line, "\t")
		if ok {
			out[p] = agent.LockState(st)
		}
	}
	return out, nil
}
