package gui

import (
	"slices"

	"github.com/roeehrl/hopsesh/internal/config"
)

// The window's panes: the sidebar and the inspector of the Sessions screen, resizable and
// hideable, saved in the config ([window]) and shown in the View menu, and the inspector's
// sections the user opened or closed.

// LayoutDTO is the panes' widths (CSS pixels; the inspector's 0 is its default, which
// follows the window's width) and whether the user hid them, and the inspector's sections
// the user opened or closed.
type LayoutDTO struct {
	SidebarWidth    int      `json:"sidebarWidth"`
	SidebarHidden   bool     `json:"sidebarHidden"`
	InspectorWidth  int      `json:"inspectorWidth"`
	InspectorHidden bool     `json:"inspectorHidden"`
	SectionsOpen    []string `json:"sectionsOpen"`
	SectionsClosed  []string `json:"sectionsClosed"`
}

func layoutOf(w config.Window, in config.Inspector) LayoutDTO {
	d := LayoutDTO{SidebarWidth: w.SidebarWidth, SidebarHidden: w.SidebarHidden, InspectorWidth: w.InspectorWidth, InspectorHidden: w.InspectorHidden,
		SectionsOpen: nonNil(in.Open), SectionsClosed: nonNil(in.Closed)}
	if d.SidebarWidth == 0 {
		d.SidebarWidth = config.SidebarWidth
	}
	return d
}

// SaveLayout stores the panes' layout (what the user chose: never a pane the window hid
// for being narrow) and the inspector's sections.
func (a *App) SaveLayout(l LayoutDTO) error {
	w := config.Window{SidebarWidth: min(max(l.SidebarWidth, config.SidebarMin), config.SidebarMax), SidebarHidden: l.SidebarHidden,
		InspectorHidden: l.InspectorHidden}
	in := config.Inspector{Open: known(l.SectionsOpen, config.InspectorSections), Closed: known(l.SectionsClosed, config.InspectorSections)}
	if l.InspectorWidth > 0 {
		w.InspectorWidth = min(max(l.InspectorWidth, config.InspectorMin), config.InspectorMax)
	}
	if w.SidebarWidth == config.SidebarWidth {
		w.SidebarWidth = 0
	}
	a.mu.Lock()
	old := a.core.Cfg.Inspector
	changed := a.core.Cfg.Window != w || !slices.Equal(old.Open, in.Open) || !slices.Equal(old.Closed, in.Closed)
	a.core.Cfg.Window, a.core.Cfg.Inspector = w, in
	var err error
	if changed {
		err = a.save()
	}
	a.mu.Unlock()
	return err
}

// known keeps the values that are one of allowed, once each, in their order (nil when
// none).
func known(vals, allowed []string) []string {
	var out []string
	for _, v := range vals {
		if slices.Contains(allowed, v) && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
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
