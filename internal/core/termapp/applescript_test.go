package termapp

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden AppleScript files")

// fakeScripts records the scripts a terminal would run and answers them; it never runs
// osascript (no test drives a real terminal app).
type fakeScripts struct {
	mu      sync.Mutex
	scripts []string
	answer  func(script string) (string, error)
}

func (f *fakeScripts) run(_ context.Context, script string) (string, error) {
	f.mu.Lock()
	f.scripts = append(f.scripts, script)
	f.mu.Unlock()
	if f.answer == nil {
		return "", nil
	}
	return f.answer(script)
}

func (f *fakeScripts) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.scripts) == 0 {
		return ""
	}
	return f.scripts[len(f.scripts)-1]
}

func yes() bool { return true }
func no() bool  { return false }

var testLaunch = Launch{Program: "/Applications/hopsesh.app/Contents/MacOS/hopsesh", Args: []string{"terminal-open", "0123456789abcdef"},
	Dir: "/Users/someone/git/demo", Kind: KindSession, Where: NewTab}

// generated is every script hopsesh can generate, by golden file name.
func generated() map[string]string {
	win := testLaunch
	win.Where = NewWindow
	h := Handle{Terminal: IDITerm2, Ref: "w0t1p0-6A1F2C3D-0000-4E5F-9ABC-DEF012345678", TTY: "/dev/ttys004"}
	return map[string]string{
		"iterm2-open-tab.applescript":    iterm2Open(testLaunch),
		"iterm2-open-window.applescript": iterm2Open(win),
		"iterm2-find.applescript":        iterm2Find("/dev/ttys004"),
		"iterm2-focus.applescript":       iterm2Focus(h),
		"terminal-open.applescript":      terminalOpen(testLaunch),
		"terminal-find.applescript":      terminalFind("/dev/ttys004"),
		"terminal-focus.applescript":     terminalFocus("/dev/ttys004"),
	}
}

