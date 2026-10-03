//go:build e2e

package main

import (
	"os"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
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

// testHook runs the script in HOPSESH_E2E_SCRIPT in the window once its runtime is ready
// (the self-check on macOS and Linux, where there is no debugging port). Test builds only.
func testHook(w *application.WebviewWindow) {
	p := os.Getenv("HOPSESH_E2E_SCRIPT")
	if p == "" {
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var once sync.Once
	w.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
		once.Do(func() { w.ExecJS(string(b)) })
	})
}
