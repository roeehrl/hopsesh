package iterm2api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/termapp/iterm2api/fakeiterm2"
)

// No test here starts iTerm2, runs osascript or sends an Apple Event: every connection goes
// to fakeiterm2 on a socket in a temporary directory, and credentials come from the fake.

func startFake(t *testing.T, requireAuth bool) *fakeiterm2.Server {
	t.Helper()
	t.Setenv(envCookie, "")
	t.Setenv(envKey, "")
	srv, err := fakeiterm2.Start(requireAuth)
	if err != nil {
		t.Skipf("unix sockets unavailable here: %v", err)
	}
	t.Cleanup(srv.Close)
	return srv
}

// fakeCreds is a credential source backed by the fake, counting its calls.
func fakeCreds(srv *fakeiterm2.Server, calls *atomic.Int32) CredentialSource {
	return func(_ context.Context, app string) (Credentials, error) {
		calls.Add(1)
		if app != "hopsesh" {
			return Credentials{}, errors.New("unexpected app name " + app)
		}
		return NewCredentials(srv.Issue()), nil
	}
}

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func connect(t *testing.T, srv *fakeiterm2.Server) *Client {
	t.Helper()
	var calls atomic.Int32
	c, err := Connect(ctxT(t), Options{SocketPath: srv.Path, Credentials: fakeCreds(srv, &calls)})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestConnectProbesThenAsksForACookieOnce(t *testing.T) {
	srv := startFake(t, true)
	var calls atomic.Int32
	c, err := Connect(ctxT(t), Options{SocketPath: srv.Path, Credentials: fakeCreds(srv, &calls)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if calls.Load() != 1 {
		t.Fatalf("credential requests = %d, want 1", calls.Load())
	}
	hs := srv.Headers()
	if len(hs) != 2 {
		t.Fatalf("handshakes = %d, want 2 (probe, then authenticated)", len(hs))
	}
	probe, auth := hs[0], hs[1]
	if probe.Get("X-iTerm2-Cookie") != "" || probe.Get("X-iTerm2-Key") != "" {
		t.Error("the probe carried credentials")
	}
	for _, h := range hs {
		if h.Get("X-iTerm2-Disable-Auth-UI") != "true" {
			t.Error("handshake without x-iterm2-disable-auth-ui: iTerm2 could show its own dialog")
		}
		if h.Get("X-iTerm2-Advisory-Name") != "hopsesh" {
			t.Errorf("advisory name = %q", h.Get("X-iTerm2-Advisory-Name"))
		}
		if h.Get("Origin") != "ws://localhost/" {
			t.Errorf("origin = %q", h.Get("Origin"))
		}
		if !strings.HasPrefix(h.Get("X-iTerm2-Library-Version"), "hopsesh-go") {
			t.Errorf("library version = %q", h.Get("X-iTerm2-Library-Version"))
		}
		if !strings.Contains(h.Get("Sec-WebSocket-Protocol"), "api.iterm2.com") {
			t.Errorf("subprotocol = %q", h.Get("Sec-WebSocket-Protocol"))
		}
	}
	if auth.Get("X-iTerm2-Cookie") == "" || auth.Get("X-iTerm2-Key") == "" {
		t.Error("the authenticated handshake had no cookie and key")
	}
	if c.ProtocolVersion() != "1.19" {
		t.Errorf("protocol version = %q", c.ProtocolVersion())
	}
}

func TestConnectWithoutAuthNeedsNoCookie(t *testing.T) {
	srv := startFake(t, false)
	var calls atomic.Int32
	c, err := Connect(ctxT(t), Options{SocketPath: srv.Path, Credentials: fakeCreds(srv, &calls)})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if c.Err() != nil {
		t.Fatalf("Err after Close = %v", c.Err())
	}
	if calls.Load() != 0 {
		t.Fatalf("asked for a cookie %d times though the server let us in", calls.Load())
	}
}

func TestAPIDisabledNeverAsksForConsent(t *testing.T) {
	t.Setenv(envCookie, "")
	var calls atomic.Int32
	never := func(context.Context, string) (Credentials, error) {
		calls.Add(1)
		return Credentials{}, errors.New("must not be called")
	}
	dir, err := os.MkdirTemp("", "it2")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// No socket: the API is off (or iTerm2 never ran).
	_, err = Connect(ctxT(t), Options{SocketPath: filepath.Join(dir, "socket"), Credentials: never})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no socket: err = %v, want ErrUnavailable", err)
	}
	if err := Probe(ctxT(t), Options{SocketPath: filepath.Join(dir, "socket")}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Probe: %v", err)
	}

	// A stale socket file nobody listens on: iTerm2 quit or the API was turned off.
	stale := filepath.Join(dir, "stale")
	ln, err := net.Listen("unix", stale)
	if err != nil {
		t.Skipf("unix sockets unavailable here: %v", err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	if _, err := os.Stat(stale); err != nil {
		t.Skipf("stale socket not kept on this platform: %v", err)
	}
	_, err = Connect(ctxT(t), Options{SocketPath: stale, Credentials: never})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("stale socket: err = %v, want ErrUnavailable", err)
	}
	if calls.Load() != 0 {
		t.Fatal("asked for a cookie although the API was not listening")
	}
}

func TestDefaultsAreInactiveOffMacOS(t *testing.T) {
	if DefaultSocketPath() == "" {
		_, err := Connect(ctxT(t), Options{Credentials: func(context.Context, string) (Credentials, error) {
			t.Fatal("asked for credentials off macOS")
			return Credentials{}, nil
		}})
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
		if _, err := AppleScriptCredentials(ctxT(t), "hopsesh"); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("AppleScriptCredentials off macOS: %v", err)
		}
	}
}

func TestAuthFailures(t *testing.T) {
	srv := startFake(t, true)
	// Credentials iTerm2 did not issue.
	bogus := func(context.Context, string) (Credentials, error) {
		return NewCredentials("deadbeef", "nope"), nil
	}
	_, err := Connect(ctxT(t), Options{SocketPath: srv.Path, Credentials: bogus})
	if !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("bogus cookie: err = %v, want ErrNotAuthorized", err)
	}
	if strings.Contains(err.Error(), "deadbeef") {
		t.Fatal("the error carries the cookie")
	}
	// The user declined macOS's Automation prompt.
	declined := func(context.Context, string) (Credentials, error) {
		return parseCookieOutput(nil, []byte("execution error: Not authorized to send Apple events to iTerm2. (-1743)"), errors.New("exit 1"))
	}
	before := len(srv.Headers())
	_, err = Connect(ctxT(t), Options{SocketPath: srv.Path, Credentials: declined})
	if !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("declined: err = %v", err)
	}
	if got := len(srv.Headers()) - before; got != 1 {
		t.Fatalf("handshakes after a declined prompt = %d, want only the probe", got)
	}
	// A cookie is single-use: replaying one fails.
	cookie, key := srv.Issue()
	once := func(context.Context, string) (Credentials, error) { return NewCredentials(cookie, key), nil }
	c, err := Connect(ctxT(t), Options{SocketPath: srv.Path, Credentials: once})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if _, err := Connect(ctxT(t), Options{SocketPath: srv.Path, Credentials: once}); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("replayed cookie: err = %v", err)
	}
}

