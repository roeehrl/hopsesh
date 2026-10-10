package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/repos"
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
	if j.PartOf != "" && j.ID != match {
		// A leg of a hop: undo the whole hop (a leg named by its own id is undone alone).
		if hop, err := journal.Load(a.StateDir, j.PartOf); err == nil && !hop.Undone {
			j = hop
		}
	}
	machines := map[string]*host.Machine{}
	defer func() {
		for _, m := range machines {
			m.Close()
		}
	}()
	connect := func(name string) (*host.Machine, error) {
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
		return m, nil
	}
	fsFor := func(name string) (host.FS, error) {
		if name == LocalName() {
			return host.LocalFS(), nil
		}
		m, err := connect(name)
		if err != nil {
			return nil, err
		}
		return m.FS(ctx)
	}
	reach := journal.Reach{FS: fsFor, Refs: a.refsReach(connect), Clouds: a.undoClouds(), Git: repos.LocalGit{}}
	if len(j.Parts) > 0 {
		err := a.undoHop(ctx, j, reach, force)
		a.Audit.Write(audit.Entry{Action: "undo", Detail: map[string]any{"journal": j.ID, "ok": err == nil, "force": force, "parts": j.Parts}})
		return j, err
	}
	if !force {
		if err := j.Changed(ctx, reach); err != nil {
			return j, err
		}
	}
	// The other machines' part first: when one cannot be undone, nothing here is either.
	for _, r := range j.Remote {
		if err := a.undoRemote(ctx, r, force); err != nil {
			return j, fmt.Errorf("undo on %s failed, so nothing was undone here: %w", r.Machine, err)
		}
	}
	err = j.Undo(ctx, reach, force)
	a.Audit.Write(audit.Entry{Action: "undo", Detail: map[string]any{"journal": j.ID, "ok": err == nil, "force": force}})
	return j, err
}

// Activity is one operation hopsesh carried out, and whether it can be undone now.
type Activity struct {
	Journal *journal.Journal `json:"journal"`
	CanUndo bool             `json:"canUndo"`
	Why     string           `json:"why,omitempty"` // why not: undone, or what changed since
	// Part: it is a leg of a hop (Journal.PartOf), undone with it.
	Part bool `json:"part,omitempty"`
}

// Activities lists what hopsesh did, newest first. Whether each can still be undone is
// checked on this machine's files only (other machines, remotes and clouds are checked
// when undoing).
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
	byID := map[string]*journal.Journal{}
	for _, j := range js {
		byID[j.ID] = j
	}
	reach := journal.Reach{FS: local, Git: repos.LocalGit{}}
	for _, j := range js {
		act := Activity{Journal: j, CanUndo: !j.Undone, Part: j.PartOf != ""}
		switch {
		case j.Undone:
			act.Why = "undone"
		case len(j.Parts) > 0:
			// A hop can be undone while each of its legs can.
			var later []*journal.Journal
			for i := len(j.Parts) - 1; i >= 0; i-- {
				leg := byID[j.Parts[i]]
				if leg == nil || leg.Undone {
					continue
				}
				if err := leg.ChangedBesides(context.Background(), reach, later); err != nil {
					act.CanUndo, act.Why = false, strings.TrimPrefix(err.Error(), journal.ErrChanged.Error()+": ")
					break
				}
				later = append(later, leg)
			}
		default:
			if err := j.Changed(context.Background(), reach); err != nil {
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
