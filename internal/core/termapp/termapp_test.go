package termapp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// When macOS denies hopsesh control of iTerm2, the launch opens in Terminal and says so;
// when Terminal is denied too, nothing opens and the error says to copy the command.
func TestOpenFallback(t *testing.T) {
	ctx := context.Background()
	denied := func(string) (string, error) {
		return "", scriptError("execution error: Not authorized to send Apple events to iTerm. (-1743)", errors.New("exit 1"))
	}
	itf, tf := &fakeScripts{answer: denied}, &fakeScripts{answer: func(string) (string, error) { return "/dev/ttys002", nil }}
	it, ta := ITerm2Using(itf.run, yes), TerminalAppUsing(tf.run, yes)
	set := NewSet(it, ta)

	o, err := set.Open(ctx, it, testLaunch)
	if err != nil || !o.FellBack || o.Terminal != "Terminal" || !strings.Contains(o.Reason, "iTerm2: macOS did not allow") || o.Handle.TTY != "/dev/ttys002" {
		t.Fatalf("%+v %v", o, err)
	}
	tf.answer = denied
	_, err = set.Open(ctx, it, testLaunch)
	if !errors.Is(err, ErrNoTerminal) || !strings.Contains(err.Error(), "Terminal: macOS did not allow") {
		t.Fatal(err)
	}
	// iTerm2 not installed: Terminal, without asking iTerm2 anything.
	itf.scripts, tf.answer = nil, func(string) (string, error) { return "/dev/ttys002", nil }
	o, err = NewSet(ITerm2Using(itf.run, no), ta).Open(ctx, ITerm2Using(itf.run, no), testLaunch)
	if err != nil || !o.FellBack || len(itf.scripts) != 0 {
		t.Fatalf("%+v %v %d", o, err, len(itf.scripts))
	}
	// Another failure is reported as it is, not hidden by a fallback.
	itf.answer = func(string) (string, error) { return "", errors.New("osascript: iTerm got an error") }
	if _, err := set.Open(ctx, it, testLaunch); err == nil || errors.Is(err, ErrNoTerminal) || !strings.Contains(err.Error(), "iTerm got an error") {
		t.Fatal(err)
	}
}

// Choose takes the configured terminal when it is installed, else the best installed one.
func TestChoose(t *testing.T) {
	ctx := context.Background()
	f := &fakeScripts{}
	set := NewSet(ITerm2Using(f.run, yes), TerminalAppUsing(f.run, yes))
	if got := set.Choose(ctx, ""); got.ID() != IDITerm2 {
		t.Fatal(got.ID())
	}
	if got := set.Choose(ctx, IDTerminalApp); got.ID() != IDTerminalApp {
		t.Fatal(got.ID())
	}
	set = NewSet(ITerm2Using(f.run, no), TerminalAppUsing(f.run, yes))
	if got := set.Choose(ctx, IDITerm2); got.ID() != IDTerminalApp {
		t.Fatal(got.ID())
	}
	info := set.Detect(ctx)
	if len(info) != 2 || info[0].Installed || !info[1].Installed || !info[0].Caps.Find || !info[0].Caps.Labels || !info[0].Caps.Tabs || info[1].Caps.Tabs || info[0].Caps.Watch {
		t.Fatalf("%+v", info)
	}
}

// A line terminal (Windows Terminal, Linux, the tests') gets the launch's folder and
// hopsesh's verb: never a title or a prompt.
func TestLine(t *testing.T) {
	var got string
	lt := LineTerminal("test", "a terminal", func(line string) error { got = line; return nil })
	if _, err := lt.Open(context.Background(), testLaunch); err != nil {
		t.Fatal(err)
	}
	want := "cd /Users/someone/git/demo && " + testLaunch.Program + " terminal-open 0123456789abcdef"
	if runtime.GOOS == "windows" {
		want = "Set-Location '/Users/someone/git/demo'; & '" + testLaunch.Program + "' 'terminal-open' '0123456789abcdef'"
	}
	if got != want {
		t.Fatalf("%q", got)
	}
}

// Tickets: private files, read once, refused when old; records: kept while the process
// runs, dropped once it does not.
func TestStore(t *testing.T) {
	s := Store{Dir: filepath.Join(t.TempDir(), "terminal")}
	dir := t.TempDir()
	tk := Ticket{Kind: KindSession, Argv: []string{"claude", "--resume", "abc"}, Dir: dir, Key: "claude/abc", Labels: Labels{Title: "x"}}
	id, err := s.Save(tk)
	if err != nil || !IsTicket(id) {
		t.Fatal(id, err)
	}
	if runtime.GOOS != "windows" {
		for p, want := range map[string]os.FileMode{filepath.Join(s.Dir, "tickets"): 0o700, filepath.Join(s.Dir, "tickets", id+".json"): 0o600} {
			if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != want {
				t.Fatalf("%s: %v %v", p, fi.Mode().Perm(), err)
			}
		}
	}
	got, err := s.Take(id, time.Hour)
	if err != nil || got.Key != "claude/abc" || got.Argv[2] != "abc" {
		t.Fatal(got, err)
	}
	if _, err := s.Take(id, time.Hour); err == nil {
		t.Fatal("a ticket ran twice")
	}
	tk.Made = time.Now().Add(-2 * time.Hour)
	old, _ := s.Save(tk)
	if _, err := s.Take(old, time.Hour); err == nil {
		t.Fatal("an old ticket ran")
	}
	for _, bad := range []string{"../../etc/passwd", "0123", "ZZZZZZZZZZZZZZZZ"} {
		if _, err := s.Take(bad, time.Hour); err == nil {
			t.Fatal(bad)
		}
	}
	// Steps and sign-ins are never found again: no session key.
	step := tk
	step.Kind, step.Made = KindSignIn, time.Time{}
	sid, _ := s.Save(step)
	if got, _ := s.Take(sid, time.Hour); got.Key != "" {
		t.Fatal(got.Key)
	}
	if _, err := s.Save(Ticket{Kind: KindSession, Argv: []string{"claude"}, Dir: "relative"}); err == nil {
		t.Fatal("a relative folder")
	}

	ids := []string{"00000000000000a1", "00000000000000a2", "00000000000000a3"}
	for i, pid := range []int{11, 12, 13} {
		if err := s.Record(Record{Ticket: ids[i], Key: map[bool]string{true: "codex/t1", false: "claude/abc"}[i < 2], TTY: "/dev/ttys00" + string(rune('1'+i)), PID: pid}); err != nil {
			t.Fatal(err)
		}
	}
	alive := func(pid int) bool { return pid != 12 }
	rs := s.Running("codex/t1", alive)
	if len(rs) != 1 || rs[0].PID != 11 || rs[0].TTY != "/dev/ttys001" {
		t.Fatalf("%+v", rs)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "running", ids[1]+".json")); !os.IsNotExist(err) {
		t.Fatal("a dead record stays")
	}
	s.Forget(ids[0])
	if rs := s.Running("", alive); len(rs) != 1 || rs[0].Key != "claude/abc" {
		t.Fatalf("%+v", rs)
	}
}
