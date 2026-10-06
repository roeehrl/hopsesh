package main

/*
#cgo LDFLAGS: -framework Cocoa
double hopseshTrafficLightsEnd(void *window);
*/
import "C"

import (
	"fmt"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Measure AppKit's actual controls instead of depending on one macOS version's
// traffic-light spacing. CSS keeps a fallback until the native window is ready.
func configureTitlebar(w *application.WebviewWindow) {
	w.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
		end := application.InvokeSyncWithResult(func() float64 {
			return float64(C.hopseshTrafficLightsEnd(w.NativeWindow()))
		})
		if end > 0 && end < 200 {
			w.ExecJS(fmt.Sprintf("document.documentElement.style.setProperty('--tl-end', '%.2fpx')", end))
		}
	})
}
