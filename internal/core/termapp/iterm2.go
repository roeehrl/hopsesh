package termapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// iTerm2 through AppleScript: a launch opens as a new tab in the front window (a new
// window when there is none), running hopsesh's command in place of the profile's shell
// (`create tab with default profile command`), so nothing is typed into a shell. Such a
// tab closes when its command ends, so the command carries --hold and hopsesh's verb keeps
// the tab open with the exit code shown. A running session's tab is found by its tty and
// brought forward with select. hopsesh never changes iTerm2's settings and never reads a
// session's contents. When the user has turned on iTerm2's Python API, the richer path in
// iterm2_api.go is tried first for steps opened Beside, FindTTY, Focus and Exited, and this
// AppleScript path is the fallback for every one of its errors.

const iterm2Bundle = "com.googlecode.iterm2"

type iterm2 struct {
	run       Script
	installed func() bool
	api       *apiPath   // nil: AppleScript only
	exits     ExitSource // for Exited
}

// ITerm2 is iTerm2 on this Mac, with its Python API as the richer path when the user has
// turned it on.
func ITerm2() Terminal {
	return &iterm2{run: Osascript, installed: bundle("iTerm.app"), api: sharedAPI}
}

// ITerm2Using is iTerm2 driven by run, installed as installed says, AppleScript only
// (tests).
func ITerm2Using(run Script, installed func() bool) Terminal {
	return &iterm2{run: run, installed: installed}
}

func (t *iterm2) ID() string   { return IDITerm2 }
func (t *iterm2) Name() string { return "iTerm2" }
func (t *iterm2) caps() Caps {
	return Caps{Labels: true, Tabs: true, Hold: true, Watch: t.api.usable()}
}

func (t *iterm2) Available(context.Context) error {
	if t.installed() {
		return nil
	}
	return ErrNotInstalled
}

// iterm2Open is the script that opens a launch: a tab in the current window, or a window.
func iterm2Open(l Launch) string {
	cmd := asString(commandLine(l, "--hold"))
	var b strings.Builder
	b.WriteString("tell application id \"" + iterm2Bundle + "\"\n")
	if l.Where == NewWindow {
		b.WriteString("\tset w to (create window with default profile command " + cmd + ")\n")
		b.WriteString("\tset s to current session of w\n")
	} else {
		b.WriteString("\tset w to current window\n")
		b.WriteString("\tif w is missing value then\n")
		b.WriteString("\t\tset w to (create window with default profile command " + cmd + ")\n")
		b.WriteString("\t\tset s to current session of w\n")
		b.WriteString("\telse\n")
		b.WriteString("\t\ttell w to set t to (create tab with default profile command " + cmd + ")\n")
		b.WriteString("\t\tset s to current session of t\n")
		b.WriteString("\tend if\n")
	}
	b.WriteString("\tactivate\n")
	b.WriteString("\treturn (unique ID of s) & tab & (tty of s)\n")
	b.WriteString("end tell")
	return b.String()
}

// iterm2Find is the script that finds the session whose tty is tty, without starting
// iTerm2 when it is not running.
func iterm2Find(tty string) string {
	return "if application id \"" + iterm2Bundle + "\" is not running then return \"\"\n" +
		"tell application id \"" + iterm2Bundle + "\"\n" +
		"\trepeat with w in windows\n" +
		"\t\trepeat with t in tabs of w\n" +
		"\t\t\trepeat with s in sessions of t\n" +
		"\t\t\t\tif tty of s is " + asString(tty) + " then return (unique ID of s) & tab & (tty of s)\n" +
		"\t\t\tend repeat\n" +
		"\t\tend repeat\n" +
		"\tend repeat\n" +
		"end tell\n" +
		"return \"\""
}

// iterm2Focus is the script that selects the session with this id and tty (both must
// still match), its tab and its window, and brings iTerm2 forward.
func iterm2Focus(h Handle) string {
	return "if application id \"" + iterm2Bundle + "\" is not running then return \"\"\n" +
		"tell application id \"" + iterm2Bundle + "\"\n" +
		"\trepeat with w in windows\n" +
		"\t\trepeat with t in tabs of w\n" +
		"\t\t\trepeat with s in sessions of t\n" +
		"\t\t\t\tif (unique ID of s is " + asString(h.Ref) + ") and (tty of s is " + asString(h.TTY) + ") then\n" +
		"\t\t\t\t\tselect w\n" +
		"\t\t\t\t\tselect t\n" +
		"\t\t\t\t\tselect s\n" +
		"\t\t\t\t\tactivate\n" +
		"\t\t\t\t\treturn \"ok\"\n" +
		"\t\t\t\tend if\n" +
		"\t\t\tend repeat\n" +
		"\t\tend repeat\n" +
		"\tend repeat\n" +
		"end tell\n" +
		"return \"\""
}

func (t *iterm2) Open(ctx context.Context, l Launch) (Handle, error) {
	if err := l.check(); err != nil {
		return Handle{}, err
	}
	if l.Where == Beside && t.api.usable() {
		if h, err := t.openBeside(ctx, l); err == nil {
			return h, nil
		}
		// Any API error: a tab through AppleScript, as without the API.
	}
	out, err := t.run.run(ctx, iterm2Open(l))
	if err != nil {
		return Handle{}, err
	}
	return handleFor(t.handle(out), l), nil
}

// handle reads "<unique id>\t<tty>" as iTerm2 returned it, keeping only well-formed values.
func (t *iterm2) handle(out string) Handle {
	h := Handle{Terminal: IDITerm2}
	ref, tty, _ := strings.Cut(strings.TrimSpace(out), "\t")
	if refName.MatchString(ref) {
		h.Ref = ref
	}
	if ttyName.MatchString(tty) {
		h.TTY = tty
	}
	return h
}

func (t *iterm2) FindTTY(ctx context.Context, tty string) (Handle, bool, error) {
	if !ttyName.MatchString(tty) {
		return Handle{}, false, fmt.Errorf("not a terminal device: %q", tty)
	}
	if !t.installed() {
		return Handle{}, false, nil
	}
	if t.api.usable() {
		if h, found, err := t.findAPI(ctx, tty); err == nil {
			return h, found, nil
		}
	}
	out, err := t.run.run(ctx, iterm2Find(tty))
	if err != nil {
		return Handle{}, false, err
	}
	h := t.handle(out)
	if h.Ref == "" || h.TTY != tty {
		return Handle{}, false, nil
	}
	return h, true, nil
}

func (t *iterm2) Focus(ctx context.Context, h Handle) error {
	if !refName.MatchString(h.Ref) || !ttyName.MatchString(h.TTY) {
		return fmt.Errorf("not an iTerm2 session hopsesh found: %+v", h)
	}
	if t.api.usable() {
		switch err := t.focusAPI(ctx, h); {
		case err == nil, errors.Is(err, ErrGone):
			return err
		}
	}
	out, err := t.run.run(ctx, iterm2Focus(h))
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != "ok" {
		return ErrGone
	}
	return nil
}
