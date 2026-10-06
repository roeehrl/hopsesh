package desktop

import (
	"context"
	"errors"
	"os"
	"slices"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func capabilities() Capabilities {
	c := Capabilities{Reason: "No system tray detected. Hopsesh stays accessible in the app launcher."}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return c
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Wails registers against this specific watcher; a different service alone
	// cannot provide a working icon for this backend.
	for _, name := range []string{"org.kde.StatusNotifierWatcher"} {
		var v dbus.Variant
		err = conn.Object(name, "/StatusNotifierWatcher").CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, "org.kde.StatusNotifierWatcher", "IsStatusNotifierHostRegistered").Store(&v)
		if err == nil && v.Value() == true {
			c.Tray = true
			break
		}
	}
	// GTK4 does not expose a portable skip-taskbar control. Do not infer support
	// merely from XDG_CURRENT_DESKTOP or an X11 DISPLAY variable under Wayland.
	if c.Tray {
		c.Reason = "This desktop supports Quick access, but cannot hide the main app's taskbar entry. Choose Both."
	}
	return c
}
func setAppHidden(_ *application.WebviewWindow, hidden bool) error {
	if hidden {
		return errors.New("tray-only mode is unavailable on this desktop")
	}
	return nil
}
func loginLaunch() bool { return slices.Contains(os.Args, "--background") }
func loginOptions() application.AutostartOptions {
	return application.AutostartOptions{Arguments: []string{"--background"}}
}
