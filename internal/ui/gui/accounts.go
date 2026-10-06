package gui

import (
	"context"
	"errors"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
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
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []AccountDTO{}
	for _, p := range ps {
		d := AccountDTO{RuntimeProfile: p, Machine: p.Machine, Stale: true}
		if a.inv != nil {
			for _, m := range a.inv.Machines {
				if m.Host() != nil && m.Host().Facts.Endpoint == p.Endpoint {
					d.Machine = m.Name
					d.Local = m.Local
					d.Stale = false
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
	info, err := a.Terms.Open(spec, TabSetup{Meta: TabMeta{Kind: TabBring, Command: displayCommand(c.Argv), Agent: "Account sign-in"}})
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
