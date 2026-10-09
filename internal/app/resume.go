package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/launch"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Resume is the command that continues a session that is already on this machine.
func (a *App) Resume(inv *Inventory, e Entry, o agent.ResumeOptions) (agent.Command, error) {
	m := inv.Machine(e.Machine)
	if e.Cached || inv.Discovering || m != nil && m.host == nil {
		ctx, done := context.WithTimeout(context.Background(), time.Minute)
		defer done()
		fresh, entry, err := a.FreshSelection(ctx, e)
		if err != nil {
			return agent.Command{}, err
		}
		defer fresh.Close()
		inv, e, m = fresh, entry, fresh.Machine(entry.Machine)
	}
	if m == nil || !m.Local {
		return agent.Command{}, errors.New("the session is not on this machine; bring it here first")
	}
	mod, ok := a.Module(e.Agent)
	if !ok {
		return agent.Command{}, fmt.Errorf("%s is not enabled", e.Agent)
	}
	in, ok := m.InstallProfile(e.Agent, e.Session.Key.Profile)
	if !ok {
		return agent.Command{}, fmt.Errorf("%w: %s", agent.ErrNotInstalled, mod.Spec().Name)
	}
	if err := a.checkAccountRegistration(in); err != nil {
		return agent.Command{}, err
	}
	if o.App && in.Profile != nil && !in.Profile.Default {
		return agent.Command{}, fmt.Errorf("desktop opening cannot select an account profile; use the integrated or external terminal")
	}
	if o.App && !agent.Has(mod, agent.CapApp) {
		return agent.Command{}, fmt.Errorf("%w: %s has no desktop app hopsesh can open", agent.ErrUnsupported, mod.Spec().Name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if o.App {
		o.AppRunning = false
		if e.Live.State == agent.Live && e.Live.App {
			// Recheck ownership: a cached ID from before /clear must not be imported
			// as a new chat by an application's focus URL.
			if verifier, ok := mod.(agent.AppFocusVerifier); ok {
				h, err := m.host.For(ctx, mod.Spec(), in, nil)
				if err != nil {
					return agent.Command{}, err
				}
				if err := verifier.VerifyAppFocus(ctx, h, in, e.Session.Key); err != nil {
					return agent.Command{}, err
				}
			}
			o.AppRunning = true
		}
		if check, ok := mod.(agent.AppChecker); ok {
			if err := check.CheckApp(in, e.Session.Key, o); err != nil {
				return agent.Command{}, err
			}
		}
	}
	if err := move.ValidateProfiles(ctx, move.Input{Source: move.Side{Machine: m.host, Module: mod, Install: in}}); err != nil {
		return agent.Command{}, err
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
