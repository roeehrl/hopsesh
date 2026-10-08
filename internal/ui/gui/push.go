package gui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/peer"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// PushPlan asks hopsesh on another machine to plan receiving a session from this one, in
// its own agent (target "") or another. The connection stays open for PushApply.
func (a *App) PushPlan(key, machine, target string, o OptsDTO) (*PlanDTO, error) {
	core := a.snapshot()
	a.mu.Lock()
	inv := a.inv
	var here string
	if inv != nil && inv.Local() != nil {
		here = inv.Local().Name
	}
	e, err := a.find(here, key)
	a.closePushLocked()
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	to := core.Cfg.FindHost(machine)
	if to == nil || !to.Allowed {
		return nil, fmt.Errorf("%s is not an allowed machine", machine)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	p, err := core.StartPush(ctx, inv, e, *to, agent.ID(target), o.options(core.DefaultOptions()))
	if errors.Is(err, peer.ErrRefused) {
		return nil, fmt.Errorf("%s does not receive sessions yet: on %s, turn on Receive sessions in hopsesh's settings (or run hopsesh receive on), then try again", machine, machine)
	}
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.push, a.plan, a.res = p, nil, nil
	a.mu.Unlock()
	tm, ok := core.Module(p.Plan.Placement.Key.Agent)
	if !ok {
		tm, _ = core.Module(e.Agent)
	}
	d := planDTO(p.Plan, e, tm)
	d.Machine = machine
	return d, nil
}

// PushApply carries out the planned push.
func (a *App) PushApply() (*DoneDTO, error) {
	a.mu.Lock()
	p := a.push
	a.mu.Unlock()
	if p == nil {
		return nil, errors.New("no plan; choose the session again")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	a.emit(ProgressEvent, "sending the session to "+p.Plan.Target.Location)
	r, err := p.Commit(ctx)
	a.mu.Lock()
	a.closePushLocked()
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	res, pl := r.Result, p.Plan
	d := &DoneDTO{NoWork: pl.NoWork, Kind: pl.Kind, Title: pl.Title, Agent: pl.Agent, Command: res.Command, Files: res.Files, Bytes: move.Human(res.Bytes),
		Secrets: res.Secrets.Total, Redacted: pl.Options.Redact, Cloned: res.Cloned, Worktree: res.Worktree, Journal: r.Journal,
		SourceHost: pl.Source.Location, Pushed: r.Pushed, SyncNote: res.SyncNote, Mark: res.Mark, MarkError: res.MarkError, Notice: res.Notice,
		Warnings: res.Warnings, Machine: r.Machine, AuditDir: filepath.Join(config.StateDir(), "log"), ContinuationHint: pl.ContinuationHint()}
	if pl.NoWork && pl.SyncTo != nil && pl.SyncTo.Title != "" {
		d.Title = pl.SyncTo.Title
	}
	for _, n := range res.Rewrite.Replacements {
		d.Paths += n
	}
	if res.Sync != nil {
		d.SyncState = res.Sync.State
	}
	return d, nil
}

// ClosePlan drops the plan the window closed, and ends a send's connection.
func (a *App) ClosePlan() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.plan, a.input, a.res = nil, move.Input{}, nil
	a.closePushLocked()
}

// closePushLocked ends an open push connection (callers hold a.mu).
func (a *App) closePushLocked() {
	if a.push != nil {
		a.push.Close()
		a.push = nil
	}
}
