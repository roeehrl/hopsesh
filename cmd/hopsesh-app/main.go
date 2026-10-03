// Command hopsesh-app is the hopsesh desktop app (macOS, Windows).
package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/integrate"
	"github.com/roeehrl/hopsesh/internal/core/transport"
	"github.com/roeehrl/hopsesh/internal/ui/gui"
	"github.com/roeehrl/hopsesh/internal/update"
)

func main() {
	if transport.IsAskpass() {
		os.Exit(transport.AskpassMain(os.Args[1:])) // ssh asking for a password, see transport
	}
	update.CleanUp() // what an earlier update moved aside
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
		Windows: application.WindowsOptions{
			WebviewUserDataPath:   filepath.Join(config.StateDir(), "webview"),
			AdditionalBrowserArgs: testBrowserArgs(),
		},
		OnShutdown: svc.Shutdown,
	})
	svc.Wails = app
	app.Menu.Set(menu(func(cmd string) { app.Event.Emit(gui.MenuEvent, cmd) }))
	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
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
	testHook(win)
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// menu is the menu bar: the standard App, Edit and Window menus, and a Session menu whose
// commands the window carries out (it gets each as a gui.MenuEvent).
func menu(send func(cmd string)) *application.Menu {
	m := application.NewMenu()
	m.AddRole(application.AppMenu)
	m.AddRole(application.EditMenu)
	s := m.AddSubmenu("Session")
	item := func(label, accel, cmd string) {
		it := s.Add(label).OnClick(func(*application.Context) { send(cmd) })
		if accel != "" {
			it.SetAccelerator(accel)
		}
	}
	item("Find…", "CmdOrCtrl+K", "palette")
	item("Refresh", "CmdOrCtrl+R", "refresh")
	s.AddSeparator()
	item("Sessions", "CmdOrCtrl+1", "sessions")
	item("Activity", "CmdOrCtrl+2", "activity")
	item("Machines", "CmdOrCtrl+3", "machines")
	item("Settings…", "CmdOrCtrl+,", "settings")
	s.AddSeparator()
	item("Undo Last Move", "CmdOrCtrl+Alt+Z", "undo-last")
	m.AddRole(application.WindowMenu)
	return m
}
