package app

import (
	"errors"
	"fmt"

	"github.com/roeehrl/hopsesh/internal/core/launch"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Resume is the command that continues a session that is already on this machine.
func (a *App) Resume(inv *Inventory, e Entry, o agent.ResumeOptions) (agent.Command, error) {
	m := inv.Machine(e.Machine)
	if m == nil || !m.Local {
		return agent.Command{}, errors.New("the session is not on this machine; bring it here first")
	}
	mod, ok := a.Module(e.Agent)
	if !ok {
		return agent.Command{}, fmt.Errorf("%s is not enabled", e.Agent)
	}
	in, ok := m.Install(e.Agent)
	if !ok {
		return agent.Command{}, fmt.Errorf("%w: %s", agent.ErrNotInstalled, mod.Spec().Name)
	}
	if o.App && !agent.Has(mod, agent.CapApp) {
		return agent.Command{}, fmt.Errorf("%w: %s has no desktop app hopsesh can open", agent.ErrUnsupported, mod.Spec().Name)
	}
	p := agent.Placement{Key: e.Session.Key, SourceID: e.Session.Key.Session, CWD: e.Session.CWD, Location: m.Name}
	return mod.Resume(in, e.Session.Key, p, o), nil
}

// ResumeShell is Resume as a line for this machine's shell.
func (a *App) ResumeShell(inv *Inventory, e Entry) (string, error) {
	c, err := a.Resume(inv, e, agent.ResumeOptions{})
	if err != nil {
		return "", err
	}
	return launch.Shell(c, "", launch.DefaultShell()), nil
}
