package gui

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/termapp"
)

// Handing a cloud session on to another cloud in the window: the Hand off ▸ menu on a
// cloud row, the sheet with both legs, and the wait while the first leg runs in the user's
// terminal (Claude Code's teleport); then the hand-off's done screen. And the clean-up of
// the branches hand-offs left on the remotes, from Activity.

// HopPlanDTO is a hop's plan as the sheet shows it.
type HopPlanDTO struct {
	Kind     string        `json:"kind"` // "hop"
	Title    string        `json:"title"`
	Agent    string        `json:"agent"`
	Mark     string        `json:"mark"`
	Warnings []string      `json:"warnings"`
	Blockers []string      `json:"blockers"`
	Hop      *move.HopPlan `json:"hop"`
}

// PlanHop works out handing a cloud session on to another cloud through this machine, and
// keeps the plan for ApplyHop. Nothing changes.
func (a *App) PlanHop(machine, key, cloud string, o HandoffOptsDTO) (*HopPlanDTO, error) {
	done := a.beginSelection()
	defer done()
	core := a.snapshot()
	a.mu.Lock()
	e, err := a.find(machine, key)
	inv := a.inv
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	inv, e, err = a.selectionInventory(ctx, core, inv, e)
	if err != nil {
		return nil, err
	}
	opt := core.HandoffDefaults(cloud)
	opt.Mark, opt.Note, opt.CarryRules, opt.Env = o.Mark, o.Note, o.CarryRules, o.Env
	if o.Cleanup != "" {
		opt.Cleanup = o.Cleanup
	}
	p, err := core.PlanHop(ctx, inv, e, cloud, "", opt)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.plan, a.input, a.res = p, move.Input{}, nil
	a.mu.Unlock()
	d := &HopPlanDTO{Kind: p.Kind, Title: p.Title, Agent: p.Agent, Mark: p.Mark, Warnings: p.Warnings, Blockers: p.Blockers, Hop: p.Hop}
	if d.Warnings == nil {
		d.Warnings = []string{}
	}
	if d.Blockers == nil {
		d.Blockers = []string{}
	}
	return d, nil
}

// HopDoneDTO is where a hop stands: waiting for the user's terminal (its first leg), or
// done (Handoff is the hand-off as its done screen shows it), or failed.
type HopDoneDTO struct {
	Journal  string          `json:"journal"`
	Title    string          `json:"title"`
	Hop      *move.HopResult `json:"hop"`
	Fetch    string          `json:"fetch,omitempty"` // the first leg's journal
	Handoff  *HandedOffDTO   `json:"handoff,omitempty"`
	Brought  *BroughtDTO     `json:"brought,omitempty"`
	Error    string          `json:"error,omitempty"`
	Warnings []string        `json:"warnings"`
}

// ApplyHop carries out the last hop plan. When its first leg needs the user's terminal, it
// opens it there and returns waiting: the window then asks ContinueHop until the copy is
// here and handed on.
func (a *App) ApplyHop() (*HopDoneDTO, error) {
	core := a.snapshot()
	a.mu.Lock()
	p := a.plan
	a.mu.Unlock()
	if p == nil || p.Kind != move.KindHop {
		return nil, errors.New("no hop planned; choose the session again")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	res, err := core.Apply(ctx, p, move.Input{}, func(step string) { a.emit(ProgressEvent, step) })
	if res == nil {
		return nil, err
	}
	d := a.hopDone(core, p.Title, res, err)
	if res.Hop != nil && res.Hop.State == move.HopWaiting && res.Hop.Command != "" {
		if terr := a.openLaunch(hopLaunch(p.Title, res.Hop)); terr != nil {
			d.Warnings = append(d.Warnings, "hopsesh could not open a terminal ("+terr.Error()+"); run the command yourself")
		}
	}
	return d, nil
}

// ContinueHop takes a waiting hop on, once its copy is here (the window asks it while it
// waits).
func (a *App) ContinueHop(journal string) (*HopDoneDTO, error) {
	core := a.snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	res, err := core.ContinueHop(ctx, journal, func(step string) { a.emit(ProgressEvent, step) })
	if res == nil {
		return nil, err
	}
	title := ""
	if h, herr := core.LoadHop(journal); herr == nil {
		title = h.Title
	}
	return a.hopDone(core, title, res, err), nil
}

// OpenHop opens a waiting hop's first-leg command in a terminal again.
func (a *App) OpenHop(journal string) error {
	h, err := a.snapshot().LoadHop(journal)
	if err != nil {
		return err
	}
	if h.Result.State != move.HopWaiting || len(h.Result.Run.Argv) == 0 {
		return errors.New("there is nothing to open")
	}
	return a.openLaunch(hopLaunch(h.Title, &h.Result))
}

// hopLaunch is a waiting hop's first leg: the agent's own command that brings the session
// here (claude --teleport), a step in the user's terminal.
func hopLaunch(title string, h *move.HopResult) app.Launch {
	return app.Launch{Kind: termapp.KindStep, Run: h.Run, Labels: termapp.Labels{Title: title, Agent: filepath.Base(h.Run.Argv[0]), Machine: app.LocalName()}}
}

func (a *App) hopDone(core *app.App, title string, res *move.Result, err error) *HopDoneDTO {
	d := &HopDoneDTO{Journal: res.Journal, Title: title, Hop: res.Hop, Warnings: append([]string{}, res.Warnings...)}
	if err != nil {
		d.Error = err.Error()
	}
	if h := res.Hop; h != nil {
		d.Fetch = h.Fetch
		if f, ferr := move.LoadFetch(core.StateDir, h.Fetch); ferr == nil {
			b := core.Brought(f)
			d.Brought = &b
		}
		if h.Remembered {
			a.mu.Lock()
			if serr := a.save(); serr != nil {
				d.Warnings = append(d.Warnings, "could not remember the environment: "+serr.Error())
			}
			a.mu.Unlock()
		}
	}
	if r := res.Handoff; r != nil {
		d.Handoff = &HandedOffDTO{Journal: res.Journal, Title: title, Agent: d.agentName(), Mark: res.Mark, Warnings: d.Warnings, Handoff: r}
		if u := r.BranchURL; u != "" {
			handoffPagesMu.Lock()
			handoffPages[u] = true
			handoffPagesMu.Unlock()
		}
	}
	return d
}

func (d *HopDoneDTO) agentName() string {
	if d.Brought != nil {
		return d.Brought.Agent
	}
	return ""
}

// CleanupBranches lists the branches hand-offs left on their remotes and whether hopsesh
// offers to delete each (it asks the remotes, read-only).
func (a *App) CleanupBranches() []app.BranchCandidate {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out := a.snapshot().CleanupCandidates(ctx)
	if out == nil {
		out = []app.BranchCandidate{}
	}
	return out
}

// DeleteBranches deletes the merged branches the user picked, each with a lease; undo
// pushes them back.
func (a *App) DeleteBranches(ids []string) (*app.CleanupResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return a.snapshot().DeleteBranches(ctx, ids)
}
