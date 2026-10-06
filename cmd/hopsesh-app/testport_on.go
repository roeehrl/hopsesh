//go:build e2e

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/roeehrl/hopsesh/internal/core/pty"
	"github.com/roeehrl/hopsesh/internal/ui/gui"
)

// testBrowserArgs opens WebView2's debugging port for the end-to-end tests, which drive
// the real window with Playwright. Test builds only (-tags e2e): the port has no
// authentication.
func testBrowserArgs() []string {
	if p := os.Getenv("HOPSESH_E2E_CDP_PORT"); p != "" {
		return []string{"--remote-debugging-port=" + p}
	}
	return nil
}

// testAssets: test builds serve the same assets (the terminal check drives the real
// terminal page).
func testAssets(next http.Handler) http.Handler { return next }

// testHook runs the script in HOPSESH_E2E_SCRIPT in the window once its runtime is ready
// (the self-check on macOS and Linux, where there is no debugging port), and opens the
// terminal check in HOPSESH_E2E_TERMINAL. Test builds only.
func testHook(w *application.WebviewWindow, svc *gui.App) {
	if path := os.Getenv("HOPSESH_E2E_QUICK_SCRIPT"); path != "" {
		if quick, ok := svc.Wails.Window.GetByName("quick-access"); ok {
			var quickOnce sync.Once
			quick.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
				quickOnce.Do(func() {
					if b, err := os.ReadFile(path); err == nil {
						quick.ExecJS(string(b))
					}
				})
			})
		}
	}
	var once sync.Once
	script := os.Getenv("HOPSESH_E2E_SCRIPT")
	argv := os.Getenv("HOPSESH_E2E_TERMINAL")
	if script == "" && argv == "" {
		return
	}
	w.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
		once.Do(func() {
			if script != "" {
				if b, err := os.ReadFile(script); err == nil {
					w.ExecJS(string(b))
				}
			}
			if argv != "" {
				go terminalCheck(svc, argv)
			}
		})
	})
}

// terminalCheck runs the program in HOPSESH_E2E_TERMINAL (a JSON argument list) in a
// terminal tab, shown in the real hopsesh Terminal window, whose emulator (xterm.js)
// answers the program's DA1 query. Once the program ends it writes the tab's
// backend, the exit code and what the program printed to HOPSESH_E2E_TERMINAL_OUT, and
// quits the app.
func terminalCheck(svc *gui.App, argv string) {
	report := func(format string, a ...any) {
		if p := os.Getenv("HOPSESH_E2E_TERMINAL_OUT"); p != "" {
			_ = os.WriteFile(p, fmt.Appendf(nil, format, a...), 0o600)
		}
		svc.Wails.Quit()
	}
	var args []string
	if err := json.Unmarshal([]byte(argv), &args); err != nil {
		report("error: HOPSESH_E2E_TERMINAL: %v\n", err)
		return
	}
	dir, _ := os.Getwd()
	info, err := svc.Terms.Open(pty.Spec{Argv: args, Dir: dir, Title: "terminal check", Capture: pty.CaptureStep},
		gui.TabSetup{Meta: gui.TabMeta{Kind: gui.TabSession, Command: "terminal check"}})
	if err != nil {
		report("error: %v\n", err)
		return
	}
	s, _ := svc.Terms.Manager().Get(info.ID)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	code, err := s.Wait(ctx)
	if err != nil {
		report("error: the program did not end: %v\n", err)
		return
	}
	out, _ := s.StepOutput()
	report("backend=%s code=%d\n%s", s.Info().Backend, code, out.Text)
}
