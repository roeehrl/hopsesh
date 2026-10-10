package gui

import (
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
		RecordMBs: config.HistoryRecordMBs, ArchiveMBs: config.HistoryArchiveMBs, NativeFileMBs: config.HistoryNativeFileMBs,
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
	return nil
}
