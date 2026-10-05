package termapp

import (
	"context"
	"errors"
	"os/exec"
	"runtime"

	"github.com/roeehrl/hopsesh/internal/core/launch"
	"github.com/roeehrl/hopsesh/internal/core/proc"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Terminals that take a shell line: Windows Terminal (or a PowerShell console) on
// Windows, the first terminal emulator found on Linux, and a test's stand-in. The line is
// built here from the launch (its folder, hopsesh's path and plain words), so it never
// carries a session's title or a prompt either. These keep the window open after the
// command, so no --hold.

// Line is the launch as a line for this machine's shell: change to its folder, then run
// hopsesh's verb.
func Line(l Launch) string {
	return launch.Shell(agent.Command{Argv: append([]string{l.Program}, l.Args...), Dir: l.Dir}, "", launch.DefaultShell())
}

type lineTerminal struct {
	id, name  string
	open      func(line string) error
	installed func() bool
}

// LineTerminal is a terminal that opens a launch by running Line(l) with open (tests, and
// the browser tests' stand-in that runs the line in the background).
func LineTerminal(id, name string, open func(line string) error) Terminal {
	return &lineTerminal{id: id, name: name, open: open, installed: func() bool { return true }}
}

func (t *lineTerminal) ID() string   { return t.id }
func (t *lineTerminal) Name() string { return t.name }
func (t *lineTerminal) caps() Caps   { return Caps{} }

func (t *lineTerminal) Available(context.Context) error {
	if t.installed() {
		return nil
	}
	return ErrNotInstalled
}

func (t *lineTerminal) Open(_ context.Context, l Launch) (Handle, error) {
	if err := l.check(); err != nil {
		return Handle{}, err
	}
	return Handle{Terminal: t.id}, t.open(Line(l))
}

// WindowsTerminal is Windows Terminal, or a PowerShell console when it is not installed.
func WindowsTerminal() Terminal {
	return &lineTerminal{id: IDWindowsTerminal, name: "Windows Terminal", open: openWindowsTerminal,
		installed: func() bool { return runtime.GOOS == "windows" }}
}

// linuxTerminals are tried in order: the Debian alternative, then common emulators.
var linuxTerminals = [][]string{{"x-terminal-emulator", "-e"}, {"gnome-terminal", "--"}, {"konsole", "-e"}, {"xterm", "-e"}}

// Linux is the first terminal emulator found on a Linux machine.
func Linux() Terminal {
	return &lineTerminal{id: IDLinux, name: "a terminal", open: openLinuxTerminal, installed: func() bool {
		if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
			return false
		}
		for _, t := range linuxTerminals {
			if _, err := exec.LookPath(t[0]); err == nil {
				return true
			}
		}
		return false
	}}
}

func openLinuxTerminal(line string) error {
	for _, t := range linuxTerminals {
		if _, err := exec.LookPath(t[0]); err == nil {
			return proc.Command(t[0], append(t[1:], "sh", "-c", line+"; exec \"${SHELL:-sh}\"")...).Start()
		}
	}
	return errors.New("no terminal emulator found")
}
