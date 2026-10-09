package gui

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/pty"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

type AccountDTO struct {
	agent.RuntimeProfile
	Machine string `json:"machine"`
	Local   bool   `json:"local"`
	Stale   bool   `json:"stale"`
}

func (a *App) Accounts() ([]AccountDTO, error) {
	core := a.snapshot()
	ps, err := core.Accounts()
	if err != nil {
		return nil, err
	}
	endpoint, _ := core.LocalAccountEndpoint(context.Background())
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []AccountDTO{}
	for _, p := range ps {
		d := AccountDTO{RuntimeProfile: p, Machine: p.Machine, Local: endpoint != "" && p.Endpoint == endpoint, Stale: p.CheckedAt.IsZero() || time.Since(p.CheckedAt) > 5*time.Minute}
		if a.inv != nil {
			for _, m := range a.inv.Machines {
				if m.Host() != nil && m.Host().Facts.Endpoint == p.Endpoint {
					d.Machine = m.Name
					d.Local = m.Local
					break
				}
			}
		}
		out = append(out, d)
	}
	return out, nil
}
func (a *App) RegisterAccount(machine, id, name, root string, tags []string) (agent.RuntimeProfile, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return a.snapshot().RegisterAccount(ctx, machine, agent.ID(id), name, root, tags)
}
func (a *App) EditAccount(id, name string, tags []string, generation int) error {
	return a.snapshot().EditAccount(id, name, tags, generation)
}
func (a *App) ForgetAccount(id string, generation int) error {
	return a.snapshot().ForgetAccount(id, generation)
}
func (a *App) LoginAccount(id string) (*OpenedDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c, err := a.snapshot().AccountLogin(ctx, id)
	if err != nil {
		return nil, err
	}
	if a.Terms == nil {
		return nil, errors.New("integrated terminal is unavailable; use hopsesh accounts login " + id + " --run")
	}
	spec, err := a.tabSpec(c, "Account sign-in")
	if err != nil {
		return nil, err
	}
	info, err := a.Terms.Open(spec, TabSetup{Meta: TabMeta{Kind: TabSignIn, Account: id, Command: displayCommand(c.Argv), Agent: "Account sign-in"}, Exited: func(_ pty.Info) { _, _ = a.RefreshAccount(id) }})
	if err != nil {
		return nil, err
	}
	a.Terms.Focus(info.ID)
	return &OpenedDTO{Where: WhereHere, Tab: info.ID}, nil
}

// AccountDestinations returns only present profiles on a scanned destination.
func (a *App) AccountDestinations(machine, id string) []agent.RuntimeProfile {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []agent.RuntimeProfile{}
	if machine == "" {
		machine = app.LocalName()
	}
	if a.inv == nil {
		return out
	}
	m := a.inv.Machine(machine)
	if m == nil {
		return out
	}
	for _, st := range m.Agents {
		if string(st.Agent) == id && st.Install.Present && st.Install.Profile != nil {
			out = append(out, *st.Install.Profile)
		}
	}
	return out
}

func (a *App) ScanAccounts() (*ScanDTO, error) { return a.scanAccounts(true) }

const AccountEvent = "hopsesh:accounts"

// RefreshAccount updates public profile metadata in existing rows without rescanning peers.
func (a *App) RefreshAccount(id string) (agent.RuntimeProfile, error) {
	a.scanMu.Lock()
	defer a.scanMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	a.mu.Lock()
	if a.closing {
		a.mu.Unlock()
		cancel()
		return agent.RuntimeProfile{}, errors.New("app is shutting down")
	}
	a.scanCancel = cancel
	a.mu.Unlock()
	defer func() { cancel(); a.mu.Lock(); a.scanCancel = nil; a.mu.Unlock() }()
	p, err := a.snapshot().RefreshAccount(ctx, id)
	if err != nil {
		a.emit(AccountEvent, map[string]string{"id": id, "error": err.Error()})
		if p.ID == "" {
			return p, err
		}
	}
	a.mu.Lock()
	if a.inv != nil {
		// Published inventories may still be rendering in another window.
		// Replace metadata on a new snapshot rather than editing their rows.
		next := *a.inv
		next.Entries = slices.Clone(a.inv.Entries)
		next.Machines = slices.Clone(a.inv.Machines)
		for i := range next.Entries {
			e := &next.Entries[i]
			if e.Profile != nil && e.Profile.ID == id {
				e.Profile = &p
			}
		}
		for index, previous := range next.Machines {
			machine := *previous
			machine.Agents = slices.Clone(previous.Agents)
			m := &machine
			next.Machines[index] = m
			for i := range m.Agents {
				st := &m.Agents[i]
				if st.Install.ProfileID() == id {
					st.Install.Profile = &p
				}
			}
		}
		a.inv = &next
	}
	a.mu.Unlock()
	a.emit(AccountEvent, p)
	d := a.ScanSnapshot()
	a.emit(DiscoveryEvent, d)
	a.publishQuick(d)
	return p, err
}
