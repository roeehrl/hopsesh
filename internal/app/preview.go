package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"path/filepath"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// The app's inspector: the end of a session's conversation (read when the user selects a
// session, never during a scan), renaming a session in its agent's own data, and how the
// sessions on this machine run right now (presence, between scans).

// ErrNoPreview means the session's agent cannot show the end of its conversation.
var ErrNoPreview = errors.New("this agent's conversations can't be previewed")

// moduleOn returns an entry's module, its machine and its install there.
func (a *App) moduleOn(inv *Inventory, e Entry) (agent.Module, *Machine, agent.Install, error) {
	mod, ok := a.Module(e.Agent)
	if !ok {
		return nil, nil, agent.Install{}, fmt.Errorf("%s is turned off", e.AgentName)
	}
	m := inv.Machine(e.Machine)
	if m == nil || m.host == nil {
		return nil, nil, agent.Install{}, fmt.Errorf("%s is not reached", e.Machine)
	}
	in, ok := m.InstallProfile(e.Agent, e.Session.Key.Profile)
	if !ok {
		return nil, nil, agent.Install{}, fmt.Errorf("%s is not on %s", e.AgentName, e.Machine)
	}
	return mod, m, in, nil
}

// Preview reads the end of a session's conversation on its machine: the last n messages
// (the module's Previewer reads only the end of the file).
func (a *App) Preview(ctx context.Context, inv *Inventory, e Entry, n int) (agent.Preview, error) {
	if e.Location.IsCloud() {
		return agent.Preview{}, ErrNoPreview
	}
	mod, m, in, err := a.moduleOn(inv, e)
	if err != nil {
		return agent.Preview{}, err
	}
	p, ok := mod.(agent.Previewer)
	if !ok {
		return agent.Preview{}, ErrNoPreview
	}
	h, err := m.host.For(ctx, mod.Spec(), in, nil)
	if err != nil {
		return agent.Preview{}, err
	}
	return p.Preview(ctx, h, in, e.Session, n)
}

// CanRename reports whether a session can be renamed in its agent's own data.
func (a *App) CanRename(e Entry) bool {
	mod, ok := a.Module(e.Agent)
	if !ok || e.Location.IsCloud() {
		return false
	}
	_, ok = mod.(agent.Renamer)
	return ok
}

// Rename gives a session a new title in its agent's own data, the way the agent's own
// rename does, in a journal (Activity undoes it). It returns the journal's id.
func (a *App) Rename(ctx context.Context, inv *Inventory, e Entry, title string) (string, error) {
	title, err := agent.CheckTitle(title)
	if err != nil {
		return "", err
	}
	mod, m, in, err := a.moduleOn(inv, e)
	if err != nil {
		return "", err
	}
	r, ok := mod.(agent.Renamer)
	if !ok || e.Location.IsCloud() {
		return "", fmt.Errorf("hopsesh can't rename %s sessions", e.AgentName)
	}
	j, err := journal.New(a.StateDir, journal.KindRename, fmt.Sprintf("rename to “%s” on %s", title, m.Name))
	if err != nil {
		return "", err
	}
	j.AddKey(e.Session.Key)
	h, err := m.host.For(ctx, mod.Spec(), in, j)
	if err == nil {
		err = r.Rename(ctx, h, in, e.Session, title)
	}
	if a.Audit != nil {
		a.Audit.Write(audit.Entry{Action: "rename", Host: m.Name, Session: e.Session.Key.String(), Detail: map[string]any{"ok": err == nil}})
	}
	if err != nil {
		return "", err
	}
	return j.ID, nil
}

