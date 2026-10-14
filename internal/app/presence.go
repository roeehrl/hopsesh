package app

import (
	"context"
	"errors"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/peer"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// How hopsesh works with a machine (Machine.Presence).
const (
	// PresenceLogin: hopsesh runs in the login session there and answers for it.
	PresenceLogin = "login"
	// PresenceInstalled: hopsesh is there, but not in a login session (nobody is logged
	// in, or it was not set up to start at login): SSH-only answers, plus sending.
	PresenceInstalled = "installed"
	// PresenceOlder: hopsesh there is older than this one's protocol 6.
	PresenceOlder = "older"
	// PresenceSSH: no hopsesh there; it is read over SSH only.
	PresenceSSH = "ssh"
)

// meetHopsesh asks hopsesh on a machine reached over SSH whether it runs in the login
// session there; when it does, the sign-ins come from it (a Mac's Keychain included).
func (a *App) meetHopsesh(ctx context.Context, hm *host.Machine, m *Machine) {
	if m.Hopsesh == "" {
		m.Presence = PresenceSSH
		return
	}
	h, err := a.allowedHost(hm.Name)
	if err != nil {
		m.Presence = PresenceInstalled
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c, hr, done, err := a.dialPeer(cctx, h)
	if err != nil {
		m.Presence, m.PresenceWhy = PresenceInstalled, err.Error()
		return
	}
	defer done()
	m.Hopsesh = hr.Version
	switch {
	case hr.Protocol < 6:
		m.Presence = PresenceOlder
		return
	case !hr.Login:
		m.Presence, m.PresenceWhy = PresenceInstalled, peer.NoLogin(hm.Name).Error()
		return
	}
	// Only what hopsesh there answers comes from the login session; the commands this
	// machine still runs over SSH do not (hm.Facts.LoginSession stays false).
	m.Presence = PresenceLogin
	var ps []agent.RuntimeProfile
	if err := c.Call(cctx, peer.MethodAccounts, struct{}{}, &ps); err != nil {
		a.Log.Warn("accounts from hopsesh there", "machine", hm.Name, "error", err)
		return
	}
	known, err := a.Accounts()
	if err != nil {
		return
	}
	endpoint, err := hm.ReadIdentity(cctx)
	if err != nil || endpoint == "" {
		endpoint = hr.Endpoint
	}
	if endpoint == "" {
		return
	}
	for _, mod := range a.Modules() {
		if mod.Spec().Accounts == nil {
			continue
		}
		if err := a.importRemoteProfiles(cctx, hm, endpoint, mod.Spec().ID, known, ps); err != nil {
			a.Log.Warn("importing accounts from hopsesh there", "machine", hm.Name, "error", err)
		}
	}
}

// LocalAccounts checks every account folder on this machine now (hopsesh serve answering
// another machine) and returns them, without their tags (the user's own words).
func (a *App) LocalAccounts(ctx context.Context) ([]agent.RuntimeProfile, error) {
	m := a.localMachine(ctx)
	defer m.Close()
	endpoint, err := m.ReadIdentity(ctx)
	if err != nil {
		return nil, err
	}
	var errs []error
	for _, mod := range a.Modules() {
		if mod.Spec().Accounts == nil {
			continue
		}
		h, err := m.For(ctx, mod.Spec(), agent.Install{}, nil)
		if err != nil {
			continue
		}
		base, err := mod.Detect(ctx, h)
		if err != nil {
			continue
		}
		if _, err := a.profileInstalls(ctx, m, mod, base, true); err != nil {
			errs = append(errs, err)
		}
	}
	if endpoint == "" {
		endpoint, _ = m.ReadIdentity(ctx)
	}
	ps, err := a.accountStore().List()
	if err != nil {
		return nil, errors.Join(append(errs, err)...)
	}
	out := []agent.RuntimeProfile{}
	for _, p := range ps {
		if p.Endpoint == endpoint {
			p.Tags = nil
			out = append(out, p)
		}
	}
	return out, nil
}
