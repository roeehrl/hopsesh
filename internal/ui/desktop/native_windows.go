package desktop

import (
	"fmt"
	"os"
	"slices"

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows"
)

var user32 = windows.NewLazySystemDLL("user32.dll")
var getStyle = user32.NewProc("GetWindowLongPtrW")
var setStyle = user32.NewProc("SetWindowLongPtrW")
var setPos = user32.NewProc("SetWindowPos")

func capabilities() Capabilities { return Capabilities{Tray: true, HideApp: true} }
func setAppHidden(w *application.WebviewWindow, hidden bool) error {
	// Windows requires hiding and showing a visible window when changing taskbar styles.
	visible := w.IsVisible()
	if visible {
		w.Hide()
		defer w.Show()
	}
	var err error
	application.InvokeSync(func() {
		hwnd := uintptr(w.NativeWindow())
		if hwnd == 0 {
			err = fmt.Errorf("main window is not ready")
			return
		}
		idx := int32(-20)
		style, _, _ := getStyle.Call(hwnd, uintptr(idx))
		next := style
		if hidden {
			next = (style &^ 0x40000) | 0x80
		} else {
			next = (style &^ 0x80) | 0x40000
		}
		if next == style {
			return
		}
		_, _, callErr := setStyle.Call(hwnd, uintptr(idx), next)
		actual, _, _ := getStyle.Call(hwnd, uintptr(idx))
		if actual != next {
			err = fmt.Errorf("change taskbar visibility: %v", callErr)
			return
		}
		setPos.Call(hwnd, 0, 0, 0, 0, 0, 0x0027) // frame changed, no move/size/z-order
	})
	return err
}
func loginLaunch() bool { return slices.Contains(os.Args, "--background") }
func loginOptions() application.AutostartOptions {
	return application.AutostartOptions{Arguments: []string{"--background"}}
}
