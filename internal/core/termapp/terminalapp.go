package termapp

import (
	"context"
	"fmt"
	"strings"
)

// Terminal (macOS's own) through AppleScript. It has no verb for a new tab, so a launch
// opens a new window: `do script` with no target starts a shell there and runs hopsesh's
// command in it (never `do script … in` a tab that exists, which would type into it).
// The shell stays when the command ends, so the window keeps its output without --hold.
// A tab is found by its tty (Terminal still exposes it on macOS 27) and brought forward.

const terminalBundle = "com.apple.Terminal"

type terminalApp struct {
	run       Script
	installed func() bool
}

// TerminalApp is Terminal on this Mac.
func TerminalApp() Terminal {
	return TerminalAppUsing(Osascript, bundle("Terminal.app", "/System/Applications/Utilities", "/Applications/Utilities"))
}

// TerminalAppUsing is Terminal driven by run, installed as installed says (tests).
func TerminalAppUsing(run Script, installed func() bool) Terminal {
	return &terminalApp{run: run, installed: installed}
}

func (t *terminalApp) ID() string   { return IDTerminalApp }
func (t *terminalApp) Name() string { return "Terminal" }
func (t *terminalApp) caps() Caps   { return Caps{} }

func (t *terminalApp) Available(context.Context) error {
	if t.installed() {
		return nil
	}
	return ErrNotInstalled
}

func terminalOpen(l Launch) string {
	return "tell application id \"" + terminalBundle + "\"\n" +
		"\tset t to do script " + asString(commandLine(l)) + "\n" +
		"\tactivate\n" +
		"\treturn tty of t\n" +
		"end tell"
}

func terminalFind(tty string) string {
	return "if application id \"" + terminalBundle + "\" is not running then return \"\"\n" +
		"tell application id \"" + terminalBundle + "\"\n" +
		"\trepeat with w in windows\n" +
		"\t\trepeat with t in tabs of w\n" +
		"\t\t\tif tty of t is " + asString(tty) + " then return tty of t\n" +
		"\t\tend repeat\n" +
		"\tend repeat\n" +
		"end tell\n" +
		"return \"\""
}

func terminalFocus(tty string) string {
	return "if application id \"" + terminalBundle + "\" is not running then return \"\"\n" +
		"tell application id \"" + terminalBundle + "\"\n" +
		"\trepeat with w in windows\n" +
		"\t\trepeat with t in tabs of w\n" +
		"\t\t\tif tty of t is " + asString(tty) + " then\n" +
		"\t\t\t\tset selected tab of w to t\n" +
		"\t\t\t\tset frontmost of w to true\n" +
		"\t\t\t\tactivate\n" +
		"\t\t\t\treturn \"ok\"\n" +
		"\t\t\tend if\n" +
		"\t\tend repeat\n" +
		"\tend repeat\n" +
		"end tell\n" +
		"return \"\""
}

func (t *terminalApp) Open(ctx context.Context, l Launch) (Handle, error) {
	if err := l.check(); err != nil {
		return Handle{}, err
	}
	out, err := t.run.run(ctx, terminalOpen(l))
	if err != nil {
		return Handle{}, err
	}
	h := Handle{Terminal: IDTerminalApp}
	if tty := strings.TrimSpace(out); ttyName.MatchString(tty) {
		h.TTY, h.Ref = tty, tty
	}
	return h, nil
}

func (t *terminalApp) FindTTY(ctx context.Context, tty string) (Handle, bool, error) {
	if !ttyName.MatchString(tty) {
		return Handle{}, false, fmt.Errorf("not a terminal device: %q", tty)
	}
	if !t.installed() {
		return Handle{}, false, nil
	}
	out, err := t.run.run(ctx, terminalFind(tty))
	if err != nil {
		return Handle{}, false, err
	}
	if strings.TrimSpace(out) != tty {
		return Handle{}, false, nil
	}
	return Handle{Terminal: IDTerminalApp, Ref: tty, TTY: tty}, true, nil
}

func (t *terminalApp) Focus(ctx context.Context, h Handle) error {
	if !ttyName.MatchString(h.TTY) {
		return fmt.Errorf("not a Terminal tab hopsesh found: %+v", h)
	}
	out, err := t.run.run(ctx, terminalFocus(h.TTY))
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != "ok" {
		return ErrGone
	}
	return nil
}
