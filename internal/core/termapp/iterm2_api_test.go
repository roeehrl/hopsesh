package termapp

import (
	"context"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/termapp/iterm2api"
	"github.com/roeehrl/hopsesh/internal/core/termapp/iterm2api/fakeiterm2"
)

// The iTerm2 backend with its Python API on, against fakeiterm2 (a fake API server on a
// Unix socket in a temporary folder): no test reaches a real iTerm2 or sends an Apple
// Event. The AppleScript side is fakeScripts.

type apiFixture struct {
	srv     *fakeiterm2.Server
	scripts *fakeScripts
	creds   atomic.Int32
	refuse  atomic.Bool
	mu      sync.Mutex
	codes   map[string]int // ticket → exit code hopsesh's verb wrote
	term    *iterm2
}

func newAPIFixture(t *testing.T) *apiFixture {
	t.Helper()
	t.Setenv("ITERM2_COOKIE", "")
	t.Setenv("ITERM2_KEY", "")
	srv, err := fakeiterm2.Start(true)
	if err != nil {
		t.Skipf("unix sockets unavailable here: %v", err)
	}
	t.Cleanup(srv.Close)
	f := &apiFixture{srv: srv, scripts: &fakeScripts{}, codes: map[string]int{}}
	api := newAPIPath(iterm2api.Options{
		SocketPath: srv.Path,
		Credentials: func(context.Context, string) (iterm2api.Credentials, error) {
			f.creds.Add(1)
			if f.refuse.Load() {
				return iterm2api.Credentials{}, iterm2api.ErrNotAuthorized
			}
			return iterm2api.NewCredentials(srv.Issue()), nil
		},
	})
	f.term = &iterm2{run: f.scripts.run, installed: yes, api: api}
	f.term = f.term.withExits(func(h Handle) (int, bool) {
		f.mu.Lock()
		defer f.mu.Unlock()
		c, ok := f.codes[h.Ticket]
		return c, ok
	}).(*iterm2)
	return f
}

func (f *apiFixture) exit(ticket string, code int) {
	f.mu.Lock()
	f.codes[ticket] = code
	f.mu.Unlock()
}

func stepLaunch() Launch {
	l := testLaunch
	l.Args = []string{"terminal-step", "fedcba9876543210"}
	l.Kind, l.Where = KindStep, Beside
	return l
}

