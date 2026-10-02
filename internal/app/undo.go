package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
)

// Journals lists what can be undone, newest first.
func (a *App) Journals() ([]*journal.Journal, error) { return journal.List(a.StateDir) }

// errNothingToUndo starts the error of an Undo that found nothing (also across peers).
const errNothingToUndo = "nothing to undo"

// Undo reverses a journal: by id, by a session id it concerns, or the newest when match
// is "". Writes on other machines are undone over SSH.
func (a *App) Undo(ctx context.Context, match string) (*journal.Journal, error) {
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
	// The other machines' part first: when one cannot be undone, nothing here is either.
	for _, r := range j.Remote {
		if err := a.undoRemote(ctx, r); err != nil {
			return j, fmt.Errorf("undo on %s failed, so nothing was undone here: %w", r.Machine, err)
		}
	}
	machines := map[string]*host.Machine{}
	defer func() {
		for _, m := range machines {
			m.Close()
		}
	}()
	err = j.Undo(func(name string) (host.FS, error) {
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
	})
	a.Audit.Write(audit.Entry{Action: "undo", Detail: map[string]any{"journal": j.ID, "ok": err == nil}})
	return j, err
}

func concerns(j *journal.Journal, match string) bool {
	for _, k := range j.Keys {
		if string(k.Session) == match || len(match) >= 4 && strings.HasPrefix(string(k.Session), match) {
			return true
		}
	}
	return false
}
