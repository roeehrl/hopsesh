package gui

import (
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// NoticeHooks reports local installation only. Vendor trust and actual delivery
// remain separate from an installed configuration file.
func (a *App) NoticeHooks() ([]app.MovementHookStatus, error) {
	ctx, cancel := ctx20()
	defer cancel()
	return a.snapshot().NoticeHooks(ctx)
}
func (a *App) InstallNoticeHooks(id, profile string) ([]app.MovementHookStatus, error) {
	ctx, cancel := ctx20()
	defer cancel()
	return a.snapshot().InstallNoticeHooks(ctx, agent.ID(id), profile)
}
func (a *App) RemoveNoticeHooks(id, profile string) ([]app.MovementHookStatus, error) {
	ctx, cancel := ctx20()
	defer cancel()
	return a.snapshot().RemoveNoticeHooks(ctx, agent.ID(id), profile)
}

// ResolveEntry resolves a raw scanned replica even when grouping shows a different
// copy of its branch. It never scans or guesses a destination from timestamps.
func (a *App) ResolveEntry(machine, key string) (*EntryDTO, error) {
	core := a.snapshot()
	a.mu.Lock()
	defer a.mu.Unlock()
	e, err := a.find(machine, key)
	if err != nil {
		return nil, err
	}
	item := app.Item{Entry: e}
	for _, it := range a.inv.Items() {
		for _, c := range it.Copies {
			if c.Machine == machine && c.Key.String() == key {
				item.Copies = it.Copies
				break
			}
		}
		if len(item.Copies) > 0 {
			break
		}
	}
	d := entryDTO(core, a.inv, item, continueTargets(core, a.inv))
	return &d, nil
}
