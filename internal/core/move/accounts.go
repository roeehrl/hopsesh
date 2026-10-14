package move

import (
	"context"
	"fmt"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// A profile boundary never inherits native replay permission from email or org equality.
func profileBoundary(in Input) bool {
	s, t := in.Source.Install, in.Target.Install
	return (s.Profile != nil || t.Profile != nil) && (s.ProfileID() != t.ProfileID() || s.Profile != nil && t.Profile != nil && s.Profile.Endpoint != t.Profile.Endpoint)
}

// sourceAccountKey is the account that ran the session: the one its own record names
// (Claude Code's Remote Control records), else the account its root is signed in to now.
// A root that was signed in to another account before holds that account's sessions.
func sourceAccountKey(in Input) string {
	if m := in.Session.Mirror; m != nil && m.Account != "" {
		return m.Account
	}
	return signedInKey(in.Source)
}

func signedInKey(s Side) string {
	if a := s.Account; a != nil {
		return a.Key
	}
	if p := s.Install.Profile; p != nil && p.Account != nil && p.Account.LoggedIn {
		return p.Account.Key
	}
	return ""
}

// ownerBoundary: the session's recorded account is not the one its destination is
// signed in to now. Its account-bound content (such as signed reasoning) cannot be
// replayed there, even when the destination is the session's own root.
func ownerBoundary(in Input) bool {
	if in.Source.Module == nil || in.Target.Module == nil || in.Source.Module.Spec().ID != in.Target.Module.Spec().ID {
		return false
	}
	m := in.Session.Mirror
	t := signedInKey(in.Target)
	return m != nil && m.Account != "" && t != "" && m.Account != t
}

// samePlace: the destination is the session's own root on its own machine.
func samePlace(in Input) bool {
	return in.Source.Machine != nil && in.Target.Machine != nil && in.Source.Machine.Name == in.Target.Machine.Name &&
		in.Source.Install.ProfileID() == in.Target.Install.ProfileID() && in.Source.Install.BindingID() == in.Target.Install.BindingID()
}
func ValidateProfiles(ctx context.Context, in Input) error {
	for _, side := range []Side{in.Source, in.Target} {
		p := side.Install.Profile
		if p == nil || side.Machine == nil || side.Machine.IsSnapshot() {
			continue
		}
		fsys, err := side.Machine.FS(ctx)
		if err != nil {
			return err
		}
		root, err := fsys.RealPath(p.Root)
		if err != nil || side.Machine.Path().Clean(root) != p.Root {
			return fmt.Errorf("account %q root changed; refresh the plan", p.Name)
		}
		if ap, ok := side.Module.(agent.AccountProber); ok && side.Install.Binary != "" {
			h, err := side.Machine.For(ctx, side.Module.Spec(), side.Install, nil)
			if err != nil {
				return err
			}
			now, err := ap.Account(ctx, h, side.Install)
			if err != nil {
				return fmt.Errorf("cannot recheck account %q: %w", p.Name, err)
			}
			if !p.Default && now.IsolationWhy != "" {
				return fmt.Errorf("account %q: %s", p.Name, now.IsolationWhy)
			}
			// An unknown default login (for example a locked macOS Keychain
			// over SSH) is not evidence that a login changed. Portable transfers
			// across profiles can prepare a fresh copy without claiming account
			// continuity. Never extend this to native replay, named profiles or
			// a previously observed login, and still reject a new visible login.
			if p.Account == nil && p.Default && profileBoundary(in) && !now.LoggedIn && now.Observation != "" {
				continue
			}
			if p.Account == nil || now.Observation != p.Account.Observation || now.LoggedIn != p.Account.LoggedIn || now.Provider != p.Account.Provider {
				return fmt.Errorf("account %q login changed; scan accounts and create a new plan", p.Name)
			}
		}
	}
	return nil
}

// ProfileInput includes the pinned local driver roots of cloud operations too.
func ProfileInput(p *Plan, in Input) Input {
	if p.fetchIn != nil {
		f := p.fetchIn
		in.Source = Side{Machine: f.Machine, Module: f.Module, Install: f.Install}
		if f.Continue != nil {
			in.Target = Side{Machine: f.Machine, Module: f.Continue, Install: f.ContinueInstall}
		}
	}
	if p.handoffIn != nil {
		h := p.handoffIn
		in.Source = h.Source
		in.Target = Side{Machine: h.Here, Module: h.Module, Install: h.Install}
	}
	return in
}
