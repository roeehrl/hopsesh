package gui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/pty"
)

// Reopening: quitting ends the programs in the terminal's tabs (the app owns them), and
// the agents keep each conversation up to its last finished turn. So when the app quits,
// it writes down the sessions its tabs ran; the next launch offers to resume them, each in
// a tab again, and after an update it does so without asking (the update's own dialog
// said it would).

// ReopenTab is a session a tab ran when the app quit.
type ReopenTab struct {
	Kind    string `json:"kind"` // TabSession, or TabRemote: a terminal hopsesh runs on Machine
	Machine string `json:"machine"`
	Key     string `json:"key,omitempty"`
	Remote  string `json:"remote,omitempty"` // TabRemote: the terminal there, which may still run
	Agent   string `json:"agent,omitempty"`
	Title   string `json:"title"`
	Dir     string `json:"dir,omitempty"`
	// Working: its program was busy (a turn in progress, which the agent did not keep).
	Working bool `json:"working,omitempty"`
}

// ReopenDTO is what the last quit left to reopen.
type ReopenDTO struct {
	At time.Time `json:"at"`
	// Update: an update restarted the app, and the window reopens the tabs without asking.
	Update bool        `json:"update"`
	Tabs   []ReopenTab `json:"tabs"`
}

// ReopenedDTO is what reopening did: the tabs it opened, and why the others did not.
type ReopenedDTO struct {
	Opened int      `json:"opened"`
	Failed []string `json:"failed"`
}

// reopenStale is how old a list may be and still be offered.
const reopenStale = 7 * 24 * time.Hour

func reopenPath() string { return filepath.Join(config.StateDir(), "reopen.json") }

// readReopen is the list the last quit left (nil: none, unreadable, or stale).
func readReopen() *ReopenDTO {
	b, err := os.ReadFile(reopenPath())
	if err != nil {
		return nil
	}
	var r ReopenDTO
	if json.Unmarshal(b, &r) != nil || time.Since(r.At) > reopenStale || len(r.Tabs) == 0 {
		return nil
	}
	return &r
}

// reopenable is what tabs leaves to reopen: the sessions whose programs still run (a tab
// that ended has nothing left to stop; a new session not yet bound has no key to resume).
func reopenable(tabs []TermTab) []ReopenTab {
	var out []ReopenTab
	for _, t := range tabs {
		if t.State == pty.Exited || t.State == "" {
			continue
		}
		r := ReopenTab{Kind: t.Kind, Machine: t.Machine, Key: t.Key, Agent: t.Agent, Title: t.Title, Dir: t.Dir, Working: t.State == pty.Running}
		switch {
		case t.Kind == TabSession && t.Key != "" && t.Association != associationChecking:
		case t.Kind == TabBring && t.Saved != "" && t.Key != "":
			r.Kind = TabSession // the copy it saved here
		case t.Kind == TabRemote && t.Agent != "" && (t.Key != "" || t.Remote != ""):
			r.Remote = t.Remote
		default:
			continue
		}
		out = append(out, r)
	}
	return out
}

// rememberTabs writes down the sessions the tabs run, once, before they end (update: an
// update restarts the app). A list the window has not answered yet is kept too, so
// quitting before answering does not lose it.
func (a *App) rememberTabs(update bool) {
	if a.Terms == nil || !a.remembered.CompareAndSwap(false, true) {
		return
	}
	tabs := reopenable(a.Terms.Tabs())
	if old := readReopen(); old != nil {
		for _, o := range old.Tabs {
			if !slices.ContainsFunc(tabs, func(t ReopenTab) bool { return t.Machine == o.Machine && t.Key == o.Key && t.Remote == o.Remote }) {
				tabs = append(tabs, o)
			}
		}
	}
	if len(tabs) == 0 {
		_ = os.Remove(reopenPath())
		return
	}
	b, err := json.Marshal(ReopenDTO{At: time.Now(), Update: update, Tabs: tabs})
	if err == nil {
		_ = os.WriteFile(reopenPath(), b, 0o600)
	}
}

// ReopenOffer is what the last quit left to reopen (nil: nothing). The window asks once
// its first scan is in, and answers with Reopen or ForgetReopen.
func (a *App) ReopenOffer() *ReopenDTO { return readReopen() }

// ForgetReopen drops the list (the user said not now).
func (a *App) ForgetReopen() { _ = os.Remove(reopenPath()) }

// Reopen resumes each session the last quit left, in a tab: one here with its agent's
// resume command, one on another machine shown again (still running there, or continued
// there again). The list is gone afterwards, whatever opened.
func (a *App) Reopen() (*ReopenedDTO, error) {
	r := readReopen()
	if r == nil {
		return &ReopenedDTO{Failed: []string{}}, nil
	}
	_ = os.Remove(reopenPath())
	out := &ReopenedDTO{Failed: []string{}}
	for _, t := range r.Tabs {
		if err := a.reopenTab(t); err != nil {
			out.Failed = append(out.Failed, t.Title+": "+err.Error())
			continue
		}
		out.Opened++
	}
	return out, nil
}

func (a *App) reopenTab(t ReopenTab) error {
	switch t.Kind {
	case TabSession:
		if t.Machine != app.LocalName() {
			return errors.New("it ran on " + t.Machine + ", not here")
		}
		_, err := a.ResumeSession(t.Machine, t.Key, WhereHere)
		return err
	case TabRemote:
		if t.Remote != "" {
			if ok, err := a.reattach(t); ok || err != nil {
				return err
			}
		}
		if t.Key == "" {
			return errors.New("it ended on " + t.Machine)
		}
		_, err := a.OpenOnMachine(t.Machine, t.Key)
		return err
	}
	return errors.New("not a session")
}
