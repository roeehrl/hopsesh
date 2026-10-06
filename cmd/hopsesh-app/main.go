// Command hopsesh-app is the hopsesh desktop app (macOS, Windows).
package main

import (
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

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
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(gui.Assets),
			// The terminal window may reach its page and its streams only.
			Middleware: application.ChainMiddleware(svc.Terms.Gate, testAssets),
		},
		Mac: application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false},
		// With programs running in terminal tabs, the window asks before quitting.
		ShouldQuit:     svc.ShouldQuit,
		SingleInstance: &application.SingleInstanceOptions{UniqueID: fmt.Sprintf("hopsesh-%x", sha256.Sum256([]byte(config.Dir()))), OnSecondInstanceLaunch: func(application.SecondInstanceData) { _ = svc.QuickOpen("sessions", "", "") }},
		Windows: application.WindowsOptions{
			WebviewUserDataPath:   filepath.Join(config.StateDir(), "webview"),
			AdditionalBrowserArgs: testBrowserArgs(),
		},
		OnShutdown: svc.Shutdown,
	})
	svc.Wails = app
	svc.Terms.Attach(app)
	app.Menu.Set(menu(func(cmd string) { app.Event.Emit(gui.MenuEvent, cmd) }, svc.Terms.Toggle))
	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "hopsesh",
		Width:     1280,
		Height:    820,
		MinWidth:  900,
		MinHeight: 600,
		URL:       "/",
		// The header is 52px, as tall as AppKit's own unified toolbar, so the window's buttons
		// sit on its middle (style.css).
		Mac: application.MacWindow{
			TitleBar:                application.MacTitleBarHiddenInset,
			InvisibleTitleBarHeight: 52,
		},
	})
	svc.Terms.Privileged(win) // the app's own window: the one with its bindings
	configureTitlebar(win)
	svc.AttachDesktop(win)
	// Desktop presence owns close behavior. Quit still confirms running terminal
	// programs; canceling it must leave the main window available.
	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if cancel, hide := svc.MainClosing(); cancel {
			e.Cancel()
			if hide {
				win.Hide()
			}
		} else {
			e.Cancel()
			go app.Quit()
		}
	})
	// macOS: clicking the Dock icon brings a hidden window back.
	app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) { win.Show().Focus() })
	testHook(win, svc)
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// menu is the menu bar: the standard App, Edit and Window menus, a View menu for the
// panes and the session list's display, and a Session menu whose commands the window
// carries out (it gets each as a gui.MenuEvent); Terminal (Ctrl+`) moves between the app's
// window and the hopsesh Terminal window.
func menu(send func(cmd string), terminal func()) *application.Menu {
	m := application.NewMenu()
	m.AddRole(application.AppMenu)
	m.AddRole(application.EditMenu)
	// View: the Sessions screen's panes (each title says what it does now; the window
	// tells, gui.SetViewMenu), and full screen on macOS.
	v := m.AddSubmenu("View")
	sidebarKey, inspectorKey := "Ctrl+B", "Ctrl+I"
	if runtime.GOOS == "darwin" {
		sidebarKey, inspectorKey = "Ctrl+Cmd+S", "Alt+Cmd+I"
	}
	sidebar := v.Add("Hide Sidebar").SetAccelerator(sidebarKey).OnClick(func(*application.Context) { send("toggle-sidebar") })
	inspector := v.Add("Hide Inspector").SetAccelerator(inspectorKey).OnClick(func(*application.Context) { send("toggle-inspector") })
	gui.SetViewMenu(func(sb, in bool) {
		sidebar.SetLabel(map[bool]string{true: "Hide Sidebar", false: "Show Sidebar"}[sb])
		inspector.SetLabel(map[bool]string{true: "Hide Inspector", false: "Show Inspector"}[in])
	})
	// The session list: Group By and Sort By (radio items; the window tells which is
	// chosen, gui.SetListMenu), collapsing every group, compact rows and the Display popover.
	v.AddSeparator()
	groups := radios(v.AddSubmenu("Group By"), [][2]string{{"repository", "Repository"}, {"location", "Location"}, {"agent", "Agent"},
		{"status", "Status"}, {"last-active", "Last Active"}, {"none", "None"}}, "group:", send)
	sorts := radios(v.AddSubmenu("Sort By"), [][2]string{{"last-active", "Last Active"}, {"title", "Title"}, {"status", "Status"}, {"size", "Size"}}, "sort:", send)
	v.Add("Collapse All Groups").OnClick(func(*application.Context) { send("collapse-all") })
	v.Add("Expand All Groups").OnClick(func(*application.Context) { send("expand-all") })
	compact := v.AddCheckbox("Compact Rows", false).OnClick(func(*application.Context) { send("compact") })
	v.Add("Show Display Options").SetAccelerator("CmdOrCtrl+J").OnClick(func(*application.Context) { send("display") })
	gui.SetListMenu(func(groupBy, sortBy string, c bool) {
		for id, it := range groups {
			it.SetChecked(id == groupBy)
		}
		for id, it := range sorts {
			it.SetChecked(id == sortBy)
		}
		compact.SetChecked(c)
	})
	if runtime.GOOS == "darwin" {
		v.AddSeparator()
		v.AddRole(application.ToggleFullscreen)
	}
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
	s.AddSeparator()
	s.Add("Terminal").SetAccelerator("Ctrl+`").OnClick(func(*application.Context) { terminal() })
	m.AddRole(application.WindowMenu)
	return m
}

// radios fills a submenu with one radio item per choice ({id, label}); picking one sends
// prefix+id. The items are returned by id, for the window to check the chosen one.
func radios(sub *application.Menu, choices [][2]string, prefix string, send func(cmd string)) map[string]*application.MenuItem {
	out := map[string]*application.MenuItem{}
	for i, c := range choices {
		id := c[0]
		out[id] = sub.AddRadio(c[1], i == 0).OnClick(func(*application.Context) { send(prefix + id) })
	}
	return out
}
