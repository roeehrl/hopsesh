package gui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Clouds in the window: the sidebar's Clouds group and the cloud scope, cloud rows and
// their inspector, the plan sheet and done screen of bringing one here, and the Machines
// page's cloud cards.

// CloudDTO is one cloud as the window shows it (only clouds hopsesh can do something
// with).
type CloudDTO struct {
	Name      string `json:"name"`
	Title     string `json:"title"`
	Agent     string `json:"agent"`
	AgentName string `json:"agentName"`
	Driver    string `json:"driver"`
	Version   string `json:"version"`
	Tested    bool   `json:"tested"`
	TestedOn  string `json:"testedOn"` // the driver versions hopsesh tested ("2.1")
	Status    string `json:"status"`   // app.Cloud* statuses
	Hint      string `json:"hint"`
	Error     string `json:"error"`
	Allowed   bool   `json:"allowed"`
	Sessions  int    `json:"sessions"`
	Mirrors   int    `json:"mirrors"`
	Partial   bool   `json:"partial"`
	Listable  bool   `json:"listable"`
	Fetchable bool   `json:"fetchable"`
	// Rename: the cloud's own branches come home under hopsesh/from/<cloud>/.
	Rename bool `json:"rename"`
	// VendorPrefix starts the cloud's own branches ("claude/").
	VendorPrefix string `json:"vendorPrefix"`
	// Fidelity is what comes back of a conversation (native, code, text).
	Fidelity string `json:"fidelity"`
	// CodeOnly: hopsesh brings only the code of this cloud's sessions (it writes a
	// conversation here only from a native copy, so far); CodeDown says how the code comes
	// (pr, branch, diff).
	CodeOnly bool     `json:"codeOnly"`
	CodeDown []string `json:"codeDown"`
	// Test is the last read-only probe of it, when there was one.
	Test *CloudTestDTO `json:"test,omitempty"`
}

