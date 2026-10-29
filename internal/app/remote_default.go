package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/peer"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// RemoteDefault is one agent folder's own "start Remote Control for every session"
// setting (Claude Code's remoteControlAtStartup in its user settings).
type RemoteDefault struct {
	Agent        agent.ID `json:"agent"`
	AgentName    string   `json:"agentName"`
	Profile      string   `json:"profile,omitempty"`
	ProfileLabel string   `json:"profileLabel,omitempty"`
	Path         string   `json:"path,omitempty"`
	On           bool     `json:"on"`
	Reason       string   `json:"reason,omitempty"` // why it can't be set in this folder
	Error        string   `json:"error,omitempty"`
}

// RemoteDefaults are a machine's agent folders' Remote Control defaults.
type RemoteDefaults struct {
	Machine string          `json:"machine"`
	Local   bool            `json:"local"`
	Folders []RemoteDefault `json:"folders"`
	Error   string          `json:"error,omitempty"`
}

// LocalRemoteDefaults reads this machine's agent folders' Remote Control defaults.
func (a *App) LocalRemoteDefaults(ctx context.Context) (RemoteDefaults, error) {
	return a.localRemoteDefaults(ctx, nil)
}

// SetLocalRemoteDefault turns Remote Control for every session on or off in each of this
// machine's agent folders that has the setting.
func (a *App) SetLocalRemoteDefault(ctx context.Context, on bool) (RemoteDefaults, error) {
	return a.localRemoteDefaults(ctx, &on)
}

func (a *App) localRemoteDefaults(ctx context.Context, set *bool) (RemoteDefaults, error) {
	m := a.localMachine(ctx)
	defer m.Close()
	return a.remoteDefaultsOn(ctx, m, set)
}

func (a *App) remoteDefaultsOn(ctx context.Context, m *host.Machine, set *bool) (RemoteDefaults, error) {
	out := RemoteDefaults{Machine: m.Name, Local: true, Folders: []RemoteDefault{}}
	var errs []error
	for _, mod := range a.Modules() {
		d, ok := mod.(agent.RemoteControlDefaulter)
		if !ok {
			continue
		}
		h, err := m.For(ctx, mod.Spec(), agent.Install{}, nil)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		base, err := mod.Detect(ctx, h)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		// The same folders the protection hooks go in: the default and the registered ones.
		installs, err := a.movementHookInstalls(ctx, m, mod, base, "")
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, in := range installs {
			fh, err := m.For(ctx, mod.Spec(), in, nil)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			f := d.RemoteControlDefault(fh, in)
			if f == nil {
				continue
			}
			rd := RemoteDefault{Agent: mod.Spec().ID, AgentName: mod.Spec().Name, Profile: in.ProfileID(), ProfileLabel: profileLabel(in.Profile), Path: f.Path, Reason: f.Reason}
			if filepath.Dir(f.Path) != filepath.Clean(in.Root("home")) {
				rd.Error = "the settings file is outside the agent folder"
				out.Folders = append(out.Folders, rd)
				continue
			}
			if set != nil && f.Reason == "" {
				if err := editMovementHookFile(ctx, f.Path, func(b []byte) ([]byte, error) { return f.Set(b, *set) }); err != nil {
					rd.Error = err.Error()
					errs = append(errs, err)
				}
			}
			b, err := readMovementHookFile(f.Path)
			if err == nil {
				rd.On, err = f.Get(b)
			}
			if err != nil && rd.Error == "" {
				rd.Error = err.Error()
			}
			out.Folders = append(out.Folders, rd)
		}
	}
	err := errors.Join(errs...)
	if err != nil {
		out.Error = err.Error()
	}
	return out, err
}

// MachineRemoteDefaults reads, or with set changes, another machine's Remote Control
// defaults through hopsesh in its login session there.
func (a *App) MachineRemoteDefaults(ctx context.Context, machine string, set *bool) (RemoteDefaults, error) {
	out := RemoteDefaults{Machine: machine, Folders: []RemoteDefault{}}
	c, hr, done, err := a.loginPeer(ctx, machine)
	if err != nil {
		out.Error = err.Error()
		return out, err
	}
	defer done()
	if !slices.Contains(hr.Features, peer.FeatureRemoteDefault) {
		err = fmt.Errorf("update hopsesh on %s first (Machines › Update hopsesh): %s there can't read or change this", machine, hr.Version)
		out.Error = err.Error()
		return out, err
	}
	err = c.Call(ctx, peer.MethodRemoteDefault, peer.RemoteDefault{Set: set}, &out)
	out.Machine, out.Local = machine, false
	if out.Folders == nil {
		out.Folders = []RemoteDefault{}
	}
	return out, err
}
