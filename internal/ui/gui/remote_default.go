package gui

import (
	"context"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
)

// RemoteDefaults reads a machine's "start Remote Control for every session" setting in
// each of its agent folders ("" or this machine's name: here; another machine through
// hopsesh in its login session there).
func (a *App) RemoteDefaults(machine string) (*app.RemoteDefaults, error) {
	return a.remoteDefaults(machine, nil)
}

// SetRemoteDefault turns Remote Control for every session on or off in each of a
// machine's agent folders that has the setting.
func (a *App) SetRemoteDefault(machine string, on bool) (*app.RemoteDefaults, error) {
	return a.remoteDefaults(machine, &on)
}

func (a *App) remoteDefaults(machine string, set *bool) (*app.RemoteDefaults, error) {
	core := a.snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var r app.RemoteDefaults
	var err error
	if machine == "" || machine == app.LocalName() {
		if set != nil {
			r, err = core.SetLocalRemoteDefault(ctx, *set)
		} else {
			r, err = core.LocalRemoteDefaults(ctx)
		}
	} else {
		r, err = core.MachineRemoteDefaults(ctx, machine, set)
	}
	// A folder's own failure is in its row; the page shows the rest.
	if err != nil && len(r.Folders) > 0 {
		err = nil
	}
	return &r, err
}