// CloudEntryDTO is what a cloud row adds to a session row.
type CloudEntryDTO struct {
	Name     string `json:"name"`  // the cloud
	Title    string `json:"title"` // "Claude Code cloud"
	ID       string `json:"id"`
	URL      string `json:"url"`
	State    string `json:"state"`
	PR       string `json:"pr,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Base     string `json:"base,omitempty"`
	Repo     string `json:"repo,omitempty"`
	Checkout string `json:"checkout,omitempty"`
}

// MirrorDTO is a local session's copy on the vendor's site (Remote Control).
type MirrorDTO struct {
	Cloud string `json:"cloud"`
	URL   string `json:"url"`
	Host  string `json:"host"` // "claude.ai"
}

// CloudTestDTO is a read-only probe of a cloud.
type CloudTestDTO struct {
	OK      bool               `json:"ok"`
	Account string             `json:"account,omitempty"`
	Checks  []agent.CloudCheck `json:"checks"`
	Error   string             `json:"error,omitempty"`
	At      string             `json:"at"` // RFC 3339
}

// tests are the last probes, by cloud, for the cards.
var (
	testsMu sync.Mutex
	tests   = map[string]*CloudTestDTO{}
)

func cloudDTO(core *app.App, c *app.Cloud) CloudDTO {
	d := CloudDTO{CodeDown: []string{}, Name: c.Name, Title: c.Title, Agent: string(c.Agent), AgentName: c.AgentName, Driver: c.Driver, Version: c.Version,
		Tested: c.Tested, Status: c.Status, Hint: c.Hint, Error: c.Error, Allowed: c.Allowed, Sessions: c.Sessions, Mirrors: c.Mirrors,
		Partial: c.Partial, Listable: c.Listable, Fetchable: c.Fetchable, Rename: *core.Cfg.CloudSettings(c.Name).RenameVendorBranches}
	if m, ok := core.Module(c.Agent); ok {
		if cl, ok := m.Spec().FindCloud(c.Name); ok {
			d.TestedOn, d.VendorPrefix, d.Fidelity = strings.Join(cl.Tested, ", "), cl.VendorPrefix, string(cl.Down)
			d.CodeOnly = cl.Down != agent.FidNative
			for _, w := range cl.CodeDown {
				d.CodeDown = append(d.CodeDown, string(w))
			}
		}
	}
	testsMu.Lock()
	d.Test = tests[c.Name]
	testsMu.Unlock()
	return d
}

// shownClouds are the scanned clouds hopsesh can do something with.
func shownClouds(core *app.App, inv *app.Inventory) []CloudDTO {
	out := []CloudDTO{}
	for _, c := range inv.Clouds {
		if c.Listable || c.Fetchable {
			out = append(out, cloudDTO(core, c))
		}
	}
	return out
}

// mirrorDTO is a session's mirror for its row.
func mirrorDTO(mr *agent.CloudLink) *MirrorDTO {
	if mr == nil {
		return nil
	}
	host := mr.Cloud
	if rest, ok := strings.CutPrefix(mr.URL, "https://"); ok {
		host, _, _ = strings.Cut(rest, "/")
	}
	return &MirrorDTO{Cloud: mr.Cloud, URL: mr.URL, Host: host}
}

func cloudEntryDTO(core *app.App, e app.Entry) *CloudEntryDTO {
	c := e.Cloud
	if c == nil {
		return nil
	}
	d := &CloudEntryDTO{Name: e.Location.Name, Title: e.Location.Name, ID: string(c.Key.Session), URL: c.URL, State: string(c.State), PR: c.PR,
		Branch: c.Branch, Base: c.Base, Repo: c.Repo, Checkout: e.Checkout}
	if m, ok := core.Module(e.Agent); ok {
		if cl, ok := m.Spec().FindCloud(e.Location.Name); ok {
			d.Title = cl.Title
		}
	}
	return d
}

// SetCloudAllowed records consent for a cloud: hopsesh runs its agent's cloud commands
// only once it is allowed.
func (a *App) SetCloudAllowed(name string, allowed bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.core.IsCloud(name) {
		return fmt.Errorf("unknown cloud %q", name)
	}
	if a.core.Cfg.Clouds == nil {
		a.core.Cfg.Clouds = map[string]config.Cloud{}
	}
	a.core.Cfg.SetCloudAllowed(name, allowed)
	return a.save()
}

// TestCloud probes a cloud through its agent's command, read-only (the Test button).
func (a *App) TestCloud(name string) (*CloudTestDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	t, err := a.snapshot().TestCloud(ctx, name)
	d := &CloudTestDTO{OK: err == nil, Account: t.Account, Checks: t.Checks, At: time.Now().Format(time.RFC3339)}
	if d.Checks == nil {
		d.Checks = []agent.CloudCheck{}
	}
	for _, c := range t.Checks {
		d.OK = d.OK && c.OK
	}
	if err != nil {
		d.Error = err.Error()
		for _, e := range []error{agent.ErrSignedOut, agent.ErrNotEligible, agent.ErrUnsupported} {
			d.Error = strings.TrimPrefix(d.Error, e.Error()+": ")
		}
	}
	testsMu.Lock()
	tests[name] = d
	testsMu.Unlock()
	return d, nil
}

// CheckoutDTO is a repository checked out here, for choosing where a cloud session comes.
type CheckoutDTO struct {
	Name     string `json:"name"`
	Identity string `json:"identity"`
	Path     string `json:"path"`
}

// Checkouts are the repositories the last scan found checked out here.
func (a *App) Checkouts() []CheckoutDTO {
	core := a.snapshot()
	a.mu.Lock()
	inv := a.inv
	a.mu.Unlock()
	out := []CheckoutDTO{}
	if inv == nil {
		return out
	}
	for _, g := range inv.Groups(core.LocalRoots()) {
		if g.Local != "" {
			out = append(out, CheckoutDTO{Name: g.Name, Identity: g.Identity, Path: g.Local})
		}
	}
	return out
}

// PastedDTO is a pasted cloud link, now a row (machine and key as the row's).
type PastedDTO struct {
	Cloud string `json:"cloud"`
	Key   string `json:"key"`
}

// PasteCloud remembers a cloud session by its link or id, with the checkout here it works
// on (chosen from Checkouts, "" when not known yet); the next scan lists it.
func (a *App) PasteCloud(link, checkout string) (*PastedDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	core := a.snapshot()
	p, err := core.Paste(ctx, link, checkout)
	if err != nil {
		return nil, err
	}
	mod, _, _, _ := core.ParseCloudLink(link)
	return &PastedDTO{Cloud: p.Cloud, Key: agent.SessionKey{Agent: mod.Spec().ID, Session: p.ID}.String()}, nil
}

// PlanPicked plans bringing a cloud session that is not a row: id "" leaves the choice to
// the vendor's own picker (Find in Claude Code…), into the checkout chosen here.
func (a *App) PlanPicked(cloud, id, checkout, target string, o OptsDTO) (*PlanDTO, error) {
	core := a.snapshot()
	a.mu.Lock()
	inv := a.inv
	a.mu.Unlock()
	if inv == nil {
		return nil, errors.New("scan first")
	}
	e, err := inv.CloudEntry(core, cloud, agent.SessionID(id))
	if err != nil {
		return nil, err
	}
	if checkout != "" {
		e.Checkout = checkout
	}
	return a.planEntry(core, inv, e, target, o)
}

// fetchPlanDTO is a fetch plan as the window shows it.
func fetchPlanDTO(p *move.Plan, e app.Entry) *PlanDTO {
	d := &PlanDTO{Kind: p.Kind, Title: p.Title, Agent: p.Agent, FromAgent: e.AgentName, SourceHost: p.Source.Location, TargetCWD: p.Target.CWD,
		Warnings: p.Warnings, Blockers: p.Blockers, Conflict: p.Conflict, Mark: p.Mark, Repo: p.Repo, Mappings: []agent.Mapping{},
		Options: p.Options, SessionKey: p.Key, SourceAgent: e.Agent, Fetch: p.Fetch, Machine: ""}
	if d.Warnings == nil {
		d.Warnings = []string{}
	}
	if d.Blockers == nil {
		d.Blockers = []string{}
	}
	return d
}

// BroughtDTO is a fetch from a cloud: waiting for the user's terminal, or what came back
// (app.Brought, the command line's shape too).
type BroughtDTO = app.Brought

// AdoptStatus looks for the copy the agent's own command wrote for a fetch, adopts it
// once it is there, and says where it stands (the done screen's wait).
func (a *App) AdoptStatus(journal string) (*BroughtDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	core := a.snapshot()
	f, err := core.Adopt(ctx, journal, false)
	if f == nil {
		return nil, err
	}
	b := core.Brought(f)
	return &b, err
}

// KeepPartial keeps a partial copy as it is.
func (a *App) KeepPartial(journal string) error { return a.snapshot().KeepPartial(journal) }

// OpenBrought opens a fetch's next command in a terminal: the agent's own command while
// it waits, the command that resumes the copy once it is here.
func (a *App) OpenBrought(journal string) error {
	f, err := move.LoadFetch(a.snapshot().StateDir, journal)
	if err != nil {
		return err
	}
	b := a.snapshot().Brought(f)
	if b.Command == "" || b.Outcome == move.FetchEmpty {
		return errors.New("there is nothing to open")
	}
	return terminal(b.Command)
}

// cloudPage reports whether url is a page hopsesh may open for a cloud: a session's page
// as its module makes it, or an upstream problem a cloud declares.
// listedPage reports whether url is the page of a cloud session the last scan listed (a
// cloud's module gave it, as for a Copilot task's pull request).
func (a *App) listedPage(url string) bool {
	if !strings.HasPrefix(url, "https://") {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inv == nil {
		return false
	}
	for _, e := range a.inv.Entries {
		if e.Cloud != nil && e.Cloud.URL == url {
			return true
		}
	}
	return false
}

func cloudPage(core *app.App, url string) bool {
	if _, cl, id, ok := core.ParseCloudLink(url); ok {
		if m, c, ok := cloudModuleOf(core, cl); ok {
			if l, ok := m.(agent.CloudLinker); ok && l.CloudURL(c.Name, id) == url {
				return true
			}
		}
	}
	for _, m := range core.Modules() {
		for _, c := range m.Spec().Clouds {
			for _, u := range c.Problems {
				if u == url {
					return true
				}
			}
		}
	}
	return false
}

func cloudModuleOf(core *app.App, name string) (agent.Module, agent.Cloud, bool) {
	for _, m := range core.Modules() {
		if c, ok := m.Spec().FindCloud(name); ok {
			return m, c, true
		}
	}
	return nil, agent.Cloud{}, false
}

// terminal opens a shell line hopsesh built in a new terminal window; SetTerminal
// replaces it (the browser tests run the line in the background, as a terminal would).
var terminal = openTerminal

// SetTerminal replaces how the window opens a command in a terminal.
func SetTerminal(f func(line string) error) { terminal = f }
