package gui

import (
	"context"
	"encoding/json"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/presence"
)

const DiscoveryEvent = "hopsesh:discovery"

// CachedScan has no agent, SSH, git, or presence probes. A cache failure falls
// back to an empty usable screen; the normal scan repairs its read model.
func (a *App) CachedScan() *ScanDTO {
	core := a.snapshot()
	inv := core.CachedInventory()
	a.mu.Lock()
	if a.inv == nil {
		a.inv = inv
	} else {
		inv = a.inv
	}
	a.mu.Unlock()
	d := scanDTO(core, inv, time.Time{}, time.Time{}, presence.Table{})
	d.Cached = len(inv.Entries) > 0
	d.Revision = a.scanRevision.Add(1)
	a.publishQuick(d)
	return d
}

// selectionInventory upgrades just the selected source and local destination.
// Replacing a.inv also relinquishes an in-flight scan's publication ownership,
// so its late result cannot close transports used by this action/review.
func (a *App) selectionInventory(ctx context.Context, core *app.App, inv *app.Inventory, e app.Entry) (*app.Inventory, app.Entry, error) {
	m := inv.Machine(e.Machine)
	if !e.Cached && !inv.Discovering && m != nil && m.Host() != nil {
		return inv, e, nil
	}
	fresh, entry, err := core.FreshSelection(ctx, e)
	if err != nil {
		return inv, e, err
	}
	a.mu.Lock()
	old := a.inv
	if old != nil {
		// Keep other sources visible, but detach their old transports and live evidence.
		b, _ := json.Marshal(old)
		saved := &app.Inventory{}
		_ = json.Unmarshal(b, saved)
		for _, m := range saved.Machines {
			if fresh.Machine(m.Name) == nil {
				fresh.Machines = append(fresh.Machines, m)
			}
		}
		known := map[string]bool{}
		for _, x := range fresh.Entries {
			known[app.EntryIdentity(x.Machine, x.Session.Key.String())] = true
		}
		for _, x := range saved.Entries {
			if !known[app.EntryIdentity(x.Machine, x.Session.Key.String())] && x.Machine != e.Machine && x.Machine != app.LocalName() {
				x.Cached = true
				x.Live.State = "unknown"
				fresh.Entries = append(fresh.Entries, x)
			}
		}
		fresh.Clouds = saved.Clouds
		old.Close()
	}
	a.inv = fresh
	a.mu.Unlock()
	return fresh, entry, nil
}

func (a *App) beginSelection() func() {
	a.mu.Lock()
	a.selectionReads++
	a.mu.Unlock()
	return func() { a.mu.Lock(); a.selectionReads--; a.mu.Unlock() }
}
func (a *App) CancelScan() {
	a.mu.Lock()
	cancel := a.scanCancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// ScanSnapshot returns the current browsing model after a source-only retry.
func (a *App) ScanSnapshot() *ScanDTO {
	core := a.snapshot()
	a.mu.Lock()
	inv := a.inv
	a.mu.Unlock()
	if inv == nil {
		inv = &app.Inventory{}
	}
	d := scanDTO(core, inv, time.Time{}, time.Time{}, presence.Table{})
	d.Revision = a.scanRevision.Add(1)
	return d
}
