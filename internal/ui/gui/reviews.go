package gui

import (
	"errors"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/move"
)

var errReviewChanged = errors.New("review changed; review the session again")
var errActionRunning = errors.New("a transfer is in progress; wait for it to finish before reviewing another transfer")

func (a *App) reviewBusyLocked() bool {
	return a.actionRunning || a.plan != nil && a.res == nil || a.push != nil
}

// Capture the budget and review generation atomically. Starting another review
// supersedes older planners, including an ABA change back to the same settings.
func (a *App) reviewSnapshot() (*app.App, uint64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.actionRunning {
		return nil, 0, errActionRunning
	}
	a.reviewGeneration++
	c := *a.core
	c.Cfg = a.core.Cfg.Clone()
	return &c, a.reviewGeneration, nil
}

func (a *App) checkReviewLocked(generation uint64) error {
	if a.actionRunning {
		return errActionRunning
	}
	if a.closing || generation != a.reviewGeneration {
		return errReviewChanged
	}
	return nil
}

func (a *App) publishReview(generation uint64, p *move.Plan, in move.Input) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkReviewLocked(generation); err != nil {
		return err
	}
	a.closePushLocked()
	a.plan, a.input, a.res, a.planGeneration = p, in, nil, generation
	a.endDestinationToken, a.endDestinationPlan = "", nil
	return nil
}

func (a *App) publishPushReview(generation uint64, p *app.Push) error {
	a.mu.Lock()
	if err := a.checkReviewLocked(generation); err != nil {
		a.mu.Unlock()
		// This newly created connection was never published or borrowed by apply.
		p.Close()
		return err
	}
	a.closePushLocked()
	a.push, a.plan, a.res, a.pushGeneration = p, nil, nil, generation
	a.endDestinationToken, a.endDestinationPlan = "", nil
	a.mu.Unlock()
	return nil
}

// A settings save or ClosePlan revokes reviews, but never closes a resource an
// active apply borrowed. Release that resource only after its operation returns.
func (a *App) invalidateReviewsLocked() {
	a.reviewGeneration++
	a.endDestinationPlan, a.endDestinationToken = nil, ""
	if !a.actionRunning {
		a.plan, a.input = nil, move.Input{}
		a.closePushLocked()
	}
}

func (a *App) beginPlanAction(kind string) (*app.App, *move.Plan, move.Input, func(), error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, generation := a.plan, a.planGeneration
	if p == nil || kind != "" && p.Kind != kind {
		return nil, nil, move.Input{}, nil, errors.New("no plan; choose the session again")
	}
	if err := a.checkReviewLocked(generation); err != nil {
		return nil, nil, move.Input{}, nil, err
	}
	a.actionRunning = true
	c := *a.core
	c.Cfg = a.core.Cfg.Clone()
	return &c, p, a.input, func() { a.finishAction(generation, p, nil) }, nil
}

func (a *App) beginPushAction() (*app.Push, func(), error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, generation := a.push, a.pushGeneration
	if p == nil {
		return nil, nil, errors.New("no plan; choose the session again")
	}
	if err := a.checkReviewLocked(generation); err != nil {
		return nil, nil, err
	}
	a.actionRunning = true
	return p, func() { a.finishAction(generation, nil, p) }, nil
}

func (a *App) finishAction(generation uint64, p *move.Plan, push *app.Push) {
	a.mu.Lock()
	a.actionRunning = false
	if push != nil && a.push == push {
		a.push = nil
	}
	if a.plan == p && a.res == nil && generation != a.reviewGeneration {
		a.plan, a.input = nil, move.Input{}
	}
	a.mu.Unlock()
	if push != nil {
		push.Close()
	}
}
