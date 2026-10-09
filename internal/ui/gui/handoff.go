package gui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Handing a session off to a cloud in the window: Move ▾'s clouds (every cloud, a
// disabled one with its reason), the plan sheet, the applying checklist, the done screen
// and a follow-up.

// HandoffOptsDTO are the hand-off sheet's choices.
type HandoffOptsDTO struct {
	Untracked   []string `json:"untracked"`   // untracked files to carry (paths)
	HistoryFile bool     `json:"historyFile"` // also commit the conversation on the branch
	Bundle      bool     `json:"bundle"`      // the agent uploads the repository instead
	Mark        bool     `json:"mark"`
	Cleanup     string   `json:"cleanup"`
	Brief       string   `json:"brief"` // the user's edit of the briefing ("" : hopsesh's)
	Note        string   `json:"note"`
	CarryRules  bool     `json:"carryRules"`
	// Env is the cloud environment picked ("": the repository's); StartingDiff sends the
	// changes with the cloud session instead of on a branch.
	Env          string `json:"env"`
	StartingDiff bool   `json:"startingDiff"`
}

// HandoffDefaultsDTO are the sheet's first choices for a cloud, from the configuration.
type HandoffDefaultsDTO struct {
	HistoryFile bool   `json:"historyFile"`
	Bundle      bool   `json:"bundle"`
	Mark        bool   `json:"mark"`
	Cleanup     string `json:"cleanup"`
}

// HandoffDefaults are a cloud's hand-off defaults.
func (a *App) HandoffDefaults(cloud string) HandoffDefaultsDTO {
	o := a.snapshot().HandoffDefaults(cloud)
	return HandoffDefaultsDTO{HistoryFile: o.HistoryFile, Bundle: o.Bundle, Mark: o.Mark, Cleanup: o.Cleanup}
}

// HandoffPlanDTO is a hand-off plan as the sheet shows it.
type HandoffPlanDTO struct {
	Kind       string            `json:"kind"` // "handoff"
	Title      string            `json:"title"`
	Agent      string            `json:"agent"` // the session's agent
	AgentID    agent.ID          `json:"agentId"`
	SourceHost string            `json:"sourceHost"`
	Local      bool              `json:"local"`
	Mark       string            `json:"mark"`
	Warnings   []string          `json:"warnings"`
	Blockers   []string          `json:"blockers"`
	Handoff    *move.HandoffPlan `json:"handoff"`
}

// PlanHandoff works out handing a session off to a cloud and keeps the plan for
// ApplyHandoff. Nothing changes.
func (a *App) PlanHandoff(machine, key, cloud string, o HandoffOptsDTO) (*HandoffPlanDTO, error) {
	doneSelection := a.beginSelection()
	defer doneSelection()
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
	opt.Untracked, opt.HistoryFile, opt.Bundle, opt.Mark = o.Untracked, o.HistoryFile, o.Bundle, o.Mark
	opt.Brief, opt.Note, opt.CarryRules = o.Brief, o.Note, o.CarryRules
	opt.Env, opt.StartingDiff = o.Env, o.StartingDiff
	if o.Cleanup != "" {
		opt.Cleanup = o.Cleanup
	}
	p, err := core.PlanHandoff(ctx, inv, e, cloud, opt)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.plan, a.input, a.res = p, move.Input{}, nil
	a.mu.Unlock()
	d := &HandoffPlanDTO{Kind: p.Kind, Title: p.Title, Agent: e.AgentName, AgentID: e.Agent, SourceHost: p.Source.Location, Local: e.Machine == app.LocalName(),
		Mark: p.Mark, Warnings: p.Warnings, Blockers: p.Blockers, Handoff: p.Handoff}
	if d.Warnings == nil {
		d.Warnings = []string{}
	}
	if d.Blockers == nil {
		d.Blockers = []string{}
	}
	return d, nil
}

// HandedOffDTO is a hand-off as the done screen shows it: what happened, or the step that
// failed and what had happened by then.
type HandedOffDTO struct {
	Journal  string              `json:"journal"`
	Title    string              `json:"title"`
	Agent    string              `json:"agent"`
	Mark     string              `json:"mark"`
	Warnings []string            `json:"warnings"`
	Error    string              `json:"error,omitempty"`
	Handoff  *move.HandoffResult `json:"handoff"`
}

// handoffPages are the pages a hand-off made (its branch on GitHub), which OpenURL may
// open.
var (
	handoffPagesMu sync.Mutex
	handoffPages   = map[string]bool{}
)

// ApplyHandoff carries out the last hand-off plan, sending each step to the window. A step
// that fails is reported in the result (with what had happened by then), not as an error.
func (a *App) ApplyHandoff() (*HandedOffDTO, error) {
	core := a.snapshot()
	a.mu.Lock()
	p := a.plan
	a.mu.Unlock()
	if p == nil || p.Kind != move.KindHandoff {
		return nil, errors.New("no hand-off planned; choose the session again")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	res, err := core.Apply(ctx, p, move.Input{}, func(step string) { a.emit(ProgressEvent, step) })
	if res == nil || res.Handoff == nil {
		if err == nil {
			err = errors.New("the hand-off did nothing")
		}
		return nil, err
	}
	a.mu.Lock()
	a.res = res
	if err == nil && a.core.RememberEnv(p) {
		if serr := a.save(); serr != nil {
			res.Warnings = append(res.Warnings, "could not remember the environment for "+p.Handoff.Repo+": "+serr.Error())
		}
	}
	a.mu.Unlock()
	d := &HandedOffDTO{Journal: res.Journal, Title: p.Title, Agent: p.Agent, Mark: res.Mark, Warnings: res.Warnings, Handoff: res.Handoff}
	if d.Warnings == nil {
		d.Warnings = []string{}
	}
	if err != nil {
		d.Error = err.Error()
	}
	if u := res.Handoff.BranchURL; u != "" {
		handoffPagesMu.Lock()
		handoffPages[u] = true
		handoffPagesMu.Unlock()
	}
	return d, nil
}

func handoffPage(url string) bool {
	handoffPagesMu.Lock()
	defer handoffPagesMu.Unlock()
	return handoffPages[url]
}

// FollowUp sends a cloud session one message (it starts a turn there, on the user's plan).
func (a *App) FollowUp(cloud, id, text string) (*agent.CloudSession, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("write the message first")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cs, err := a.snapshot().FollowUp(ctx, cloud, agent.SessionID(id), text)
	if err != nil {
		return nil, err
	}
	return &cs, nil
}
