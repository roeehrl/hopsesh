package gui

import (
	"context"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
)

// MachineStatuses is what hopsesh can do on each scanned machine (app.MachineStatus):
// version against the newest, login session and receiving, command, hooks and skill.
// Hooks, skill and command are read again when older than ten minutes, or with refresh.
func (a *App) MachineStatuses(refresh bool) []app.MachineStatus {
	core := a.snapshot()
	a.mu.Lock()
	inv := a.inv
	a.mu.Unlock()
	if inv == nil {
		return []app.MachineStatus{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return core.MachineStatuses(ctx, inv, app.StatusOptions{Refresh: refresh})
}
