package app

import (
	"context"

	"github.com/roeehrl/hopsesh/internal/core/move"
)

// EndDestination uses the reviewed module/profile and checks its registration
// before asking the module to end the original. It never applies the transfer.
func (a *App) EndDestination(ctx context.Context, p *move.Plan, in move.Input) error {
	if err := a.checkAccountRegistration(in.Target.Install); err != nil {
		return err
	}
	return move.EndDestination(ctx, p, in)
}
