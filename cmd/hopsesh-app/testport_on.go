//go:build e2e

package main

import "os"

// testBrowserArgs opens WebView2's debugging port for the end-to-end tests, which drive
// the real window with Playwright. Test builds only (-tags e2e): the port has no
// authentication.
func testBrowserArgs() []string {
	if p := os.Getenv("HOPSESH_E2E_CDP_PORT"); p != "" {
		return []string{"--remote-debugging-port=" + p}
	}
	return nil
}
