package gui

import (
	"maps"
	"slices"

	"github.com/roeehrl/hopsesh/internal/config"
)

// The session list's display ([list] in the config): grouping, sorting, row density, the
// groups the user collapsed or expanded, and the filters; and where each agent's sessions
// resume, as last chosen from a Resume menu ([agents.<id>] place).

// ListDTO is the list's display. Decided is false until the app (on the first scan) or
// the user chose: the window then picks rows by how many sessions there are.
type ListDTO struct {
	Decided          bool      `json:"decided"`
	GroupBy          string    `json:"groupBy"`
	SortBy           string    `json:"sortBy"`
	SortReverse      bool      `json:"sortReverse"`
	Density          string    `json:"density"`
	CollapseInactive bool      `json:"collapseInactive"`
	Collapsed        []string  `json:"collapsed"`
	Expanded         []string  `json:"expanded"`
	Filter           FilterDTO `json:"filter"`
}

// FilterDTO is the list's saved filters (config.ListFilter).
type FilterDTO struct {
	Account       []string `json:"account"`
	AccountNot    bool     `json:"accountNot"`
	Tag           []string `json:"tag"`
	TagNot        bool     `json:"tagNot"`
	Status        []string `json:"status"`
	StatusNot     bool     `json:"statusNot"`
	Location      []string `json:"location"`
	LocationNot   bool     `json:"locationNot"`
	Agent         []string `json:"agent"`
	AgentNot      bool     `json:"agentNot"`
	Repository    []string `json:"repository"`
	RepositoryNot bool     `json:"repositoryNot"`
	LastActive    string   `json:"lastActive"`
	Has           []string `json:"has"`
	HasNot        bool     `json:"hasNot"`
}

func listOf(l config.List) ListDTO {
	f := l.Filter
	return ListDTO{Decided: l.Density != "", GroupBy: nonEmpty(l.GroupBy, "repository"), SortBy: nonEmpty(l.SortBy, "last-active"), SortReverse: l.SortReverse,
		Density: nonEmpty(l.Density, "comfortable"), CollapseInactive: l.CollapseInactive, Collapsed: nonNil(l.Collapsed), Expanded: nonNil(l.Expanded),
		Filter: FilterDTO{Account: nonNil(f.Account), AccountNot: f.AccountNot, Tag: nonNil(f.Tag), TagNot: f.TagNot, Status: nonNil(f.Status), StatusNot: f.StatusNot, Location: nonNil(f.Location), LocationNot: f.LocationNot,
			Agent: nonNil(f.Agent), AgentNot: f.AgentNot, Repository: nonNil(f.Repository), RepositoryNot: f.RepositoryNot,
			LastActive: f.LastActive, Has: nonNil(f.Has), HasNot: f.HasNot}}
}

// SaveList stores the list's display. Values the app does not know are refused; the
// collapsed and expanded groups keep the newest ListKeysMax.
func (a *App) SaveList(d ListDTO) error {
	newest := func(s []string) []string {
		s = slices.Compact(slices.Clone(s))
		if len(s) > config.ListKeysMax {
			s = s[len(s)-config.ListKeysMax:]
		}
		return empty(s)
	}
	f := d.Filter
	l := config.List{GroupBy: d.GroupBy, SortBy: d.SortBy, SortReverse: d.SortReverse, Density: d.Density, CollapseInactive: d.CollapseInactive,
		Collapsed: newest(d.Collapsed), Expanded: newest(d.Expanded),
		Filter: config.ListFilter{Account: empty(f.Account), AccountNot: f.AccountNot, Tag: empty(f.Tag), TagNot: f.TagNot, Status: empty(f.Status), StatusNot: f.StatusNot, Location: empty(f.Location), LocationNot: f.LocationNot,
			Agent: empty(f.Agent), AgentNot: f.AgentNot, Repository: empty(f.Repository), RepositoryNot: f.RepositoryNot,
			LastActive: f.LastActive, Has: empty(f.Has), HasNot: f.HasNot}}
	if l.Density == "" {
		l.Density = "comfortable" // saved once: the app has chosen
	}
	if l.GroupBy == "repository" {
		l.GroupBy = ""
	}
	if l.SortBy == "last-active" {
		l.SortBy = ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.core.Cfg
	c.List = l
	if err := c.Check(); err != nil {
		return err
	}
	a.core.Cfg.List = l
	if f := listMenu; f != nil {
		f(nonEmpty(l.GroupBy, "repository"), nonEmpty(l.SortBy, "last-active"), l.Density == "compact")
	}
	return a.save()
}

// empty is nil for a list with nothing in it (left out of the file).
func empty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

var listMenu func(groupBy, sortBy string, compact bool)

// SetListMenu is how the app's View menu learns the list's grouping, sorting and rows.
func SetListMenu(f func(groupBy, sortBy string, compact bool)) { listMenu = f }

// SetPlace remembers where an agent's sessions resume: here (the hopsesh Terminal
// window), terminal (the user's terminal app) or app (the agent's desktop app).
func (a *App) SetPlace(agentID, place string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.core.Cfg
	agents := maps.Clone(c.Agents)
	if agents == nil {
		agents = map[string]config.Agent{}
	}
	ag := agents[agentID]
	ag.Place = place
	agents[agentID] = ag
	c.Agents = agents
	if err := c.Check(); err != nil {
		return err
	}
	a.core.Cfg.Agents = agents
	return a.save()
}

// SetPreviews turns the inspector's conversation previews on or off.
func (a *App) SetPreviews(on bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.core.Cfg.Previews = nil
	if !on {
		off := false
		a.core.Cfg.Previews = &off
	}
	return a.save()
}
