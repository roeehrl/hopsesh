package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
)

// Journals lists what can be undone, newest first.
func (a *App) Journals() ([]*journal.Journal, error) { return journal.List(a.StateDir) }

// errNothingToUndo starts the error of an Undo that found nothing (also across peers).
const errNothingToUndo = "nothing to undo"

// Undo reverses a journal: by id, by a session id it concerns, or the newest when match
// is "". Writes on other machines are undone over SSH. Unless force, it refuses when a file
// the operation wrote changed since (that later work would be lost).
func (a *App) Undo(ctx context.Context, match string, force bool) (*journal.Journal, error) {
	js, err := a.Journals()
	if err != nil {
		return nil, err
	}
	var j *journal.Journal
	for _, x := range js {
		if x.Undone {
			continue
		}
		if match == "" || x.ID == match || concerns(x, match) {
			j = x
			break
		}
	}
	if j == nil {
		return nil, errors.New(errNothingToUndo + map[bool]string{true: "", false: " for " + match}[match == ""])
	}
	machines := map[string]*host.Machine{}
	defer func() {
		for _, m := range machines {
			m.Close()
		}
	}()
	fsFor := func(name string) (host.FS, error) {
		if name == LocalName() {
			return host.LocalFS(), nil
		}
		m, ok := machines[name]
		if !ok {
			h := a.Cfg.FindHost(name)
			if h == nil {
				return nil, fmt.Errorf("%s is not a configured machine", name)
			}
			hm, err := a.Connect(ctx, *h)
			if err != nil {
				return nil, err
			}
			machines[name], m = hm, hm
		}
		return m.FS(ctx)
	}
	if !force {
		if err := j.Changed(fsFor); err != nil {
			return j, err
		}
	}
	// The other machines' part first: when one cannot be undone, nothing here is either.
	for _, r := range j.Remote {
		if err := a.undoRemote(ctx, r, force); err != nil {
			return j, fmt.Errorf("undo on %s failed, so nothing was undone here: %w", r.Machine, err)
		}
	}
	err = j.Undo(fsFor, force)
	a.Audit.Write(audit.Entry{Action: "undo", Detail: map[string]any{"journal": j.ID, "ok": err == nil, "force": force}})
	return j, err
}

// OwedMarks are the marks waiting for a copy left behind to end.
func (a *App) OwedMarks() ([]lineage.Pending, error) { return lineage.LoadPending(a.StateDir) }

// Activity is one operation hopsesh carried out, and whether it can be undone now.
type Activity struct {
	Journal *journal.Journal `json:"journal"`
	CanUndo bool             `json:"canUndo"`
	Why     string           `json:"why,omitempty"` // why not: undone, or what changed since
}

// Activities lists what hopsesh did, newest first. Whether each can still be undone is
// checked on this machine's files only (other machines are checked when undoing).
func (a *App) Activities() ([]Activity, error) {
	js, err := a.Journals()
	if err != nil {
		return nil, err
	}
	local := func(name string) (host.FS, error) {
		if name == LocalName() {
			return host.LocalFS(), nil
		}
		return nil, errors.New("not this machine")
	}
	out := make([]Activity, 0, len(js))
	for _, j := range js {
		act := Activity{Journal: j, CanUndo: !j.Undone}
		switch {
		case j.Undone:
			act.Why = "undone"
		default:
			if err := j.Changed(local); err != nil {
				act.CanUndo, act.Why = false, strings.TrimPrefix(err.Error(), journal.ErrChanged.Error()+": ")
			}
		}
		out = append(out, act)
	}
	return out, nil
}

func concerns(j *journal.Journal, match string) bool {
	for _, k := range j.Keys {
		if string(k.Session) == match || len(match) >= 4 && strings.HasPrefix(string(k.Session), match) {
			return true
		}
	}
	return false
}