func TestEnvironmentCredentialsAreUsedOnceAndRemoved(t *testing.T) {
	srv := startFake(t, true)
	cookie, key := srv.Issue()
	t.Setenv(envCookie, cookie)
	t.Setenv(envKey, key)
	var calls atomic.Int32
	c, err := Connect(ctxT(t), Options{SocketPath: srv.Path, Credentials: fakeCreds(srv, &calls)})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if calls.Load() != 0 {
		t.Fatal("asked AppleScript although iTerm2 supplied credentials")
	}
	if os.Getenv(envCookie) != "" || os.Getenv(envKey) != "" {
		t.Fatal("the cookie stayed in the environment, where children would inherit it")
	}
	if len(srv.Headers()) != 1 {
		t.Fatalf("handshakes = %d, want 1 (no probe needed)", len(srv.Headers()))
	}

	// A stale inherited cookie falls back to one AppleScript request.
	t.Setenv(envCookie, "stale")
	t.Setenv(envKey, "stale")
	c, err = Connect(ctxT(t), Options{SocketPath: srv.Path, Credentials: fakeCreds(srv, &calls)})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if calls.Load() != 1 {
		t.Fatalf("AppleScript requests = %d, want 1", calls.Load())
	}
}

func TestCredentialsNeverPrint(t *testing.T) {
	c := NewCredentials("s3cr3tc00kie", "s3cr3tkey")
	wrapped := struct {
		C Credentials
		P *Credentials
	}{c, &c}
	for _, f := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d"} {
		for _, v := range []any{c, &c, wrapped} {
			out := fmt.Sprintf(f, v)
			if strings.Contains(out, "s3cr3t") || strings.Contains(out, "733363") {
				t.Fatalf("%s of %T printed the secret: %s", f, v, out)
			}
		}
	}
}

