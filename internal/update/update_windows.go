package update

import (
	"context"
	"errors"

	"golang.org/x/sys/windows/registry"
)

func installMacApp(context.Context, string, []byte, bool) error { return errors.New("not macOS") }

// setInstalledVersion keeps the version Windows lists for the installed app current (the
// installer's uninstall entry), so it does not look outdated after an update.
func setInstalledVersion(version string) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Uninstall\hopsesh`, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	_ = k.SetStringValue("DisplayVersion", version)
}
