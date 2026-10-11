package gui

import (
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// HistorySettingsDTO is Settings → History: how much history a transfer carries, and
// the resources it may use. Zero values are automatic/defaults.
type HistorySettingsDTO struct {
	config.History
	ContextBudgets []int `json:"contextBudgets"`
	ReadMemoryMBs  []int `json:"readMemoryMBs"`
	RecordMBs      []int `json:"recordMBs"`
	ArchiveMBs     []int `json:"archiveMBs"`
	LineageMBs     []int `json:"lineageMBs"`
	NativeFileMBs  []int `json:"nativeFileMBs"`
	// Effective values after defaults, shown next to "Default".
	Effective ir.Limits `json:"effective"`
	Defaults  ir.Limits `json:"defaults"`
}

// HistorySettings are Settings → History.
func (a *App) HistorySettings() HistorySettingsDTO {
	a.mu.Lock()
	h := a.core.Cfg.History
	a.mu.Unlock()
	return HistorySettingsDTO{History: h, ContextBudgets: config.HistoryContextBudgets, ReadMemoryMBs: config.HistoryReadMemoryMBs,
		RecordMBs: config.HistoryRecordMBs, ArchiveMBs: config.HistoryArchiveMBs, LineageMBs: config.HistoryLineageMBs, NativeFileMBs: config.HistoryNativeFileMBs,
		Effective: h.Limits(), Defaults: ir.DefaultLimits()}
}

// SetHistorySettings stores Settings → History. Values equal to the default are stored
// as the default (left out of the file), so a later default change still applies.
func (a *App) SetHistorySettings(in config.History) error {
	d := ir.DefaultLimits()
	zero := func(v *int, def int64) {
		if int64(*v)<<20 == def {
			*v = 0
		}
	}
	zero(&in.ReadMemoryMB, d.ReadBytes)
	zero(&in.RecordMB, d.RecordBytes)
	zero(&in.ArchiveMB, d.ArchiveBytes)
	zero(&in.LineageMB, d.LineageBytes)
	zero(&in.NativeFileMB, d.NativeFileBytes)
	if in.Older == ir.OlderExtract {
		in.Older = ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.core.Cfg
	c.History = in
	if err := c.Check(); err != nil {
		return err
	}
	prev := a.core.Cfg.History
	a.core.Cfg.History = in
	if err := a.save(); err != nil {
		a.core.Cfg.History = prev
		return err
	}
	if prev != in {
		// Do not mutate a published inventory while scans/actions still read it.
		// The next selection re-reads its source with the newly saved limits.
		if a.historyStale == nil {
			a.historyStale = map[string]bool{}
		}
		if a.inv != nil {
			for _, e := range a.inv.Entries {
				a.historyStale[app.EntryIdentity(e.Machine, e.Session.Key.String())] = true
			}
		}
		a.invalidateReviewsLocked()
	}
	return nil
}

// Call only after publishing observations made under the current settings.
func (a *App) historyObservedLocked(inv *app.Inventory) {
	for _, e := range inv.Entries {
		if !e.Cached {
			delete(a.historyStale, app.EntryIdentity(e.Machine, e.Session.Key.String()))
		}
	}
}
