package gui

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/move"
)

// ActivityDTO is one operation in the Activity list.
type ActivityDTO struct {
	PendingReceipt bool     `json:"pendingReceipt"`
	ID             string   `json:"id"`
	Kind           string   `json:"kind"` // move | continue | push | fetch | handoff | hop | …
	Title          string   `json:"title"`
	When           string   `json:"when"` // RFC 3339
	Changes        int      `json:"changes"`
	Remote         []string `json:"remote"` // other machines whose part it undoes too
	CanUndo        bool     `json:"canUndo"`
	Undone         bool     `json:"undone"`
	Why            string   `json:"why,omitempty"` // why it cannot be undone now
	// Fetch is what a fetch from a cloud brought (kind fetch).
	Fetch *BroughtDTO `json:"fetch,omitempty"`
	// Handoff is where a hand-off went (kind handoff).
	Handoff *HandoffActivityDTO `json:"handoff,omitempty"`
	// Part: a leg of a hop, undone with it (PartOf); Parts, a hop's legs.
	Part   bool     `json:"part,omitempty"`
	PartOf string   `json:"partOf,omitempty"`
	Parts  []string `json:"parts,omitempty"`
	// Hop is a hop from one cloud to another (kind hop).
	Hop *move.HopResult `json:"hop,omitempty"`
}

// HandoffActivityDTO is a hand-off in the Activity list.
type HandoffActivityDTO struct {
	Cloud      string `json:"cloud"`
	CloudTitle string `json:"cloudTitle"`
	Machine    string `json:"machine"`
	Session    string `json:"session"`
	URL        string `json:"url"`
	Branch     string `json:"branch"`
	Pushed     bool   `json:"pushed"`
	Noun       string `json:"noun"` // what the cloud calls its sessions ("task")
}

// ActivityListDTO is the Activity screen.
type ActivityListDTO struct {
	Items []ActivityDTO `json:"items"`
	// Waiting are fetches whose copy the agent's own command has not written yet.
	Waiting []BroughtDTO `json:"waiting"`
}

// Activity lists what hopsesh did, newest first.
func (a *App) Activity() (*ActivityListDTO, error) {
	core := a.snapshot()
	acts, err := core.Activities()
	if err != nil {
		return nil, err
	}
	out := &ActivityListDTO{Items: []ActivityDTO{}, Waiting: []BroughtDTO{}}
	fetches := map[string]BroughtDTO{}
	if fs, err := core.Fetches(); err == nil {
		for _, f := range fs {
			b := core.Brought(f)
			fetches[f.Journal] = b
			if f.Waiting() {
				out.Waiting = append(out.Waiting, b)
			}
		}
	}
	for _, x := range acts {
		j := x.Journal
		d := ActivityDTO{ID: j.ID, Kind: j.Kind, Title: j.Title, When: j.Time.Format(time.RFC3339), Changes: len(j.Entries),
			PendingReceipt: j.PendingReceipts(), Remote: []string{}, CanUndo: x.CanUndo, Undone: j.Undone, Why: x.Why, Part: x.Part, PartOf: j.PartOf, Parts: j.Parts}
		if j.Kind == "hop" {
			if hop, err := core.LoadHop(j.ID); err == nil {
				r := hop.Result
				d.Hop = &r
			}
		}
		for _, r := range j.Remote {
			d.Remote = append(d.Remote, r.Machine)
		}
		if j.Kind == "handoff" {
			if ho, err := move.LoadHandoff(core.StateDir, j.ID); err == nil {
				d.Handoff = &HandoffActivityDTO{Cloud: ho.Cloud, CloudTitle: ho.Cloud, Machine: ho.Machine, Session: string(ho.Session.Session), URL: ho.URL,
					Branch: ho.Branch, Pushed: ho.Pushed, Noun: "session"}
				if _, cl, ok := cloudModuleOf(core, ho.Cloud); ok {
					d.Handoff.CloudTitle, d.Handoff.Noun = cl.Title, cl.SessionNoun()
				}
			}
		}
		if b, ok := fetches[j.ID]; ok {
			d.Fetch = &b
			if j.Undone {
				out.Waiting = slices.DeleteFunc(out.Waiting, func(w BroughtDTO) bool { return w.Journal == j.ID })
			}
		}
		out.Items = append(out.Items, d)
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
		if x.CanUndo && !x.Part { // a hop's leg goes with its hop
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			_, err := core.Undo(ctx, x.Journal.ID, false)
			return x.Journal.Title, err
		}
	}
	return "", errors.New("nothing to undo")
}

// RetryReceipts repairs pending metadata without relaunching a native or cloud agent.
func (a *App) RetryReceipts(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return a.snapshot().RecoverReceipts(ctx, id)
}
