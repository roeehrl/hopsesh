package gui

import "github.com/roeehrl/hopsesh/internal/config"

// The window's panes: the sidebar and the inspector of the Sessions screen, resizable and
// hideable, saved in the config ([window]) and shown in the View menu.

// LayoutDTO is the panes' widths (CSS pixels) and whether the user hid them, with the
// limits the window keeps to.
type LayoutDTO struct {
	SidebarWidth    int  `json:"sidebarWidth"`
	SidebarHidden   bool `json:"sidebarHidden"`
	InspectorWidth  int  `json:"inspectorWidth"`
	InspectorHidden bool `json:"inspectorHidden"`
}

func layoutOf(w config.Window) LayoutDTO {
	d := LayoutDTO{SidebarWidth: w.SidebarWidth, SidebarHidden: w.SidebarHidden, InspectorWidth: w.InspectorWidth, InspectorHidden: w.InspectorHidden}
	if d.SidebarWidth == 0 {
		d.SidebarWidth = config.SidebarWidth
	}
	if d.InspectorWidth == 0 {
		d.InspectorWidth = config.InspectorWidth
	}
	return d
}

// SaveLayout stores the panes' layout (what the user chose: never a pane the window hid
// for being narrow).
func (a *App) SaveLayout(l LayoutDTO) error {
	w := config.Window{SidebarWidth: min(max(l.SidebarWidth, config.SidebarMin), config.SidebarMax), SidebarHidden: l.SidebarHidden,
		InspectorWidth: min(max(l.InspectorWidth, config.InspectorMin), config.InspectorMax), InspectorHidden: l.InspectorHidden}
	if w.SidebarWidth == config.SidebarWidth {
		w.SidebarWidth = 0
	}
	if w.InspectorWidth == config.InspectorWidth {
		w.InspectorWidth = 0
	}
	a.mu.Lock()
	changed := a.core.Cfg.Window != w
	a.core.Cfg.Window = w
	var err error
	if changed {
		err = a.save()
	}
	a.mu.Unlock()
	return err
}

// ViewState tells the View menu what the window shows now (sidebar, inspector), so its
// items say Show or Hide; a pane hidden for a narrow window counts as hidden.
func (a *App) ViewState(sidebar, inspector bool) {
	if f := viewMenu; f != nil {
		f(sidebar, inspector)
	}
}

var viewMenu func(sidebar, inspector bool)

// SetViewMenu is how the app's View menu learns what the window shows.
func SetViewMenu(f func(sidebar, inspector bool)) { viewMenu = f }
