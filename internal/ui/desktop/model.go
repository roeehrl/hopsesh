// Package desktop owns the desktop shell lifecycle. It does not scan machines or
// launch agents; the GUI service supplies the shared session state and commands.
package desktop

import (
	"errors"

	"github.com/roeehrl/hopsesh/internal/config"
)

type Capabilities struct {
	Tray    bool   `json:"tray"`
	HideApp bool   `json:"hideApp"`
	Reason  string `json:"reason"`
}
type State struct {
	Preferences  config.Desktop `json:"preferences"`
	Capabilities Capabilities   `json:"capabilities"`
	Effective    string         `json:"effective"`
	Login        bool           `json:"login"`
	LoginError   string         `json:"loginError,omitempty"`
	Error        string         `json:"error,omitempty"`
}

func Effective(d config.Desktop, c Capabilities) string {
	mode := d.Placement()
	if !c.Tray {
		return "app"
	}
	if mode == "tray" && !c.HideApp {
		return "both"
	}
	return mode
}
func Validate(d config.Desktop, c Capabilities) error {
	if err := d.Check(); err != nil {
		return err
	}
	if d.Placement() != "app" && !c.Tray {
		return errors.New("no system tray is available; keep Hopsesh in the app launcher")
	}
	if d.Placement() == "tray" && !c.HideApp {
		return errors.New("this desktop cannot hide Hopsesh's taskbar entry; choose Both")
	}
	return nil
}

// KeepOnClose never hides the last access route on desktops without a Dock.
func KeepOnClose(d config.Desktop, c Capabilities, mac bool) bool {
	return d.Close == "keep" && (mac || Effective(d, c) != "app")
}
