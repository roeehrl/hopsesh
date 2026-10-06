package desktop

import (
	"fmt"
	"testing"

	"github.com/roeehrl/hopsesh/internal/config"
)

// Run in the existing Linux/macOS/Windows CI matrix: capability scenarios are
// deterministic even when the runner itself has no interactive shell.
func TestDesktopLifecycleMatrix(t *testing.T) {
	for _, c := range []struct {
		name string
		caps Capabilities
		mac  bool
	}{
		{"macOS", Capabilities{Tray: true, HideApp: true}, true},
		{"Windows", Capabilities{Tray: true, HideApp: true}, false},
		{"Linux-tray", Capabilities{Tray: true}, false},
		{"Linux-no-host", Capabilities{}, false},
	} {
		for _, mode := range []string{"app", "tray", "both"} {
			for _, close := range []string{"keep", "quit"} {
				t.Run(fmt.Sprintf("%s/%s/%s", c.name, mode, close), func(t *testing.T) {
					p := config.Desktop{Mode: mode, Close: close}
					effective := Effective(p, c.caps)
					if effective == "tray" && (!c.caps.Tray || !c.caps.HideApp) {
						t.Fatal("unrecoverable tray-only state")
					}
					valid := mode == "app" || c.caps.Tray && (mode != "tray" || c.caps.HideApp)
					if (Validate(p, c.caps) == nil) != valid {
						t.Fatal("capability validation disagrees with mode")
					}
					keep := KeepOnClose(p, c.caps, c.mac)
					if close == "quit" && keep {
						t.Fatal("quit preference ignored")
					}
					if !c.mac && effective == "app" && keep {
						t.Fatal("hides last access route")
					}
					if close == "keep" && c.mac && !keep {
						t.Fatal("Dock cannot reopen closed window")
					}
				})
			}
		}
	}
}
func TestHostLossRecovery(t *testing.T) {
	p := config.Desktop{Mode: "tray", Close: "keep"}
	for _, c := range []Capabilities{{Tray: true, HideApp: true}, {}, {Tray: true, HideApp: true}} {
		effective := Effective(p, c)
		if !c.Tray && effective != "app" {
			t.Fatal("host loss did not restore app")
		}
		if c.Tray && effective != "tray" {
			t.Fatal("host recovery lost saved preference")
		}
	}
}
