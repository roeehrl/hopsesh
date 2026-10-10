package move

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// CanEndDestination advertises recovery for an exact, reviewed append destination.
// Process handling belongs to the module's Stopper, never to the move engine.
func CanEndDestination(p *Plan, in Input) bool {
	if p == nil || p.Continue == nil || p.Continue.Relation != RelationAppend || p.Continue.AppendTo == nil || in.Target.Machine == nil || in.Target.Module == nil {
		return false
	}
	t := in.Target
	_, stop := t.Module.(agent.Stopper)
	_, live := t.Module.(agent.LiveDetector)
	return slices.Contains(p.Blockers, "the destination copy is open; quit it first") && t.Machine.Local && !t.Machine.IsSnapshot() && t.Machine.Facts.OS != "windows" && stop && live && agent.Has(t.Module, agent.CapStop)
}

// EndDestination ends only the reviewed original session. It does not apply the
// transfer or write a receipt. Callers must discard the plan and replan afterwards:
// the module can save final transcript records while it exits.
func EndDestination(ctx context.Context, p *Plan, in Input) error {
	if !CanEndDestination(p, in) {
		return fmt.Errorf("ending this destination is unavailable; end it in its agent and review a fresh return plan")
	}
	t := in.Target
	s := *p.Continue.AppendTo
	if s.Key != p.Placement.Key || s.Key.Agent != t.Module.Spec().ID || s.Key.Profile != t.Install.ProfileID() || p.Target.Profile != t.Install.ProfileID() || p.Target.Binding != t.Install.BindingID() || p.Target.Location != t.Machine.Name || p.Target.ID != t.Machine.Facts.Endpoint {
		return fmt.Errorf("return destination changed; review a fresh plan")
	}
	if err := ValidateProfiles(ctx, ProfileInput(p, in)); err != nil {
		return err
	}
	// The destination's saved history must still be the one the user reviewed.
	seg, err := readSegment(ctx, t, s)
	if err != nil || seg.Cursor != p.Continue.expect {
		return fmt.Errorf("original conversation changed; review a fresh plan before ending it")
	}
	h, err := t.Machine.For(ctx, t.Module.Spec(), t.Install, nil)
	if err != nil {
		return err
	}
	if err := t.Module.(agent.Stopper).Stop(ctx, h, t.Install, s, 10*time.Second); err != nil {
		return fmt.Errorf("could not end the original session: %w", err)
	}
	live, err := t.Module.(agent.LiveDetector).Live(ctx, h, t.Install, []agent.SessionID{s.Key.Session})
	if err != nil {
		return fmt.Errorf("cannot verify that the original session ended: %w", err)
	}
	if live[s.Key.Session].State != agent.Ended {
		return fmt.Errorf("the original session is still running or its state is unknown; check again before returning")
	}
	return nil
}
