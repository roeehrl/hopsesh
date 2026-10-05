package gui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The terminal window reaches its page, the Wails runtime script and the two stream
// endpoints, and nothing else: no bound method (/wails/runtime), no other page, no event
// payload. The app's own window reaches everything. A request no window is tagged on is
// treated as the terminal's.
func TestTerminalGate(t *testing.T) {
	terms := NewTerminals("test")
	terms.Privileged(1)
	reached := ""
	gate := terms.Gate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = r.Method + " " + r.URL.Path }))
	do := func(window, method, path string) (int, http.Header) {
		reached = ""
		r := httptest.NewRequest(method, "http://wails.localhost"+path, strings.NewReader("{}"))
		if window != "" {
			r.Header.Set("x-wails-window-id", window)
		}
		w := httptest.NewRecorder()
		gate.ServeHTTP(w, r)
		if w.Code == http.StatusOK && reached != method+" "+path {
			t.Errorf("%s %s from %q passed but reached %q", method, path, window, reached)
		}
		return w.Code, w.Header()
	}
	for _, path := range []string{"/", "/index.html", "/app.js", "/wails/runtime", "/wails/event-payload/0123", "/terminal/"} {
		for _, m := range []string{http.MethodGet, http.MethodPost} {
			if code, _ := do("1", m, path); code != http.StatusOK {
				t.Errorf("the app's window: %s %s = %d", m, path, code)
			}
		}
	}
	for _, window := range []string{"2", "", "junk"} {
		allowed := map[string]string{
			"/terminal/":          http.MethodGet,
			"/terminal/dev.js":    http.MethodGet,
			"/wails/runtime.js":   http.MethodGet,
			"/wails/transport.js": http.MethodGet,
			"/wails/custom.js":    http.MethodHead,
			"/wails/stream/poll":  http.MethodGet,
			"/wails/stream/send":  http.MethodPost,
		}
		for path, m := range allowed {
			code, h := do(window, m, path)
			if code != http.StatusOK {
				t.Errorf("terminal window %q: %s %s = %d", window, m, path, code)
			}
			if strings.HasPrefix(path, "/terminal/") && !strings.Contains(h.Get("Content-Security-Policy"), "script-src 'self'") {
				t.Errorf("no CSP on %s", path)
			}
		}
		for _, req := range [][2]string{
			{http.MethodPost, "/wails/runtime"}, {http.MethodGet, "/wails/runtime"}, {http.MethodGet, "/"}, {http.MethodGet, "/index.html"},
			{http.MethodGet, "/app.js"}, {http.MethodGet, "/wails/event-payload/0123456789abcdef0123456789abcdef"},
			{http.MethodPost, "/terminal/"}, {http.MethodGet, "/wails/stream/send"}, {http.MethodPost, "/wails/stream/poll"},
			{http.MethodGet, "/terminal/../index.html"}, {http.MethodDelete, "/wails/stream/send"},
		} {
			if code, _ := do(window, req[0], req[1]); code != http.StatusForbidden {
				t.Errorf("terminal window %q: %s %s = %d, want 403", window, req[0], req[1], code)
			}
		}
	}
}

// The window's terminal calls work without a terminal window, and closing an unknown tab
// is refused.
func TestTerminalBindings(t *testing.T) {
	a := &App{Terms: NewTerminals("test")}
	defer a.Terms.CloseAll()
	if tabs := a.TerminalTabs(); tabs == nil || len(tabs) != 0 {
		t.Fatalf("tabs %v", tabs)
	}
	if a.TerminalRunning() != 0 {
		t.Fatal("running")
	}
	a.TerminalShow() // no app: nothing to show
	if err := a.TerminalCloseTab("0123456789abcdef"); err == nil {
		t.Fatal("closed a tab that is not there")
	}
}
