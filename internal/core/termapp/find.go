package termapp

import (
	"context"
	"errors"
	"strings"
)

// Finding the tab a session runs in, for "Show in iTerm2". Identity comes only from the
// agent's own files (its process id), the process table (that process's terminal
// device) and hopsesh's launch records (the device hopsesh's verb started on); never from
// anything a tab shows or a variable it set. The terminal apps are then asked which of
// their tabs has that device.

// Candidate is a process that may run the session: the agent's own (TTY "": whatever
// terminal it is on) or one hopsesh started (TTY: the device it started on, which must
// still be its).
type Candidate struct {
	PID int
	TTY string
}

// Found is a session's tab: the terminal that has it, the tab, and the process on it.
type Found struct {
	Terminal Terminal
	Handle   Handle
	PID      int
}

// Name is the terminal's name, for people ("iTerm2").
func (f Found) Name() string { return f.Terminal.Name() }

// FindTab finds the tab of the first candidate that runs and is on a terminal, asking
// first (the user's terminal) and then the set's other terminals that can find tabs.
// False with a nil error: no tab hopsesh can show (the process is not in a terminal app
// hopsesh knows).
func (s Set) FindTab(ctx context.Context, procs Procs, first Terminal, cands []Candidate) (Found, bool, error) {
	order := []Terminal{}
	if first != nil {
		order = append(order, first)
	}
	for _, t := range s.list {
		if first == nil || t.ID() != first.ID() {
			order = append(order, t)
		}
	}
	var errs []string
	seen := map[int]bool{}
	for _, c := range cands {
		if c.PID <= 0 || seen[c.PID] || !procs.Alive(c.PID) {
			continue
		}
		seen[c.PID] = true
		tty := procs.TTY(ctx, c.PID)
		if tty == "" || (c.TTY != "" && c.TTY != tty) {
			continue // not on a terminal, or no longer on the one hopsesh started it on
		}
		for _, t := range order {
			f, ok := t.(Finder)
			if !ok || t.Available(ctx) != nil {
				continue
			}
			h, found, err := f.FindTTY(ctx, tty)
			if err != nil {
				errs = append(errs, t.Name()+": "+err.Error())
				continue
			}
			if found {
				return Found{Terminal: t, Handle: h, PID: c.PID}, true, nil
			}
		}
	}
	if len(errs) > 0 {
		return Found{}, false, errors.New(strings.Join(errs, "; "))
	}
	return Found{}, false, nil
}

// Show brings a found tab forward, after checking again that its process still runs on
// the same terminal device (a tab's device can be reused once its program ends).
func Show(ctx context.Context, procs Procs, f Found) error {
	if !procs.Alive(f.PID) || procs.TTY(ctx, f.PID) != f.Handle.TTY {
		return ErrGone
	}
	fc, ok := f.Terminal.(Focuser)
	if !ok {
		return errors.New(f.Terminal.Name() + " cannot bring a tab forward")
	}
	return fc.Focus(ctx, f.Handle)
}
