package gui

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const AppearanceEvent = "hopsesh:appearance"

// Appearance is a cheap startup read, independent of agent/integration discovery.
func (a *App) Appearance() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.core.Cfg.AppearanceMode()
}

func (a *App) publishAppearance() {
	mode := a.Appearance()
	if a.Wails != nil {
		nativeAppearance(mode)
	}
	a.emit(AppearanceEvent, mode)
	if a.Terms != nil {
		a.Terms.Refresh()
	}
}

// AttachAppearance applies the saved native preference once AppKit is running.
// System removes the override; subsequent OS changes need no saved config writes.
func (a *App) AttachAppearance() {
	a.Wails.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		nativeAppearance(a.Appearance())
	})
}