func TestParseCookieOutput(t *testing.T) {
	c, err := parseCookieOutput([]byte("abc123 key-456\n"), nil, nil)
	if err != nil || c.cookie != "abc123" || c.key != "key-456" {
		t.Fatalf("got %v %v", c.cookie, err)
	}
	cases := []struct {
		stderr string
		want   error
	}{
		{"0:120: execution error: Not authorized to send Apple events to iTerm2. (-1743)", ErrNotAuthorized},
		{"execution error: iTerm2 got an error: Expected end of line. (-2741)", ErrTooOld},
		{"execution error: iTerm2 is not running (-600)", ErrUnavailable},
		{"", ErrUnavailable},
		{"execution error: something else (-1)", ErrNotAuthorized},
	}
	for _, tc := range cases {
		_, err := parseCookieOutput([]byte("leaked-cookie leaked-key"), []byte(tc.stderr), errors.New("exit status 1"))
		if !errors.Is(err, tc.want) {
			t.Errorf("%q: err = %v, want %v", tc.stderr, err, tc.want)
		}
		if strings.Contains(err.Error(), "leaked") {
			t.Errorf("%q: error carries osascript's output", tc.stderr)
		}
	}
	if _, err := parseCookieOutput(nil, nil, nil); !errors.Is(err, ErrUnavailable) {
		t.Errorf("empty reply (iTerm2 not running): %v", err)
	}
	for _, out := range []string{"onlyonepart", "a b c"} {
		if _, err := parseCookieOutput([]byte(out), nil, nil); !errors.Is(err, ErrNotAuthorized) {
			t.Errorf("%q: err = %v", out, err)
		}
	}
	if !strings.Contains(CookieScript("hopsesh"), `is not running then return ""`) {
		t.Error("the cookie script could launch iTerm2")
	}
	if _, err := AppleScriptCredentials(ctxT(t), `x" & do shell script "y`); err == nil {
		t.Error("an app name with quotes reached AppleScript")
	}
}

