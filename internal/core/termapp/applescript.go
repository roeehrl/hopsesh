package termapp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/launch"
	"github.com/roeehrl/hopsesh/internal/core/proc"
)

// AppleScript is how hopsesh drives iTerm2 and Terminal on macOS. Every script it runs is
// generated here from a fixed set of lines (allowedLines) and checked against that set
// before it runs: open a window or tab with a command, read a tab's tty or id, select a
// tab, and bring the app forward. Nothing types into a tab, reads what one shows, or
// changes an app's settings, and nothing the person or a vendor wrote reaches a script:
// the only values in one are hopsesh's path with a verb and an id, a terminal device
// name, and an id the app itself gave.

// Script runs an AppleScript and returns what it returned (osascript, or a test's fake).
type Script func(ctx context.Context, script string) (string, error)

// literal is an AppleScript string literal.
const literal = `"(?:[^"\\]|\\.)*"`

// allowedLines are the only lines a generated script may have (leading tabs aside).
// <cmd> is an AppleScript string, <tty> a terminal device, <ref> an iTerm2 session id.
var allowedLines = func() []*regexp.Regexp {
	pats := []string{
		// Both apps.
		`tell application id "(?:com\.googlecode\.iterm2|com\.apple\.Terminal)"`,
		`if application id "(?:com\.googlecode\.iterm2|com\.apple\.Terminal)" is not running then return ""`,
		`end tell`, `end if`, `else`, `end repeat`, `activate`, `return ""`, `return "ok"`,
		`repeat with w in windows`, `repeat with t in tabs of w`,
		// iTerm2: open a window or a tab that runs hopsesh's command, never a typed line.
		`set w to current window`,
		`if w is missing value then`,
		`set w to \(create window with default profile command <cmd>\)`,
		`tell w to set t to \(create tab with default profile command <cmd>\)`,
		`set s to current session of [wt]`,
		`return \(unique ID of s\) & tab & \(tty of s\)`,
		`repeat with s in sessions of t`,
		`if tty of s is <tty> then return \(unique ID of s\) & tab & \(tty of s\)`,
		`if \(unique ID of s is <ref>\) and \(tty of s is <tty>\) then`,
		`select [wts]`,
		// iTerm2's Python API, when the user turned it on: a cookie for hopsesh's own
		// connection (iterm2api.CookieScript), nothing else.
		`return \(request cookie and key for app named "hopsesh"\)`,
		// Terminal: a new window running hopsesh's command (do script without "in", so
		// never into a tab that exists), its tab's tty, and bringing a tab forward.
		`set t to do script <cmd>`,
		`return tty of t`,
		`if tty of t is <tty> then return tty of t`,
		`if tty of t is <tty> then`,
		`set selected tab of w to t`,
		`set frontmost of w to true`,
	}
	r := strings.NewReplacer("<cmd>", literal, "<tty>", `"/dev/ttys[0-9]{1,4}"`, "<ref>", `"[A-Za-z0-9-]{1,64}"`)
	out := make([]*regexp.Regexp, len(pats))
	for i, p := range pats {
		out[i] = regexp.MustCompile(`^` + r.Replace(p) + `$`)
	}
	return out
}()

// VetScript reports the first line of a script that is not one hopsesh may run.
func VetScript(script string) error {
	for i, line := range strings.Split(script, "\n") {
		line = strings.TrimLeft(line, "\t")
		ok := false
		for _, re := range allowedLines {
			if re.MatchString(line) {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("line %d is not one hopsesh runs: %q", i+1, line)
		}
	}
	return nil
}

// run vets a script and runs it.
func (s Script) run(ctx context.Context, script string) (string, error) {
	if err := VetScript(script); err != nil {
		return "", err
	}
	return s(ctx, script)
}

// Osascript runs a script with macOS's osascript (from standard input, so no argument
// list carries it), and maps its errors: -1743/-1744 (not authorised to send Apple
// events) to ErrDenied, -1728/-10814 (no such application) to ErrNotInstalled.
func Osascript(ctx context.Context, script string) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", errors.New("AppleScript runs on macOS only")
	}
	if testing.Testing() {
		// No test drives a real terminal app (or asks macOS for permission to).
		return "", errors.New("tests never run osascript")
	}
	cmd := proc.CommandContext(ctx, "osascript", "-")
	cmd.Stdin = strings.NewReader(script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", scriptError(stderr.String(), err)
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

var errCode = regexp.MustCompile(`\((-?[0-9]+)\)\s*$`)

// scriptError is what an osascript failure means, from its standard error.
func scriptError(stderr string, err error) error {
	msg := strings.TrimSpace(Sanitize(stderr, 200))
	code := ""
	if m := errCode.FindStringSubmatch(strings.TrimSpace(stderr)); m != nil {
		code = m[1]
	}
	switch code {
	case "-1743", "-1744":
		return fmt.Errorf("%w (System Settings › Privacy & Security › Automation)", ErrDenied)
	case "-1728", "-10814", "-2700":
		if strings.Contains(stderr, "application") || code == "-10814" {
			return fmt.Errorf("%w: %s", ErrNotInstalled, msg)
		}
	}
	if msg == "" {
		return err
	}
	return fmt.Errorf("osascript: %s", msg)
}

// asString is s as an AppleScript string literal.
func asString(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

// commandLine is a launch as one POSIX command line: hopsesh's quoted path, its plain
// words, and quoted plain paths (Launch.check holds them to that).
func commandLine(l Launch, extra ...string) string {
	parts := []string{launch.ShQuote(l.Program)}
	for _, a := range l.Args {
		if !word.MatchString(a) {
			a = launch.ShQuote(a)
		}
		parts = append(parts, a)
	}
	parts = append(parts, extra...)
	return strings.Join(parts, " ")
}

var (
	ttyName = regexp.MustCompile(`^/dev/ttys[0-9]{1,4}$`)
	refName = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
)

// bundle reports whether an app bundle is in /Applications, ~/Applications or one of
// extra (macOS only).
func bundle(name string, extra ...string) func() bool {
	return func() bool {
		if runtime.GOOS != "darwin" {
			return false
		}
		dirs := append([]string{"/Applications"}, extra...)
		if home, err := os.UserHomeDir(); err == nil {
			dirs = append(dirs, filepath.Join(home, "Applications"))
		}
		for _, d := range dirs {
			if fi, err := os.Stat(filepath.Join(d, name)); err == nil && fi.IsDir() {
				return true
			}
		}
		return false
	}
}