// LiveHere asks the agents on this machine which of its sessions in inv are open now (their
// own registries and locks, no scan): by session key, for the sessions whose agent can
// tell.
func (a *App) LiveHere(ctx context.Context, inv *Inventory) map[agent.SessionKey]agent.LiveInfo {
	m := inv.Local()
	out := map[agent.SessionKey]agent.LiveInfo{}
	if m == nil || m.host == nil {
		return out
	}
	type profileKey struct {
		agent   agent.ID
		profile string
	}
	ids := map[profileKey][]agent.SessionID{}
	for _, e := range inv.Entries {
		if e.Machine == m.Name && !e.Location.IsCloud() {
			pk := profileKey{e.Agent, e.Session.Key.Profile}
			ids[pk] = append(ids[pk], e.Session.Key.Session)
		}
	}
	for id, list := range ids {
		mod, ok := a.Module(id.agent)
		if !ok {
			continue
		}
		ld, ok := mod.(agent.LiveDetector)
		in, has := m.InstallProfile(id.agent, id.profile)
		if !ok || !has {
			continue
		}
		h, err := m.host.For(ctx, mod.Spec(), in, nil)
		if err != nil {
			continue
		}
		live, err := ld.Live(ctx, h, in, list)
		if err != nil {
			continue
		}
		for sid, li := range live {
			out[agent.SessionKey{Agent: id.agent, Profile: id.profile, Session: sid}] = li
		}
	}
	return out
}

// PreviewSelected resolves a registered source without enumerating sessions or
// borrowing a background scan's connection. It never reads saved transcript text.
func (a *App) PreviewSelected(ctx context.Context, e Entry, n int) (agent.Preview, error) {
	if e.Location.IsCloud() {
		return agent.Preview{}, ErrNoPreview
	}
	mod, ok := a.Module(e.Agent)
	if !ok {
		return agent.Preview{}, fmt.Errorf("agent is turned off")
	}
	p, ok := mod.(agent.Previewer)
	if !ok {
		return agent.Preview{}, ErrNoPreview
	}
	var m *host.Machine
	var err error
	if e.Machine == LocalName() {
		m = &host.Machine{Name: LocalName(), Local: true, Facts: host.ProbeLocalFast(ctx, a.Specs()), Log: a.Log}
	} else {
		m, err = a.accountMachine(ctx, e.Machine)
	}
	if err != nil {
		return agent.Preview{}, err
	}
	defer m.Close()
	h, err := m.For(ctx, mod.Spec(), agent.Install{}, nil)
	if err != nil {
		return agent.Preview{}, err
	}
	in := agent.DefaultInstall(mod.Spec(), h)
	if e.Session.Key.Profile != "" {
		ps, err := a.Accounts()
		if err != nil {
			return agent.Preview{}, err
		}
		found := false
		endpoint, err := m.ReadIdentity(ctx)
		if err != nil {
			return agent.Preview{}, err
		}
		for _, profile := range ps {
			if profile.ID != e.Session.Key.Profile || profile.Agent != e.Agent || profile.Machine != e.Machine || profile.Endpoint != endpoint {
				continue
			}
			in.Profile = &profile
			in.Accounts = mod.Spec().Accounts
			in.Roots = map[string]string{"home": profile.Root}
			h, err = m.For(ctx, mod.Spec(), in, nil)
			if err != nil {
				return agent.Preview{}, err
			}
			in = agent.DefaultInstall(mod.Spec(), h)
			in.Profile = &profile
			in.Accounts = mod.Spec().Accounts
			found = true
			break
		}
		if !found {
			return agent.Preview{}, fmt.Errorf("account root changed or was removed; refresh this machine")
		}
	}
	// An old catalog must not redirect a preview outside the selected state root.
	root := in.Root("home")
	rel, within := m.Path().Rel(root, e.Session.Path)
	if !within || rel == ".." || strings.HasPrefix(rel, "../") || strings.HasPrefix(rel, `..\`) || filepath.IsAbs(rel) {
		return agent.Preview{}, fmt.Errorf("session path is outside its account root")
	}
	h, err = m.For(ctx, mod.Spec(), in, nil)
	if err != nil {
		return agent.Preview{}, err
	}
	return p.Preview(ctx, h, in, e.Session, n)
}
