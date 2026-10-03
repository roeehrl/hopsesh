package gui

import (
	"context"
	"errors"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
)

// ActivityDTO is one operation in the Activity list.
type ActivityDTO struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"` // move | continue | push | mark
	Title   string   `json:"title"`
	When    string   `json:"when"` // RFC 3339
	Changes int      `json:"changes"`
	Remote  []string `json:"remote"` // other machines whose part it undoes too
	CanUndo bool     `json:"canUndo"`
	Undone  bool     `json:"undone"`
	Why     string   `json:"why,omitempty"` // why it cannot be undone now
}

// OwedDTO is a mark waiting for a copy left behind to end.
type OwedDTO struct {
	Title    string `json:"title"`
	Location string `json:"location"`
	Mark     string `json:"mark"` // "moved to studio", "continued in Codex on studio"
	Since    string `json:"since"`
}

// ActivityListDTO is the Activity screen.
type ActivityListDTO struct {
	Items []ActivityDTO `json:"items"`
	Owed  []OwedDTO     `json:"owed"`
}

// Activity lists what hopsesh did, newest first, and the marks still owed.
func (a *App) Activity() (*ActivityListDTO, error) {
	core := a.snapshot()
	acts, err := core.Activities()
	if err != nil {
		return nil, err
	}
	out := &ActivityListDTO{Items: []ActivityDTO{}, Owed: []OwedDTO{}}
	for _, x := range acts {
		j := x.Journal
		d := ActivityDTO{ID: j.ID, Kind: j.Kind, Title: j.Title, When: j.Time.Format(time.RFC3339), Changes: len(j.Entries),
			Remote: []string{}, CanUndo: x.CanUndo, Undone: j.Undone, Why: x.Why}
		for _, r := range j.Remote {
			d.Remote = append(d.Remote, r.Machine)
		}
		out.Items = append(out.Items, d)
	}
	owed, err := core.OwedMarks()
	if err != nil {
		return nil, err
	}
	for _, p := range owed {
		out.Owed = append(out.Owed, OwedDTO{Title: p.Title, Location: p.Location, Mark: app.MarkWords(p.Mark), Since: p.Time.Format(time.RFC3339)})
	}
	return out, nil
}

// UndoLast undoes the newest operation that can still be undone and returns its title.
func (a *App) UndoLast() (string, error) {
	core := a.snapshot()
	acts, err := core.Activities()
	if err != nil {
		return "", err
	}
	for _, x := range acts {
		if x.CanUndo {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			_, err := core.Undo(ctx, x.Journal.ID, false)
			return x.Journal.Title, err
		}
	}
	return "", errors.New("nothing to undo")
}
