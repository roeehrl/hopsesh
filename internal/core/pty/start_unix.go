//go:build !windows

package pty

import (
	"errors"
	"os/exec"
	"sync"
	"syscall"

	"github.com/charmbracelet/x/xpty"
)

// unixTerm is a program in a Unix pseudo-terminal: a session leader with the terminal as
// its controlling terminal, so Ctrl-C reaches its foreground job as SIGINT and the
// terminal's size changes as SIGWINCH.
type unixTerm struct {
	pty       *xpty.UnixPty
	cmd       *exec.Cmd
	closeOnce sync.Once
}

func start(argv []string, dir string, env []string, cols, rows int, _ string) (backend, error) {
	p, err := xpty.NewUnixPty(cols, rows)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // the tab's program, by its argument list
	cmd.Dir, cmd.Env = dir, env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := p.Start(cmd); err != nil {
		_ = p.Close()
		return nil, err
	}
	// This process's copy of the program's end: closed, so reading ends with the program.
	_ = p.Slave().Close()
	return &unixTerm{pty: p, cmd: cmd}, nil
}

func (u *unixTerm) Read(b []byte) (int, error)  { return u.pty.Master().Read(b) }
func (u *unixTerm) Write(b []byte) (int, error) { return u.pty.Master().Write(b) }
func (u *unixTerm) Resize(cols, rows int) error { return u.pty.Resize(cols, rows) }
func (u *unixTerm) ClosesOnExit() bool          { return true }
func (u *unixTerm) Name() string                { return "pty" }

func (u *unixTerm) Wait() int {
	err := u.cmd.Wait()
	ps := u.cmd.ProcessState
	if ps == nil {
		return -1
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		return -1
	}
	return ps.ExitCode()
}

// signal sends sig to the program's process group (it leads its own session).
func (u *unixTerm) signal(sig syscall.Signal) {
	if u.cmd.Process != nil {
		_ = syscall.Kill(-u.cmd.Process.Pid, sig)
	}
}

func (u *unixTerm) Hangup() { u.signal(syscall.SIGHUP) }
func (u *unixTerm) Kill()   { u.signal(syscall.SIGKILL) }

func (u *unixTerm) CloseTerminal() {
	u.closeOnce.Do(func() { _ = u.pty.Master().Close() })
}

// BundledConpty is Windows only: elsewhere a tab always uses the system's pseudo-terminal.
func BundledConpty(string) (dll, host string, ok bool) { return "", "", false }
