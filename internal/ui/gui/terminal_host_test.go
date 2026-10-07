package gui

import (
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestTerminalModuleTypesIgnoreOSFileAssociations(t *testing.T) {
	for _, ext := range []string{".js", ".mjs", ".css"} {
		previous := mime.TypeByExtension(ext)
		t.Cleanup(func() { _ = mime.AddExtensionType(ext, previous) })
		if err := mime.AddExtensionType(ext, "application/octet-stream"); err != nil {
			t.Fatal(err)
		}
	}
	terms := NewTerminals("test")
	defer terms.CloseAll()
	raw, err := terms.hostURL(false)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	for file, want := range map[string]string{"window-start.js": "text/javascript", "vendor/xterm.mjs": "text/javascript", "terminal.css": "text/css"} {
		resp, err := http.Get(u.Scheme + "://" + u.Host + "/terminal/" + file)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), want) {
			t.Errorf("%s: status %d, type %s", file, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
	}
}

func TestEmbeddedRendererCannotInheritMainWindowBindings(t *testing.T) {
	terms := NewTerminals("test")
	terms.privilegedID(1)
	gate := terms.Gate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("frame reached app bindings") }))
	for _, origin := range []string{"null", "http://127.0.0.1:9876", "https://evil.test"} {
		r := httptest.NewRequest("POST", "http://wails.localhost/wails/runtime", nil)
		r.Header.Set("x-wails-window-id", "1")
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		gate.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("origin %q: %d", origin, w.Code)
		}
	}
}
func TestTerminalHostCapabilityAndAssetBoundary(t *testing.T) {
	terms := NewTerminals("test")
	defer terms.CloseAll()
	raw, err := terms.hostURL(false)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	for _, p := range []string{"/", "/app.js", "/wails/runtime", "/terminal/", "/stream?name=hopsesh.terminal"} {
		resp, err := http.Get(u.Scheme + "://" + u.Host + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Fatalf("%s: %d", p, resp.StatusCode)
		}
	}
	resp, err := http.Get(raw)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("authorized view %d", resp.StatusCode)
	}
	// Opaque sandbox origins must be able to load this listener's resources in
	// WebKitGTK without trusting another process's loopback HTTP/WS listener.
	policy := resp.Header.Get("Content-Security-Policy")
	origin := u.Scheme + "://" + u.Host
	for _, directive := range []string{"script-src " + origin + ";", "style-src " + origin + " 'unsafe-inline';", "connect-src " + origin + " ws://" + u.Host + ";", "object-src 'none'", "form-action 'none'"} {
		if !strings.Contains(policy, directive) {
			t.Errorf("policy missing %q: %s", directive, policy)
		}
	}
	if strings.Contains(policy, "'self'") || strings.Contains(policy, "'unsafe-eval'") {
		t.Fatalf("opaque origin or executable inline escape in policy: %s", policy)
	}
	_, _ = terms.hostURL(true)
	resp, err = http.Get(raw)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("old capability remained valid")
	}
}

func TestRehostCommitsOnlyAfterReplacementConnectsAndRollsBackFailure(t *testing.T) {
	terms := NewTerminals("test")
	defer terms.CloseAll()
	placement := "separate"
	terms.Prefs = func() TermPrefs { return TermPrefs{Placement: placement} }
	terms.SavePlacement = func(p string) error { placement = p; return nil }
	original, _ := terms.hostURL(false)
	terms.moveWorkspace("bottom")
	if placement != "separate" {
		t.Fatal("placement committed before renderer loaded")
	}
	terms.host.mu.Lock()
	next := terms.host.pending
	terms.host.mu.Unlock()
	terms.rollbackWorkspace(next, "separate")
	resp, err := http.Get(original)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal("failed load revoked original")
	}
	terms.moveWorkspace("bottom")
	terms.host.mu.Lock()
	next = terms.host.pending
	terms.host.mu.Unlock()
	if !terms.commitWorkspace(next) || placement != "bottom" {
		t.Fatal("ready renderer did not commit")
	}
	resp, err = http.Get(original)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("commit did not revoke original")
	}
}