// The generated scripts are exactly the reviewed ones in testdata (go test -update
// rewrites them, for review in the diff).
func TestScriptsGolden(t *testing.T) {
	for name, script := range generated() {
		p := filepath.Join("testdata", name)
		if *update {
			if err := os.WriteFile(p, []byte(script+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("%s: %v (run go test -update)", name, err)
		}
		if string(want) != script+"\n" {
			t.Errorf("%s changed:\n%s\nwant:\n%s", name, script, want)
		}
	}
}

// Rule 12: every generated script uses only the allowed lines, and none could type into
// a session, read one, or reach another app.
func TestScriptsAllowlist(t *testing.T) {
	forbidden := regexp.MustCompile(`(?i)\bwrite\b|contents|\btext of\b|\bhistory\b|keystroke|key code|System Events|variable|do shell script|cookie|API|\bsplit\b|\bclose\b|\bdelete\b|profile "|do script .* in `)
	for name, script := range generated() {
		if err := VetScript(script); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		// Words in hopsesh's own path ("…/Contents/MacOS/hopsesh") are not script.
		code := regexp.MustCompile(literal).ReplaceAllString(script, `""`)
		if m := forbidden.FindString(code); m != "" {
			t.Errorf("%s has %q", name, m)
		}
	}
	// The vetting refuses what the generator must never produce.
	for _, bad := range []string{
		"tell application id \"com.googlecode.iterm2\"\n\ttell current session of current window to write text \"ls\"\nend tell",
		"tell application id \"com.googlecode.iterm2\"\n\treturn contents of current session of current window\nend tell",
		"tell application id \"com.googlecode.iterm2\"\n\treturn text of current session of current window\nend tell",
		"tell application \"System Events\" to keystroke \"x\"",
		"tell application id \"com.googlecode.iterm2\"\n\tset variable named \"user.x\" to \"y\"\nend tell",
		"tell application id \"com.apple.Terminal\"\n\tdo script \"ls\" in front window\nend tell",
		"tell application id \"com.apple.Terminal\"\n\tset t to do script \"ls\" in tab 1 of window 1\nend tell",
		"do shell script \"rm -rf ~\"",
		"tell application id \"com.googlecode.iterm2\"\n\trequest cookie and key for app named \"hopsesh\"\nend tell",
		"tell application id \"com.apple.Terminal\"\n\treturn history of tab 1 of window 1\nend tell",
		"tell application \"Finder\"\n\tactivate\nend tell",
	} {
		if VetScript(bad) == nil {
			t.Errorf("vetted: %q", bad)
		}
	}
}

// A path with quotes and backslashes stays one AppleScript string and one shell word;
// launches whose arguments are not plain words never reach a script.
func TestScriptQuoting(t *testing.T) {
	l := testLaunch
	l.Program = `/Users/o"brien/Apps\x/hopsesh`
	s := iterm2Open(l)
	if err := VetScript(s); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, `command "'/Users/o\"brien/Apps\\x/hopsesh' terminal-open 0123456789abcdef --hold"`) {
		t.Fatalf("quoting:\n%s", s)
	}
	f := &fakeScripts{}
	it := ITerm2Using(f.run, yes)
	for _, bad := range []Launch{
		{Program: "hopsesh", Args: []string{"terminal-open", "x"}, Kind: KindSession},
		{Program: "/bin/hopsesh\nrm", Args: []string{"terminal-open"}, Kind: KindSession},
		{Program: "/bin/hopsesh", Args: []string{"terminal-open", "a b"}, Kind: KindSession},
		{Program: "/bin/hopsesh", Args: []string{`x"; do shell script "rm`}, Kind: KindSession},
		{Program: "/bin/hopsesh", Args: []string{"terminal-open", "0123456789abcdef"}, Kind: "other"},
	} {
		if _, err := it.Open(context.Background(), bad); err == nil {
			t.Errorf("opened %+v", bad)
		}
	}
	if len(f.scripts) != 0 {
		t.Fatalf("scripts ran: %v", f.scripts)
	}
}

// iTerm2 opens a tab running hopsesh's verb with --hold, and reads back only a
// well-formed session id and terminal device.
func TestITerm2Open(t *testing.T) {
	f := &fakeScripts{answer: func(string) (string, error) {
		return "w0t1p0-6A1F2C3D\t/dev/ttys004\n", nil
	}}
	h, err := ITerm2Using(f.run, yes).Open(context.Background(), testLaunch)
	if err != nil || h.Ref != "w0t1p0-6A1F2C3D" || h.TTY != "/dev/ttys004" || h.Terminal != IDITerm2 {
		t.Fatalf("%+v %v", h, err)
	}
	if !strings.Contains(f.last(), "create tab with default profile command") || !strings.Contains(f.last(), "--hold") {
		t.Fatal(f.last())
	}
	f.answer = func(string) (string, error) { return "evil\"id\t/etc/passwd", nil }
	h, _ = ITerm2Using(f.run, yes).Open(context.Background(), testLaunch)
	if h.Ref != "" || h.TTY != "" {
		t.Fatalf("kept %+v", h)
	}
	if _, err := ITerm2Using(f.run, no).Open(context.Background(), testLaunch); err != nil {
		// Open does not check installation (Set.Open does); a missing app fails in osascript.
		t.Logf("not installed: %v", err)
	}
}

// Terminal opens a new window (do script, never into a tab) without --hold: its shell
// stays.
func TestTerminalAppOpen(t *testing.T) {
	f := &fakeScripts{answer: func(string) (string, error) { return "/dev/ttys009", nil }}
	h, err := TerminalAppUsing(f.run, yes).Open(context.Background(), testLaunch)
	if err != nil || h.TTY != "/dev/ttys009" {
		t.Fatalf("%+v %v", h, err)
	}
	if strings.Contains(f.last(), "--hold") || !strings.Contains(f.last(), "set t to do script \"/Applications/hopsesh.app/Contents/MacOS/hopsesh terminal-open 0123456789abcdef\"") {
		t.Fatal(f.last())
	}
}

// osascript's errors: a refused Automation permission is ErrDenied, a missing app
// ErrNotInstalled, anything else its (sanitised) message.
func TestScriptErrors(t *testing.T) {
	base := errors.New("exit status 1")
	cases := []struct {
		stderr string
		want   error
	}{
		{"58:120: execution error: Not authorized to send Apple events to iTerm. (-1743)\n", ErrDenied},
		{"execution error: hopsesh is not allowed assistive access. (-1744)", ErrDenied},
		{"execution error: Can’t get application id \"com.googlecode.iterm2\". (-1728)", ErrNotInstalled},
		{"LSOpenURLsWithRole() failed (-10814)", ErrNotInstalled},
	}
	for _, c := range cases {
		if err := scriptError(c.stderr, base); !errors.Is(err, c.want) {
			t.Errorf("%q: %v", c.stderr, err)
		}
	}
	err := scriptError("execution error: iTerm got an error: \x1b]0;x\a boom (-2753)", base)
	if errors.Is(err, ErrDenied) || strings.ContainsAny(err.Error(), "\x1b\a") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("%q", err)
	}
	if err := scriptError("", base); err != base {
		t.Fatal(err)
	}
}
