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
