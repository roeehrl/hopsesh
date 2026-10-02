// Command hopsesh-app is the hopsesh desktop app (macOS, Windows).
package main

import (
	"log"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/roeehrl/hopsesh/internal/core/transport"
	"github.com/roeehrl/hopsesh/internal/gui"
)

func main() {
	if transport.IsAskpass() {
		os.Exit(transport.AskpassMain(os.Args[1:])) // ssh asking for a password, see transport
	}
	svc, err := gui.NewApp()
	if err != nil {
		log.Fatal(err)
	}
	app := application.New(application.Options{
		Name:        "hopsesh",
		Description: "Continue your Claude Code sessions from your other machines here",
		Services:    []application.Service{application.NewService(svc)},
		Assets:      application.AssetOptions{Handler: application.BundledAssetFileServer(gui.Assets)},
		Mac:         application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
		OnShutdown:  svc.Shutdown,
	})
	svc.App = app
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