func TestScrubEnv(t *testing.T) {
	got := ScrubEnv([]string{"PATH=/bin", "ITERM2_COOKIE=x", "ITERM2_KEY=y", "ITERM_SESSION_ID=w0t0p0:1", "iterm2_cookie=z"})
	want := []string{"PATH=/bin", "ITERM_SESSION_ID=w0t0p0:1"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestOperations(t *testing.T) {
	srv := startFake(t, true)
	win, first := srv.AddWindow()
	c := connect(t, srv)
	ctx := ctxT(t)

	key, err := c.KeyWindow(ctx)
	if err != nil || key != win {
		t.Fatalf("KeyWindow = %q, %v; want %q", key, err, win)
	}
	cmd := QuoteArgv([]string{"/Applications/hopsesh.app/Contents/MacOS/hopsesh", "terminal-step", "t-1"})
	tab, err := c.OpenTab(ctx, win, Launch{Command: cmd, Dir: "/work/my repo"})
	if err != nil {
		t.Fatal(err)
	}
	if tab.WindowID != win || tab.SessionID == "" || tab.TabID == "" {
		t.Fatalf("OpenTab = %+v", tab)
	}
	props := srv.Props(tab.SessionID)
	want := map[string]string{
		"Custom Command":    `"Yes"`,
		"Command":           `"/Applications/hopsesh.app/Contents/MacOS/hopsesh terminal-step t-1"`,
		"Custom Directory":  `"Yes"`,
		"Working Directory": `"/work/my repo"`,
	}
	for k, v := range want {
		if props[k] != v {
			t.Errorf("prop %s = %s, want %s", k, props[k], v)
		}
	}
	if len(props) != len(want) {
		t.Errorf("props = %v", props)
	}

	newWin, err := c.OpenTab(ctx, "", Launch{Command: "true"})
	if err != nil || newWin.WindowID == win {
		t.Fatalf("OpenTab in a new window = %+v, %v", newWin, err)
	}

	split, err := c.OpenSplit(ctx, first, SplitRight, Launch{Command: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if w, _, ok := srv.Locate(split.SessionID); !ok || w != win {
		t.Fatalf("split went to %q", w)
	}
	if _, err := c.OpenSplit(ctx, "no-such", SplitBelow, Launch{}); err == nil {
		t.Fatal("split of a missing session succeeded")
	}

	l, err := c.ListSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(l.Sessions()); got != 4 {
		t.Fatalf("sessions = %v", l.Sessions())
	}
	if w, _, ok := l.Locate(split.SessionID); !ok || w != win {
		t.Fatalf("Locate(split) = %q %v", w, ok)
	}

	tty := srv.TTY(tab.SessionID)
	got, err := c.SessionTTY(ctx, tab.SessionID)
	if err != nil || got != tty {
		t.Fatalf("SessionTTY = %q, %v; want %q", got, err, tty)
	}
	for _, q := range []string{tty, strings.TrimPrefix(tty, "/dev/")} {
		loc, ok, err := c.FindTTY(ctx, q)
		if err != nil || !ok || loc.SessionID != tab.SessionID || loc.WindowID != win {
			t.Fatalf("FindTTY(%q) = %+v %v %v", q, loc, ok, err)
		}
	}
	if _, ok, err := c.FindTTY(ctx, "/dev/ttys999"); ok || err != nil {
		t.Fatalf("FindTTY of an unknown tty = %v %v", ok, err)
	}

	if err := c.Focus(ctx, tab.SessionID); err != nil {
		t.Fatal(err)
	}
	if a := srv.Activated(); len(a) != 1 || a[0] != tab.SessionID {
		t.Fatalf("activated = %v", a)
	}
	var re *RequestError
	if err := c.Focus(ctx, "gone"); !errors.As(err, &re) {
		t.Fatalf("Focus(gone) = %v", err)
	}

	if err := c.SetLabels(ctx, tab.SessionID, map[string]string{"title": "Fix\x1b]1337;evil\x07 parser\u202e"}); err == nil {
		t.Fatal("an unsanitised label was sent")
	}
	if err := c.SetLabels(ctx, tab.SessionID, map[string]string{"title": "Fix the parser", "machine": "laptop"}); err != nil {
		t.Fatal(err)
	}
	vars := srv.Vars(tab.SessionID)
	if vars["user.hopsesh_title"] != `"Fix the parser"` || vars["user.hopsesh_machine"] != `"laptop"` {
		t.Fatalf("vars = %v", vars)
	}
	if err := c.SetLabels(ctx, tab.SessionID, map[string]string{"../x": "y"}); err == nil {
		t.Fatal("a label name outside [a-z0-9_] was accepted")
	}

	if r := srv.Refused(); len(r) != 0 {
		t.Fatalf("the client sent requests outside its allowlist: %v", r)
	}
}

func TestNotifications(t *testing.T) {
	srv := startFake(t, true)
	_, s1 := srv.AddWindow()
	_, s2 := srv.AddWindow()
	c := connect(t, srv)
	ctx := ctxT(t)

	// Before Subscribe nothing is delivered (and nothing is queued for a reader that never
	// comes).
	srv.SendRaw(notificationMessage(7, s1))
	if _, err := c.ListSessions(ctx); err != nil { // the reader has now passed it
		t.Fatal(err)
	}

	if err := c.Subscribe(ctx); err != nil {
		t.Fatal(err)
	}
	for _, k := range []uint64{6, 7, 9} {
		if srv.Subscribers(k) != 1 {
			t.Fatalf("not subscribed to %d", k)
		}
	}
	// Notifications the client never asked for are skipped undecoded: a keystroke (1), a
	// screen update (2), a prompt (3), a custom escape sequence (5).
	for _, f := range []int{1, 2, 3, 5} {
		srv.SendRaw(notificationMessage(f, "secret typed text"))
	}
	srv.FocusSession(s2)
	srv.Terminate(s1)

	want := []Event{
		{Kind: FocusChanged, Focus: FocusChange{SessionID: s2}},
		{Kind: SessionTerminated, SessionID: s1},
	}
	for i, w := range want {
		select {
		case e := <-c.Events():
			if e.Kind != w.Kind || e.SessionID != w.SessionID || e.Focus.SessionID != w.Focus.SessionID {
				t.Fatalf("event %d = %+v, want %+v", i, e, w)
			}
		case <-ctx.Done():
			t.Fatalf("event %d never came", i)
		}
	}
	// The connection ending closes Events.
	srv.DropConnections()
	for range c.Events() {
	}
	<-c.Done()
	if c.Err() == nil {
		t.Error("Err is nil after iTerm2 dropped the connection")
	}
	if _, err := c.ListSessions(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("request after the drop: %v", err)
	}
}

func TestRequestRefusedAsMalformed(t *testing.T) {
	srv := startFake(t, false)
	c := connect(t, srv)
	_, err := c.OpenTab(ctxT(t), "", Launch{Profile: "x", Command: "true"})
	if err != nil {
		t.Fatal(err)
	}
	// The fake answers unknown windows with INVALID_WINDOW_ID.
	var re *RequestError
	if _, err := c.OpenTab(ctxT(t), "no-such-window", Launch{}); !errors.As(err, &re) || re.Status != 2 {
		t.Fatalf("err = %v", err)
	}
}

func TestMonitorReconnectsAndCatchesUp(t *testing.T) {
	srv := startFake(t, true)
	_, a := srv.AddWindow()
	_, b := srv.AddWindow()
	var calls atomic.Int32
	m := NewMonitor(MonitorOptions{
		Options:    Options{SocketPath: srv.Path, Credentials: fakeCreds(srv, &calls)},
		MinBackoff: 10 * time.Millisecond,
		MaxBackoff: 50 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(ctxT(t))
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()
	wa, wb := m.Watch(a), m.Watch(b)

	waitFor(t, func() bool { return m.Client() != nil })
	// iTerm2 goes away; b closes meanwhile; iTerm2 comes back.
	srv.Stop()
	srv.TerminateQuietly(b)
	waitFor(t, func() bool { return m.Client() == nil })
	time.Sleep(30 * time.Millisecond) // let a reconnection attempt fail
	if err := srv.Restart(); err != nil {
		t.Fatal(err)
	}

	var sawReconnect, sawB bool
	timeout := time.After(5 * time.Second)
	for !(sawReconnect && sawB) {
		select {
		case e := <-m.Events():
			switch {
			case e.Kind == Reconnected:
				sawReconnect = true
			case e.Kind == SessionTerminated && e.SessionID == b:
				sawB = true
			}
		case <-timeout:
			t.Fatalf("reconnected=%v b-terminated=%v", sawReconnect, sawB)
		}
	}
	select {
	case <-wb:
	default:
		t.Fatal("b's watch is still open after it vanished while disconnected")
	}
	select {
	case <-wa:
		t.Fatal("a reported terminated, but it is alive")
	default:
	}
	waitFor(t, func() bool { return srv.Subscribers(7) == 1 })
	srv.Terminate(a)
	select {
	case <-wa:
	case <-time.After(5 * time.Second):
		t.Fatal("a's termination was not seen")
	}
	// Watching an already-terminated session returns a closed channel.
	select {
	case <-m.Watch(a):
	default:
		t.Fatal("Watch after termination is open")
	}
	if calls.Load() < 2 {
		t.Fatalf("cookie requests = %d: each connection needs a fresh one", calls.Load())
	}
	cancel()
	go func() {
		for range m.Events() {
		}
	}()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
	if r := srv.Refused(); len(r) != 0 {
		t.Fatalf("refused requests: %v", r)
	}
}

func TestMonitorFallsBackAtOnce(t *testing.T) {
	t.Setenv(envCookie, "")
	dir, err := os.MkdirTemp("", "it2")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	m := NewMonitor(MonitorOptions{Options: Options{SocketPath: filepath.Join(dir, "socket")}})
	if err := m.Run(ctxT(t)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Run = %v, want ErrUnavailable", err)
	}
}

func TestMonitorStopsWhenConsentIsWithdrawn(t *testing.T) {
	srv := startFake(t, true)
	srv.AddWindow()
	var refuse atomic.Bool
	var calls atomic.Int32
	creds := func(ctx context.Context, app string) (Credentials, error) {
		calls.Add(1)
		if refuse.Load() {
			return Credentials{}, ErrNotAuthorized
		}
		return NewCredentials(srv.Issue()), nil
	}
	m := NewMonitor(MonitorOptions{
		Options:    Options{SocketPath: srv.Path, Credentials: creds},
		MinBackoff: 5 * time.Millisecond,
	})
	go func() {
		for range m.Events() {
		}
	}()
	done := make(chan error, 1)
	go func() { done <- m.Run(ctxT(t)) }()
	waitFor(t, func() bool { return m.Client() != nil })
	refuse.Store(true)
	srv.DropConnections()
	select {
	case err := <-done:
		if !errors.Is(err, ErrNotAuthorized) {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run kept retrying after consent was withdrawn")
	}
	if calls.Load() != 2 {
		t.Fatalf("credential requests = %d, want 2 (no retry loop on a refusal)", calls.Load())
	}
}

func TestQuoteArgvAndValidLabel(t *testing.T) {
	got := QuoteArgv([]string{"sh", "-c", "sleep 2; exit 3", "it's", ""})
	if got != `sh -c 'sleep 2; exit 3' 'it'\''s' ''` {
		t.Fatalf("QuoteArgv = %s", got)
	}
	for _, v := range []string{"Fix the parser", "é", strings.Repeat("a", 80), ""} {
		if !ValidLabel(v) {
			t.Errorf("ValidLabel(%q) = false", v)
		}
	}
	for _, v := range []string{"a\x1b]1337;x\x07", "a\x00", "a\u0085", "a\u202e", "a\u2066", "a\x7f", strings.Repeat("a", 81), "\xff"} {
		if ValidLabel(v) {
			t.Errorf("ValidLabel(%q) = true", v)
		}
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
