//go:build e2e

package gui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// devTerminal is a test page for the terminal's streams (devterm/), in test builds only
// (-tags e2e): the app's own terminal page comes with its UI.
//
//go:embed devterm
var devTerminal embed.FS

// DevTerminalAssets serves the test page at TerminalPage (test builds only). It goes
// inside Terminals.Gate, which still decides who may fetch it.
func DevTerminalAssets(next http.Handler) http.Handler {
	sub, err := fs.Sub(devTerminal, "devterm")
	if err != nil {
		panic(err)
	}
	files := http.StripPrefix(strings.TrimSuffix(TerminalPage, "/"), http.FileServer(http.FS(sub)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, TerminalPage) {
			files.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
