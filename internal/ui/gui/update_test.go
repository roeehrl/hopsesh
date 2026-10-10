package gui

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/internal/update"
	"github.com/roeehrl/hopsesh/internal/version"
)

func updateApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOPSESH_CONFIG_DIR", filepath.Join(dir, "config"))
	t.Setenv("HOPSESH_STATE_DIR", filepath.Join(dir, "state"))
	if err := os.MkdirAll(config.StateDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	oldVersion := version.Version
	version.Version = "0.4.0"
	t.Cleanup(func() { version.Version = oldVersion })
	reg, err := registry.New()
	if err != nil {
		t.Fatal(err)
	}
	a := NewApp(reg, WithoutSessionWatching())
	t.Cleanup(a.Shutdown)
	return a
}

func releaseServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	oldAPI := update.API
	update.API = s.URL
	t.Cleanup(func() { update.API = oldAPI })
}

func TestManualUpdateBypassesDailyCache(t *testing.T) {
	a := updateApp(t)
	if err := a.SetUpdateCheck(true); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	var latest atomic.Value
	latest.Store("0.4.0")
	releaseServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/"+update.Repo+"/releases/latest" {
			t.Errorf("unexpected release request: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		requests.Add(1)
		fmt.Fprintf(w, `{"tag_name":"v%s","html_url":"https://github.com/roeehrl/hopsesh/releases/tag/v%s"}`, latest.Load(), latest.Load())
	})
	check := func(manual bool, want string) {
		t.Helper()
		fn := a.CheckUpdate
		if manual {
			fn = a.LatestRelease
		}
		u, err := fn()
		if err != nil || u == nil || u.Latest != want || u.Newer != (want == "0.4.1") || u.URL != "https://github.com/roeehrl/hopsesh/releases/tag/v"+want {
			t.Fatalf("manual=%v: result=%+v, err=%v; want %s", manual, u, err, want)
		}
	}
	check(false, "0.4.0")
	stamp := filepath.Join(config.StateDir(), "update-check.json")
	cached, err := os.ReadFile(stamp)
	if err != nil {
		t.Fatal(err)
	}
	latest.Store("0.4.1")
	check(false, "0.4.0")
	check(true, "0.4.1")
	check(true, "0.4.1") // every manual request fetches again
	check(false, "0.4.0")
	if requests.Load() != 3 {
		t.Fatalf("daily check must stay cached; got %d upstream requests", requests.Load())
	}
	after, err := os.ReadFile(stamp)
	if err != nil || !bytes.Equal(after, cached) {
		t.Fatalf("manual checking changed the daily cache: %s, %v", after, err)
	}
}

func TestManualUpdateWithoutDailyOptIn(t *testing.T) {
	for _, preference := range []string{"", "off"} {
		t.Run("preference="+preference, func(t *testing.T) {
			a := updateApp(t)
			a.core.Cfg.UpdateCheck = preference
			var requests atomic.Int32
			releaseServer(t, func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				fmt.Fprint(w, `{"tag_name":"v0.4.1","html_url":"https://github.com/roeehrl/hopsesh/releases/tag/v0.4.1"}`)
			})
			if u, err := a.CheckUpdate(); err != nil || u == nil || u.Latest != "" || requests.Load() != 0 {
				t.Fatalf("daily check without opt-in: %+v, %v, requests=%d", u, err, requests.Load())
			}
			if u, err := a.LatestRelease(); err != nil || u == nil || u.Latest != "0.4.1" || !u.Newer || requests.Load() != 1 {
				t.Fatalf("manual check without opt-in: %+v, %v, requests=%d", u, err, requests.Load())
			}
			if a.core.Cfg.UpdateCheck != preference {
				t.Fatal("manual checking changed the daily preference")
			}
			if _, err := os.Stat(filepath.Join(config.StateDir(), "update-check.json")); !os.IsNotExist(err) {
				t.Fatalf("manual checking created a daily cache: %v", err)
			}
		})
	}
}

func TestManualUpdateReportsFailure(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusNotFound, 0} {
		t.Run(fmt.Sprintf("status=%d", status), func(t *testing.T) {
			a := updateApp(t)
			if err := a.SetUpdateCheck(true); err != nil {
				t.Fatal(err)
			}
			releaseServer(t, func(w http.ResponseWriter, _ *http.Request) {
				if status == 0 { // offline: the connection closes without a response
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
					return
				}
				w.WriteHeader(status)
			})
			if u, err := a.LatestRelease(); u != nil || err == nil || !strings.Contains(err.Error(), "could not check for updates:") {
				t.Fatalf("manual failure must be explicit: %+v, %v", u, err)
			}
			if u, err := a.CheckUpdate(); err != nil || u == nil || u.Latest != "" || u.Newer {
				t.Fatalf("daily failure must stay quiet: %+v, %v", u, err)
			}
			if _, err := os.Stat(filepath.Join(config.StateDir(), "update-check.json")); !os.IsNotExist(err) {
				t.Fatalf("failed checking created a daily cache: %v", err)
			}
		})
	}
}