func ctx5(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// A step opens beside the session in front, through the API, running hopsesh's verb with
// --hold; no AppleScript runs.
func TestAPIOpensStepBeside(t *testing.T) {
	f := newAPIFixture(t)
	win, front := f.srv.AddWindow()
	h, err := f.term.Open(ctx5(t), stepLaunch())
	if err != nil {
		t.Fatal(err)
	}
	if len(f.scripts.scripts) != 0 {
		t.Fatalf("AppleScript ran: %v", f.scripts.scripts)
	}
	if w, tab, ok := f.srv.Locate(h.Ref); !ok || w != win {
		t.Fatalf("split went to %s/%d", w, tab)
	}
	_, frontTab, _ := f.srv.Locate(front)
	if _, tab, _ := f.srv.Locate(h.Ref); tab != frontTab {
		t.Fatal("the split is not in the front session's tab")
	}
	if h.TTY != f.srv.TTY(h.Ref) || h.Verb != "terminal-step" || h.Ticket != "fedcba9876543210" || h.Terminal != IDITerm2 {
		t.Fatalf("handle %+v", h)
	}
	cmd := f.srv.Props(h.Ref)["Command"]
	if !strings.Contains(cmd, "terminal-step fedcba9876543210 --hold") {
		t.Fatalf("command %s", cmd)
	}
	if a := f.srv.Activated(); len(a) != 1 || a[0] != h.Ref {
		t.Fatalf("activated %v", a)
	}
	if r := f.srv.Refused(); len(r) != 0 {
		t.Fatalf("requests outside the allowlist: %v", r)
	}
	// A session launch (Where NewTab) still opens through AppleScript.
	f.scripts.answer = func(string) (string, error) { return "w0t1p0-AB\t/dev/ttys044", nil }
	h, err = f.term.Open(ctx5(t), testLaunch)
	if err != nil || h.Ref != "w0t1p0-AB" || h.Verb != "terminal-open" || h.Ticket != "0123456789abcdef" {
		t.Fatalf("%+v %v", h, err)
	}
}

// Without the API (no socket), or once it was refused, everything is the AppleScript path,
// and a refusal is never asked again.
func TestAPIFallsBackSilently(t *testing.T) {
	f := newAPIFixture(t)
	f.srv.AddWindow()
	f.scripts.answer = func(string) (string, error) { return "w0t1p0-AB\t/dev/ttys044", nil }

	f.refuse.Store(true)
	for range 3 {
		if _, err := f.term.Open(ctx5(t), stepLaunch()); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.creds.Load(); got != 1 {
		t.Fatalf("credential requests = %d, want 1 (a refusal is final)", got)
	}
	if len(f.scripts.scripts) != 3 {
		t.Fatalf("AppleScript opens = %d", len(f.scripts.scripts))
	}
	if CapsOf(f.term).Watch {
		t.Fatal("Watch claimed after a refusal")
	}
	if _, err := f.term.Exited(ctx5(t), Handle{Terminal: IDITerm2, TTY: "/dev/ttys044"}); err == nil {
		t.Fatal("Exited without the API")
	}

	// The API turned off: no socket, no prompt, AppleScript.
	g := newAPIFixture(t)
	g.srv.Stop()
	g.scripts.answer = f.scripts.answer
	if CapsOf(g.term).Watch {
		t.Fatal("Watch claimed with the API off")
	}
	if _, err := g.term.Open(ctx5(t), stepLaunch()); err != nil || len(g.scripts.scripts) != 1 || g.creds.Load() != 0 {
		t.Fatalf("err %v, scripts %d, creds %d", err, len(g.scripts.scripts), g.creds.Load())
	}
	// ITerm2Using is AppleScript only.
	if CapsOf(ITerm2Using(f.scripts.run, yes)).Watch {
		t.Fatal("the AppleScript-only iTerm2 claims Watch")
	}
}

// FindTTY and Focus go through the API, with the tty checked again before focusing.
func TestAPIFindAndFocus(t *testing.T) {
	f := newAPIFixture(t)
	_, s1 := f.srv.AddWindow()
	_, s2 := f.srv.AddWindow()
	tty := f.srv.TTY(s2)
	h, found, err := f.term.FindTTY(ctx5(t), tty)
	if err != nil || !found || h.Ref != s2 || h.TTY != tty {
		t.Fatalf("%+v %v %v", h, found, err)
	}
	if _, found, err := f.term.FindTTY(ctx5(t), "/dev/ttys999"); found || err != nil {
		t.Fatalf("found %v %v", found, err)
	}
	if err := f.term.Focus(ctx5(t), h); err != nil {
		t.Fatal(err)
	}
	if a := f.srv.Activated(); len(a) != 1 || a[0] != s2 {
		t.Fatalf("activated %v", a)
	}
	// The session's device is no longer the one found: gone, not focused.
	if err := f.term.Focus(ctx5(t), Handle{Terminal: IDITerm2, Ref: s1, TTY: tty}); !errors.Is(err, ErrGone) {
		t.Fatalf("Focus with a reused tty: %v", err)
	}
	if len(f.scripts.scripts) != 0 {
		t.Fatalf("AppleScript ran: %v", f.scripts.scripts)
	}
	if !CapsOf(f.term).Watch {
		t.Fatal("Watch not claimed with the API on")
	}
}

// Exited: the exit code comes from hopsesh's records as soon as the verb writes it (the
// held tab stays open), and a tab closed first ends with -1 (or the code, when written).
func TestAPIExited(t *testing.T) {
	f := newAPIFixture(t)
	f.srv.AddWindow()
	ctx := ctx5(t)

	// The agent exits 3; its tab is held open as a shell, so iTerm2 says nothing.
	a, err := f.term.Open(ctx, stepLaunch())
	if err != nil {
		t.Fatal(err)
	}
	ch, err := f.term.Exited(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	f.exit(a.Ticket, 3)
	if code := recv(t, ch); code != 3 {
		t.Fatalf("code %d", code)
	}
	if _, _, ok := f.srv.Locate(a.Ref); !ok {
		t.Fatal("the held tab was closed")
	}

	// The user closes the tab while the agent runs: -1.
	l := stepLaunch()
	l.Args = []string{"terminal-step", "1111111111111111"}
	b, err := f.term.Open(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	ch, err = f.term.Exited(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	f.srv.Terminate(b.Ref)
	if code := recv(t, ch); code != -1 {
		t.Fatalf("closed tab: code %d", code)
	}

	// A tab that is gone already: the code if written, else -1, at once.
	f.exit("2222222222222222", 7)
	ch, err = f.term.Exited(ctx, Handle{Terminal: IDITerm2, TTY: "/dev/ttys998", Verb: "terminal-open", Ticket: "2222222222222222"})
	if err != nil {
		t.Fatal(err)
	}
	if code := recv(t, ch); code != 7 {
		t.Fatalf("gone tab: code %d", code)
	}
	if r := f.srv.Refused(); len(r) != 0 {
		t.Fatalf("requests outside the allowlist: %v", r)
	}
}

func recv(t *testing.T, ch <-chan int) int {
	t.Helper()
	select {
	case c, ok := <-ch:
		if !ok {
			t.Fatal("closed without a code")
		}
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("no exit reported")
	}
	return 0
}

// Exit codes the verb writes are read back by ticket; old ones are pruned.
func TestStoreExits(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if _, ok := s.ExitOf("0123456789abcdef"); ok {
		t.Fatal("an exit before any was written")
	}
	if err := s.Exit("0123456789abcdef", 3); err != nil {
		t.Fatal(err)
	}
	if c, ok := s.ExitOf("0123456789abcdef"); !ok || c != 3 {
		t.Fatalf("%d %v", c, ok)
	}
	if err := s.Exit("../evil", 1); err == nil {
		t.Fatal("a non-ticket id was written")
	}
	old := filepath.Join(s.Dir, "exited", "aaaaaaaaaaaaaaaa.json")
	if err := os.WriteFile(old, []byte(`{"code":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(old, past, past)
	_ = s.Exit("bbbbbbbbbbbbbbbb", 0)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("an exit older than a day was kept")
	}
}

// Labels hopsesh cleans with Sanitize are always ones the API client accepts (the client
// refuses unclean ones rather than cleaning them a second way).
func TestSanitizeSatisfiesAPILabels(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	alphabet := []rune("ab é\x00\x07\x1b[]\x7f\u0085\u009b\u200e\u202e\u2066\u061c\t\n\ufffd😀")
	for range 2000 {
		n := r.IntN(120)
		var b strings.Builder
		for range n {
			b.WriteRune(alphabet[r.IntN(len(alphabet))])
		}
		if r.IntN(10) == 0 {
			b.WriteString("\xff\xfe")
		}
		s := Sanitize(b.String(), 80)
		if !iterm2api.ValidLabel(s) {
			t.Fatalf("Sanitize(%q) = %q, refused by the API client", b.String(), s)
		}
	}
}

// The API cookie request is the one script the AppleScript allowlist has for it, for
// hopsesh's own name only; it never launches iTerm2.
func TestCookieScriptIsVetted(t *testing.T) {
	if err := VetScript(iterm2api.CookieScript("hopsesh")); err != nil {
		t.Fatal(err)
	}
	if VetScript(iterm2api.CookieScript("someone-else")) == nil {
		t.Fatal("a cookie for another name was vetted")
	}
	if !strings.HasPrefix(iterm2api.CookieScript("hopsesh"), `if application id "com.googlecode.iterm2" is not running then return ""`) {
		t.Fatal("the cookie script could start iTerm2")
	}
	if sharedAPI != nil {
		t.Fatal("the shared API path is on under test")
	}
}
