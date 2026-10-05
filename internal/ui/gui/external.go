package gui

import (
	"context"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/termapp"
)

// How launches in the user's terminal app ended, where that terminal can say (iTerm2 with
// its Python API on, see App.WatchLaunch): the exit code comes from hopsesh's own records
// (the ticket's exit, the step's outcome), -1 when the tab was closed first. The window
// shows it as "exited N" or "closed" beside the session, and on the hand-off's step.

// ExternalExitEvent carries an ExternalExitDTO to the window.
const ExternalExitEvent = "hopsesh:external-exit"

// ExternalExitDTO is a launch in the user's terminal app that ended.
type ExternalExitDTO struct {
	Kind     string `json:"kind"`              // session, step, sign-in
	Machine  string `json:"machine,omitempty"` // a session's row
	Key      string `json:"key,omitempty"`
	Title    string `json:"title"`
	Terminal string `json:"terminal"` // "iTerm2"
	Code     int    `json:"code"`     // the exit code; -1 when the tab was closed first
	Closed   bool   `json:"closed"`
	At       string `json:"at"` // RFC 3339
}

// watchMax is how long hopsesh watches a launch in the user's terminal app.
const watchMax = 24 * time.Hour

var (
	exitsMu sync.Mutex
	exits   = map[string]ExternalExitDTO{} // by session (machine and key), or step title
)

// watchLaunch follows a launch opened in the user's terminal app, when that terminal can
// report its end, and tells the window how it ended.
func (a *App) watchLaunch(o termapp.Opened, l app.Launch) {
	ctx, cancel := context.WithTimeout(context.Background(), watchMax)
	ch, err := a.snapshot().WatchLaunch(ctx, o.Handle)
	if err != nil {
		cancel()
		return
	}
	d := ExternalExitDTO{Kind: string(l.Kind), Title: l.Labels.Title, Terminal: o.Terminal}
	if l.Kind == termapp.KindSession && l.Key.Session != "" {
		d.Machine, d.Key = app.LocalName(), l.Key.String()
	}
	go func() {
		defer cancel()
		code, ok := <-ch
		if !ok {
			return
		}
		a.noteExit(d, code)
	}()
}

// noteExit records and announces how a launch in the user's terminal app ended.
func (a *App) noteExit(d ExternalExitDTO, code int) {
	d.Code, d.Closed, d.At = code, code < 0, time.Now().UTC().Format(time.RFC3339)
	exitsMu.Lock()
	exits[d.Machine+"\x00"+d.Key+"\x00"+d.Title] = d
	exitsMu.Unlock()
	a.emit(ExternalExitEvent, d)
}

// ExternalExits are the launches in the user's terminal app that ended since the app
// started (where that terminal could say).
func (a *App) ExternalExits() []ExternalExitDTO {
	exitsMu.Lock()
	defer exitsMu.Unlock()
	out := make([]ExternalExitDTO, 0, len(exits))
	for _, d := range exits {
		out = append(out, d)
	}
	return out
}
