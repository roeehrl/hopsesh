// Command hopsesh-app is the hopsesh desktop app (macOS, Windows).
package main

import (
	"log"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/core/integrate"
	"github.com/roeehrl/hopsesh/internal/core/transport"
	"github.com/roeehrl/hopsesh/internal/ui/gui"
)

func main() {
	if transport.IsAskpass() {
		os.Exit(transport.AskpassMain(os.Args[1:])) // ssh asking for a password, see transport
	}
	reg := all.Registry()
	// Started from Finder, the app lacks the login shell's agent variables (for example
	// CLAUDE_CONFIG_DIR, CODEX_HOME); adopt them so scans, moves and the skill use the same
	// folders as the agents in Terminal.
	integrate.SetLoginVars(reg.LoginEnv())
	integrate.AdoptLoginEnv()
	svc := gui.NewApp(reg)
	app := application.New(application.Options{
		Name:        "hopsesh",
		Description: "Continue your coding agent sessions from your other machines, or in another agent",
		Services:    []application.Service{application.NewService(svc)},
		Assets:      application.AssetOptions{Handler: application.BundledAssetFileServer(gui.Assets)},
		Mac:         application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
		OnShutdown:  svc.Shutdown,
	})
	svc.Wails = app
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "hopsesh",
		Width:     1280,
		Height:    820,
		MinWidth:  900,
		MinHeight: 600,
		URL:       "/",
		Mac: application.MacWindow{
			TitleBar:                application.MacTitleBarHiddenInset,
			InvisibleTitleBarHeight: 44,
		},
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
