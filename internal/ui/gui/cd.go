package gui

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
)

// Change folder…: a session continues in another folder on its machine and stays the same
// session (hopsesh cd). On another machine, hopsesh there plans and makes the change. One
// change is planned at a time; the window plans it again whenever its choices change.

// DirPlanDTO is a planned folder change for the window.
type DirPlanDTO struct {
	Machine string        `json:"machine"`
	Local   bool          `json:"local"`
	Change  app.DirChange `json:"change"`
}

type pendingDir struct {
	mu     sync.Mutex
	local  *app.DirChange
	inv    *app.Inventory
	remote *app.RemoteDir
	cancel context.CancelFunc
}

var dirs pendingDir

func (p *pendingDir) clearLocked() {
	if p.inv != nil {
		p.inv.Close()
	}
	if p.remote != nil {
		p.remote.Close()
	}
	p.local, p.inv, p.remote = nil, nil, nil
}

// PlanChangeDir plans moving a session to dir (a folder on its machine). Nothing changes.
func (a *App) PlanChangeDir(machine, key, dir string, otherRepo, stopLocal bool) (*DirPlanDTO, error) {
	core := a.snapshot()
	a.mu.Lock()
	e, err := a.find(machine, key)
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if err := refuseOldCopy(core, e); err != nil {
		return nil, err
	}
	if dir == "" {
		return nil, errors.New("choose a folder")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	o := app.DirOptions{OtherRepo: otherRepo, StopLocal: stopLocal}
	dirs.mu.Lock()
	defer dirs.mu.Unlock()
	dirs.clearLocked()
	if machine != app.LocalName() {
		rd, err := core.PlanChangeDirOn(ctx, machine, e.Session.Key, dir, o)
		if err != nil {
			return nil, err
		}
		dirs.remote = rd
		return &DirPlanDTO{Machine: machine, Change: rd.Change}, nil
	}
	inv, fresh, err := core.FreshSelection(ctx, e)
	if err != nil {
		return nil, err
	}
	c, err := core.PlanChangeDir(ctx, inv, fresh, dir, o)
	if err != nil {
		inv.Close()
		return nil, err
	}
	dirs.local, dirs.inv = c, inv
	return &DirPlanDTO{Machine: machine, Local: true, Change: *c}, nil
}

// DirSuggestions are the other checkouts of a session's repository on its machine (its
// worktrees and the folders of its other sessions there), for Change folder….
func (a *App) DirSuggestions(machine, key string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	e, err := a.find(machine, key)
	if err != nil || a.inv == nil {
		return []string{}
	}
	seen := map[string]bool{filepath.Clean(e.Session.CWD): true}
	var out []string
	add := func(p string) {
		if p = filepath.Clean(p); p != "." && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if e.Git != nil {
		for _, w := range e.Git.Worktrees {
			add(w.Path)
		}
	}
	for _, o := range a.inv.Entries {
		if o.Machine == machine && o.Git != nil && e.Git != nil && o.Git.Identity != "" && o.Git.Identity == e.Git.Identity {
			add(o.Git.Toplevel)
		}
	}
	sort.Strings(out)
	if out == nil {
		out = []string{}
	}
	return out
}

// ChangeDir makes the planned change and returns the journal (Activity undoes it).
func (a *App) ChangeDir() (string, error) {
	dirs.mu.Lock()
	defer dirs.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var id string
	var err error
	switch {
	case dirs.remote != nil:
		id, err = dirs.remote.Apply(ctx)
	case dirs.local != nil:
		id, err = a.snapshot().ChangeDir(ctx, dirs.local)
	default:
		return "", errors.New("plan the folder change first")
	}
	dirs.clearLocked()
	return id, err
}

// WaitChangedDir waits, at most seconds, for the open session's agent to move it (the user
// typed the plan's InSession in it). CancelChangeDir stops waiting.
func (a *App) WaitChangedDir(seconds int) error {
	dirs.mu.Lock()
	local, remote := dirs.local, dirs.remote
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(max(seconds, 1))*time.Second)
	dirs.cancel = cancel
	dirs.mu.Unlock()
	defer cancel()
	var err error
	switch {
	case remote != nil:
		err = remote.Wait(ctx, time.Duration(max(seconds, 1))*time.Second)
	case local != nil:
		err = a.snapshot().WaitChangedDir(ctx, local, time.Second)
	default:
		return errors.New("plan the folder change first")
	}
	return err
}

// CancelChangeDir stops waiting and forgets the planned change.
func (a *App) CancelChangeDir() {
	dirs.mu.Lock()
	defer dirs.mu.Unlock()
	if dirs.cancel != nil {
		dirs.cancel()
		dirs.cancel = nil
	}
	dirs.clearLocked()
}
