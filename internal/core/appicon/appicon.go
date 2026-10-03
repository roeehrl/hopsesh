// Package appicon reads the icon of an agent's installed desktop app on this machine, so
// the window can picture the agent the way a launcher does, without shipping a logo.
package appicon

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrNone means none of the app's places holds an app with a readable icon.
var ErrNone = errors.New("no installed app icon")

// Find returns the icon of the first installed app among apps (by GOOS, "~" the home
// folder) as a data URL, or ErrNone.
func Find(apps map[string][]string, home string) (string, error) {
	for _, p := range apps[runtime.GOOS] {
		if rest, ok := strings.CutPrefix(p, "~/"); ok {
			p = filepath.Join(home, filepath.FromSlash(rest))
		}
		if _, err := os.Stat(p); err != nil {
			continue
		}
		png, err := iconOf(p)
		if err != nil || len(png) == 0 {
			continue
		}
		return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
	}
	return "", ErrNone
}
