package gui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/appicon"
	"github.com/roeehrl/hopsesh/internal/core/convert"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/presence"
	"github.com/roeehrl/hopsesh/internal/core/proc"
	"github.com/roeehrl/hopsesh/internal/core/pty"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/internal/core/termapp"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// ProgressEvent carries the steps of a move or continuation to the window.
const ProgressEvent = "hopsesh:progress"

// MachineDTO summarises one scanned machine.
type MachineDTO struct {
	AccountSetupRequired bool     `json:"accountSetupRequired"` // reachable remote without a persistent endpoint identity
	Name                 string   `json:"name"`
	Status               string   `json:"status"`
	Hint                 string   `json:"hint"`
	Error                string   `json:"error"`
	OS                   string   `json:"os"`
	Sessions             int      `json:"sessions"`
	Local                bool     `json:"local"`
	Agents               []string `json:"agents"`     // "Claude Code 2.1.284"
	AgentNames           []string `json:"agentNames"` // detected agents, without versions or duplicate profiles
	Hopsesh              string   `json:"hopsesh"`    // hopsesh's version there ("" when not installed)
}

// AgentOpt is an agent a session can continue in here.
type AgentOpt struct {
	ID           agent.ID `json:"id"`
	Name         string   `json:"name"`
	Experimental bool     `json:"experimental,omitempty"`
}

// EntryDTO is one session row.
type EntryDTO struct {
	Relationship    app.Relationship      `json:"relationship"`
	ObservedAt      time.Time             `json:"observedAt,omitempty"`
	Returns         []app.ReturnCandidate `json:"returns,omitempty"`
	Movement        *app.MovementNotice   `json:"movement,omitempty"`
	ContextOverflow bool                  `json:"contextOverflow,omitempty"`
	CanApp          bool                  `json:"canApp"`
	AppWhy          string                `json:"appWhy,omitempty"`
	Profile         *agent.RuntimeProfile `json:"profile,omitempty"`
	Machine         string                `json:"machine"`
	Agent           agent.ID              `json:"agent"`
	AgentName       string                `json:"agentName"`
	Key             string                `json:"key"` // agent/session
	Title           string                `json:"title"`
	// TitleSource is where the title comes from: custom (renamed), live (the running
	// agent's name for it), generated (the agent's own title), prompt (the first prompt),
	// reply (the first reply) or none ("Untitled · folder").
	TitleSource string `json:"titleSource"`
	// Session is the agent's session id; Path its file on its machine; AgentVersion the
	// agent version that last wrote it.
	Session      string `json:"session"`
	Path         string `json:"path"`
	AgentVersion string `json:"agentVersion"`
	// CanRename: its agent's own title can be changed (Rename); CanPreview: the end of its
	// conversation can be shown (Preview).
	CanRename  bool       `json:"canRename"`
	CanPreview bool       `json:"canPreview"`
	Places     []PlaceDTO `json:"places"` // where it is open on this machine, besides hopsesh's tabs
	Status     string     `json:"status"`
	Live       bool       `json:"live"`
	LastActive string     `json:"lastActive"`
	LastPrompt string     `json:"lastPrompt"`
	CWD        string     `json:"cwd"`
	Branch     string     `json:"branch"`
	Worktree   string     `json:"worktree"`
	MainBranch string     `json:"mainBranch"`
	Unpushed   int        `json:"unpushed"`
	Dirty      int        `json:"dirty"`
	SizeKB     int64      `json:"sizeKB"`
	Copies     []CopyDTO  `json:"copies,omitempty"` // every copy, across machines and agents
	HereNewest bool       `json:"hereNewest"`       // the newest copy is on this machine
	StaleHere  bool       `json:"staleHere"`        // an older copy is on this machine
	ContinueIn []AgentOpt `json:"continueIn"`       // other agents here it can continue in
	Needs      bool       `json:"needs"`            // the agent waits for the person (an approval)
	// App names the agent's desktop app ("Claude") when that app, not a terminal, runs the
	// open session.
	App               string           `json:"app,omitempty"`
	Journey           *lineage.Journey `json:"journey,omitempty"`
	CanArchiveLineage bool             `json:"canArchiveLineage"`
	LineageError      string           `json:"lineageError,omitempty"`
	History           []HopDTO         `json:"history"` // where it has been, oldest first
	// BringIn is the agent here a cloud-only module's session (Copilot's log, Amp's thread)
	// is written into by default: the one it was handed off from, else Claude Code;
	// ContinueIn then lists the others.
	BringIn *AgentOpt `json:"bringIn,omitempty"`
	// Location is "machine" or "cloud"; a cloud session's row has Cloud (Machine is the
	// cloud's name), and a session the vendor mirrors (Remote Control) has Mirror.
	Location string         `json:"location"`
	Cloud    *CloudEntryDTO `json:"cloud,omitempty"`
	Mirror   *MirrorDTO     `json:"mirror,omitempty"`
	// Handoff are the clouds it can be handed off to (every cloud; a disabled one with its
	// reason); Hop, for a cloud session, the other clouds it can be handed on to through
	// this machine.
	Handoff []app.HandoffTarget `json:"handoff"`
	Hop     []app.HandoffTarget `json:"hop"`
}

// CopyDTO is one copy of a session, on some machine and in some agent.
type CopyDTO struct {
	Machine   string      `json:"machine"`
	Agent     agent.ID    `json:"agent"`
	AgentName string      `json:"agentName"`
	Key       string      `json:"key"` // agent/session, as EntryDTO.Key
	Local     bool        `json:"local"`
	Mark      *agent.Mark `json:"mark,omitempty"`
	Newest    bool        `json:"newest"`
}

// HopDTO is one step of a session's history.
type HopDTO struct {
	Operation string   `json:"operation,omitempty"`
	Branch    string   `json:"branch,omitempty"`
	Loss      []string `json:"loss,omitempty"`
	When      string   `json:"when"` // RFC 3339
	What      string   `json:"what"`
}

// GroupDTO is one repository.
type GroupDTO struct {
	Name     string     `json:"name"`
	Remote   string     `json:"remote"`
	Local    string     `json:"local"`
	NoRepo   bool       `json:"noRepo"`   // not in a git checkout
	NoRemote bool       `json:"noRemote"` // a checkout without a remote (matched by path only)
	Entries  []EntryDTO `json:"entries"`
}

// ScanDTO is the result of a scan.
type ScanDTO struct {
	Machines []MachineDTO `json:"machines"`
	Groups   []GroupDTO   `json:"groups"`
	Total    int          `json:"total"`
	Peers    []string     `json:"peers"`   // reached machines with hopsesh, a session here can be sent to
	Updated  string       `json:"updated"` // when the scan finished (RFC 3339)
	// Elsewhere is when the other machines and the clouds were last read (RFC 3339): the
	// same as Updated, unless only this machine was read since (RefreshHere).
	Elsewhere string     `json:"elsewhere"`
	Clouds    []CloudDTO `json:"clouds"` // the clouds hopsesh can do something with
	// Adopted are copies brought from a cloud that this scan picked up.
	Adopted []BroughtDTO `json:"adopted"`
}

// Scan reads this machine and every allowed machine, for every enabled agent.
func (a *App) Scan() (*ScanDTO, error) { return a.scanAccounts(false) }
func (a *App) scanAccounts(forceAccounts bool) (*ScanDTO, error) {
	a.scanMu.Lock()
	defer a.scanMu.Unlock()
	a.mu.Lock()
	cfgErr := a.cfgErr
	a.mu.Unlock()
	if cfgErr != nil {
		return nil, cfgErr
	}
	core := a.snapshot()
	for _, h := range core.Cfg.Hosts {
		if h.Allowed {
			a.scanPhase(h.Name, "scanning", "")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	inv := core.Scan(ctx, app.ScanOptions{ForceAccounts: forceAccounts})
	for _, m := range inv.Machines {
		if !m.Local {
			a.scanPhase(m.Name, "done", scanProblem(m))
		}
	}
	now := time.Now()
	a.mu.Lock()
	if a.inv != nil {
		a.inv.Close()
	}
	a.inv, a.invAt, a.plan, a.res = inv, now, nil, nil
	a.closePushLocked()
	a.mu.Unlock()
	a.bindTerminalSessions(inv)
	return a.bindAdopted(scanDTO(core, inv, now, now)), nil
}

// bindAdopted binds the tabs that brought the copies a scan adopted (bindBring).
func (a *App) bindAdopted(d *ScanDTO) *ScanDTO {
	if a.Terms != nil {
		for _, tab := range a.Terms.Tabs() {
			for _, g := range d.Groups {
				for _, e := range g.Entries {
					if e.Machine == tab.Machine && e.Key == tab.Key && tab.Association != "Session association not confirmed" {
						r := e.Relationship
						account := ""
						if e.Profile != nil && e.Profile.Account != nil {
							account = e.Profile.Account.Email
						}
						a.Terms.setMeta(tab.ID, func(m *TabMeta) { m.Relationship = &r; m.Account = account })
					}
				}
			}
		}
	}

	a.publishQuick(d)
	for _, b := range d.Adopted {
		a.bindBring(b)
	}
	return d
}

// RefreshHere reads this machine again and keeps what the last scan found on the other
// machines and in the clouds (reading those takes SSH and the vendors' commands, so the
// window does it less often). It leaves a plan in progress alone. Before any scan it is
// local-only discovery, with no remote authentication.
func (a *App) RefreshHere() (*ScanDTO, error) {
	a.mu.Lock()
	cfgErr := a.cfgErr
	a.mu.Unlock()
	if cfgErr != nil {
		return nil, cfgErr
	}
	a.scanMu.Lock()
	defer a.scanMu.Unlock()
	core := a.snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	fresh := core.Scan(ctx, app.ScanOptions{Hosts: []string{app.LocalName()}})
	now := time.Now()
	a.mu.Lock()
	old := a.inv
	if old == nil {
		old = &app.Inventory{}
	}
	inv := &app.Inventory{Adopted: fresh.Adopted, Waiting: fresh.Waiting, Clouds: old.Clouds}
	inv.Machines = append(inv.Machines, fresh.Machines...)
	inv.Entries = append(inv.Entries, fresh.Entries...)
	for _, m := range old.Machines {
		if m.Local {
			if hm := m.Host(); hm != nil {
				hm.Close()
			}
		} else {
			inv.Machines = append(inv.Machines, m) // its connection stays open, as in old
		}
	}
	for _, e := range old.Entries {
		if lm := old.Machine(e.Machine); lm == nil || !lm.Local {
			inv.Entries = append(inv.Entries, e) // other machines' and the clouds'
		}
	}
	sort.SliceStable(inv.Entries, func(i, j int) bool {
		return inv.Entries[i].Session.LastActivity.After(inv.Entries[j].Session.LastActivity)
	})
	a.inv = inv
	at := a.invAt
	a.mu.Unlock()
	a.bindTerminalSessions(inv)
	return a.bindAdopted(scanDTO(core, inv, now, at)), nil
}

// scanDTO is an inventory for the window: updated is when it was read, elsewhere when
// the other machines and the clouds were.
func scanDTO(core *app.App, inv *app.Inventory, updated, elsewhere time.Time) *ScanDTO {
	out := &ScanDTO{Total: len(inv.Entries), Machines: []MachineDTO{}, Groups: []GroupDTO{}, Peers: []string{}, Updated: updated.Format(time.RFC3339),
		Elsewhere: elsewhere.Format(time.RFC3339), Clouds: shownClouds(core, inv), Adopted: []BroughtDTO{}}
	for _, f := range inv.Adopted {
		out.Adopted = append(out.Adopted, core.Brought(f))
	}
	for _, m := range inv.Machines {
		d := MachineDTO{Name: m.Name, Status: m.Status, Hint: m.Hint, Error: m.Error, OS: m.OS, Local: m.Local, Hopsesh: m.Hopsesh, Agents: []string{}}
		d.AccountSetupRequired = !m.Local && m.Status == app.StatusOK && m.Host() != nil && m.Host().Facts.Endpoint == ""
		for _, e := range inv.Entries {
			if e.Machine == m.Name {
				d.Sessions++
			}
		}
		d.Agents = agentNames(m)
		d.AgentNames = machineAgentNames(m)
		out.Machines = append(out.Machines, d)
		if !m.Local && m.Status == app.StatusOK && m.Hopsesh != "" {
			out.Peers = append(out.Peers, m.Name)
		}
	}
	targets := continueTargets(core, inv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	table, _ := presence.Snapshot(ctx) // where the open sessions here run (nil: not known)
	relations := inv.Relationships()
	for _, g := range inv.Groups(core.LocalRoots()) {
		gd := GroupDTO{Name: g.Name, Remote: g.Remote, Local: g.Local, NoRepo: g.Identity == "", NoRemote: strings.HasPrefix(g.Identity, "local:")}
		for _, it := range g.Items {
			d := entryDTO(core, inv, it, targets)
			d.Relationship = relations[app.EntryIdentity(it.Entry.Machine, it.Entry.Session.Key.String())]
			if name := core.Cfg.FamilyNames[d.Relationship.Family]; name != "" {
				d.Relationship.Name = name
			}
			if m := inv.Machine(d.Machine); m != nil && m.Local && d.Live {
				d.Places = placesOf(it.Entry.Live, table, os.Getpid())
			}
			gd.Entries = append(gd.Entries, d)
		}
		out.Groups = append(out.Groups, gd)
	}
	return out
}

// continueTargets are the agents on this machine that can take a converted session.
func continueTargets(core *app.App, inv *app.Inventory) []AgentOpt {
	here := inv.Local()
	if here == nil {
		return nil
	}
	var out []AgentOpt
	seen := map[agent.ID]bool{}
	for _, st := range here.Agents {
		m, ok := core.Module(st.Agent)
		if seen[st.Agent] || !ok || !st.Install.Present || !agent.Has(m, agent.CapWrite) {
			continue
		}
		seen[st.Agent] = true
		out = append(out, AgentOpt{ID: st.Agent, Name: st.Name, Experimental: m.Spec().Stability == agent.Experimental})
	}
	return out
}

// titleOf is a session's title for people (titleFor, without the running agent's name).
func titleOf(s agent.Summary) string {
	t, _ := titleFor(s, "")
	return t
}

// titleFor is a session's title for people and where it comes from, without asking a
// model: the title it was given (custom), else the name the running agent gives it (live:
// the Claude app names sessions), else the agent's own title (generated: Claude Code's
// AI title, a legacy summary, Codex's thread name), else its first real prompt (prompt) or
// first reply (reply), clipped to 80 characters, else "Untitled · folder (branch)".
func titleFor(s agent.Summary, liveName string) (string, string) {
	t := strings.TrimSpace(s.Title)
	if t != "" && s.TitleSource == "custom" {
		return t, "custom"
	}
	if n := strings.TrimSpace(liveName); n != "" {
		return n, "live"
	}
	switch {
	case t != "" && (s.TitleSource == "prompt" || s.TitleSource == "reply"):
		return clipWords(t, 80), s.TitleSource
	case t != "":
		return t, nonEmpty(s.TitleSource, "generated")
	}
	if p, _, _ := strings.Cut(strings.TrimSpace(s.LastPrompt), "\n"); strings.TrimSpace(p) != "" {
		return clipWords(strings.TrimSpace(p), 80), "prompt"
	}
	untitled := "Untitled"
	if dir := strings.TrimRight(strings.ReplaceAll(s.CWD, "\\", "/"), "/"); dir != "" {
		untitled += " · " + dir[strings.LastIndex(dir, "/")+1:]
		if s.GitBranch != "" {
			untitled += " (" + s.GitBranch + ")"
		}
	}
	return untitled, "none"
}

// clipWords shortens text to at most n characters, at a word boundary when there is one
// in its second half, with "…".
func clipWords(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := string(r[:n-1])
	if i := strings.LastIndex(cut, " "); i > len(cut)/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:.-") + "…"
}

func entryDTO(core *app.App, inv *app.Inventory, it app.Item, targets []AgentOpt) EntryDTO {
	e, s := it.Entry, it.Entry.Session
	title, source := titleFor(s, e.Live.Name)
	d := EntryDTO{ObservedAt: e.ObservedAt, Returns: e.Returns, Movement: e.Movement, Profile: e.Profile, Machine: e.Machine, Agent: e.Agent, AgentName: e.AgentName, Key: s.Key.String(), Title: title, TitleSource: source,
		Session: string(s.Key.Session), Path: s.Path, AgentVersion: s.AgentVersion, CanRename: core.CanRename(e), Places: []PlaceDTO{},
		Status: statusWords(core, e), Live: e.Live.State == agent.Live, LastActive: s.LastActivity.Format(time.RFC3339),
		ContextOverflow: s.ContextOverflow, LastPrompt: s.LastPrompt, CWD: s.CWD, SizeKB: s.Size / 1024, ContinueIn: []AgentOpt{},
		Needs:   e.Live.State == agent.Live && strings.HasPrefix(e.Live.Status, "waiting"),
		Journey: journey(e.Lineage), LineageError: e.LineageError, CanArchiveLineage: e.CanArchiveLineage, History: history(core, e.Lineage), Location: string(e.Location.Kind), Cloud: cloudEntryDTO(core, e), Mirror: mirrorDTO(s.Mirror)}
	if e.Machine == app.LocalName() && !e.Location.IsCloud() {
		if mod, ok := core.Module(e.Agent); ok && agent.Has(mod, agent.CapApp) {
			d.CanApp = true
			if e.Profile != nil && !e.Profile.Default {
				d.CanApp = false
				d.AppWhy = "Desktop opening cannot select this account profile; use a terminal"
			}
			if checker, ok := mod.(agent.AppChecker); ok {
				d.CanApp = false
				d.AppWhy = "Scan this machine to check the desktop installation"
				if machine := inv.Machine(e.Machine); machine != nil {
					if install, ok := machine.InstallProfile(e.Agent, e.Session.Key.Profile); ok {
						d.CanApp, d.AppWhy = true, ""
						if err := checker.CheckApp(install, e.Session.Key, agent.ResumeOptions{App: true, AppRunning: e.Live.State == agent.Live && e.Live.App}); err != nil {
							d.CanApp = false
							d.AppWhy = err.Error()
						}
					}
				}
			}
		}
	}

	if e.Live.State == agent.Live && e.Live.App {
		d.App = e.AgentName
		if m, ok := core.Module(e.Agent); ok {
			d.App = nonEmptyStr(appicon.Name(m.Spec().Icon.Apps), d.App)
		}
	}
	if m, ok := core.Module(e.Agent); ok && !e.Location.IsCloud() {
		_, d.CanPreview = m.(agent.Previewer)
	}
	d.Handoff, d.Hop = core.HandoffTargets(inv, e), core.HopTargets(inv, e)
	if d.Handoff == nil {
		d.Handoff = []app.HandoffTarget{}
	}
	if d.Hop == nil {
		d.Hop = []app.HandoffTarget{}
	}
	if m := inv.Machine(e.Machine); m != nil && len(it.Copies) <= 1 {
		d.HereNewest = m.Local
	}
	for _, c := range it.Copies {
		d.Copies = append(d.Copies, CopyDTO{Machine: c.Machine, Agent: c.Agent, AgentName: c.AgentName, Key: c.Key.String(), Local: c.Local, Mark: c.Mark, Newest: c.Newest})
		switch {
		case c.Local && c.Newest:
			d.HereNewest = true
		case c.Local:
			d.StaleHere = true
		}
	}
	if g := e.Git; g != nil && g.IsRepo {
		d.Branch, d.MainBranch = g.Branch, g.MainBranch
		switch {
		case g.AgentWorktree:
			d.Worktree = "agent worktree"
		case g.LinkedWorktree:
			d.Worktree = "worktree"
		}
		d.Unpushed, d.Dirty = g.LeftBehind()
	}
	if src, ok := core.Module(e.Agent); ok && agent.Has(src, agent.CapRead) {
		for _, t := range targets {
			if t.ID != e.Agent {
				d.ContinueIn = append(d.ContinueIn, t)
			}
		}
	} else if ok && e.Location.IsCloud() && !agent.Has(src, agent.CapWrite) && writesText(src, e.Location.Name) {
		def := core.BringTarget(inv, e)
		for _, t := range targets {
			if t.ID == def {
				d.BringIn = &t
			} else {
				d.ContinueIn = append(d.ContinueIn, t)
			}
		}
	}
	return d
}

// writesText reports whether a cloud's sessions come back with words to write here as a
// session: its messages as text, or a code cloud's task summary.
func writesText(m agent.Module, cloud string) bool {
	cl, ok := m.Spec().FindCloud(cloud)
	return ok && (cl.Down == agent.FidText || cl.Down == agent.FidNative || cl.Down == agent.FidCode && cl.Summary)
}

// history tells a session's hops from its lineage, oldest first.
// cloudTitleOf is a cloud's title by its name ("" when loc isn't a cloud's).
func cloudTitleOf(core *app.App, loc string) string {
	if _, cl, ok := core.CloudModule(loc); ok {
		return cl.Title
	}
	return ""
}

// statusWords is an entry's state in words, a cloud named by its title: a session marked
// before marks named clouds that way reads "continued in Claude Code cloud" too.
func statusWords(core *app.App, e app.Entry) string {
	st := e.Status()
	if strings.HasPrefix(st, "continued ") {
		st = "previously " + st
	}
	if mk := e.Session.Mark; mk != nil && mk.Kind == agent.MarkContinued && strings.TrimPrefix(st, "previously ") == app.MarkWords(*mk) {
		if t := cloudTitleOf(core, mk.Location); t != "" {
			return "previously continued in " + t
		}
	}
	return st
}

func journey(m *lineage.Manifest) *lineage.Journey {
	if m == nil {
		return nil
	}
	j := m.Journey()
	return &j
}

func history(core *app.App, m *lineage.Manifest) []HopDTO {
	out := []HopDTO{}
	if m == nil {
		return out
	}
	name := func(id agent.ID) string {
		if mod, ok := core.Reg.Get(id); ok {
			return mod.Spec().Name
		}
		return string(id)
	}
	// A cloud is named by its title ("Claude Code cloud"), and is where its agent runs.
	place := func(loc string) string { return nonEmptyStr(cloudTitleOf(core, loc), loc) }
	in := func(id agent.ID, loc string) string {
		if t := cloudTitleOf(core, loc); t != "" {
			return t
		}
		return name(id) + " on " + loc
	}
	hops := m.OrderedHops()
	for _, h := range hops {
		if h.Line != m.Branch {
			continue
		}
		if !m.HasReplica(h.From) || !m.HasReplica(h.To) {
			continue
		}
		from, to := m.Replica(h.From), m.Replica(h.To)
		if len(out) == 0 {
			out = append(out, HopDTO{When: from.Time.Format(time.RFC3339), What: "In " + in(from.Key.Agent, from.Location)})
		}
		what := "Moved to " + place(to.Location)
		switch {
		case h.Kind == lineage.HopFetch:
			what = fmt.Sprintf("Brought from %s to %s", place(from.Location), place(to.Location))
		case h.Kind == lineage.HopHandoff:
			what = "Handed off to " + place(to.Location)
		}
		if h.Kind == lineage.HopContinue {
			what = "Continued in " + in(to.Key.Agent, to.Location)
		} else if h.Backup {
			what = fmt.Sprintf("The %s copy kept on %s too", name(to.Key.Agent), place(to.Location)) // the native copy
		}
		if h.Rollover != nil {
			what += " · bounded continuation; original retained"
		}
		if h.Fork {
			what += " (separate fork)"
		}
		for _, c := range m.Compensations {
			if c.Operation == h.ID {
				what += " · undone"
			}
		}
		out = append(out, HopDTO{When: h.Time.Format(time.RFC3339), What: what, Operation: h.ID, Branch: h.Line, Loss: m.State(h.Target).Loss})
	}
	return out
}

// find returns a scanned session (callers hold a.mu).
func (a *App) find(machine, key string) (app.Entry, error) {
	if a.inv == nil {
		return app.Entry{}, errors.New("scan first")
	}
	for _, e := range a.inv.Entries {
		if e.Machine == machine && e.Session.Key.String() == key {
			return e, nil
		}
	}
	return app.Entry{}, errors.New("session not found; refresh")
}

// OptsDTO are the choices on the plan screen.
type OptsDTO struct {
	Bounded       bool   `json:"bounded"`
	NewReplica    bool   `json:"newReplica"`
	TargetProfile string `json:"targetProfile"`
	OperationID   string `json:"operationId"`
	TargetSession string `json:"targetSession"`
	TargetDir     string `json:"targetDir"`
	Clone         bool   `json:"clone"`
	ReposDir      string `json:"reposDir"`
	Worktree      string `json:"worktree"`
	Fork          bool   `json:"fork"`
	RemoteControl bool   `json:"remoteControl"`
	Notify        bool   `json:"notify"`
	Redact        bool   `json:"redact"`
	Mark          bool   `json:"mark"`
	SyncCode      bool   `json:"syncCode"`
	Push          bool   `json:"push"`
	StopLocal     bool   `json:"stopLocal"`
	Conflict      string `json:"conflict"` // "", "replace" or "keep-both"
	App           bool   `json:"app"`
	// Continuing in another agent.
	Fidelity   string   `json:"fidelity"` // history | note
	Native     bool     `json:"native"`
	Note       string   `json:"note"`
	Go         bool     `json:"go"`
	CarryRules bool     `json:"carryRules"`
	RuleFiles  []string `json:"ruleFiles"`
	Via        string   `json:"via"` // "" or "import"
	// Bringing a session from a cloud: the branch only; add its work to the session it was
	// handed off from.
	CodeOnly bool `json:"codeOnly"`
	Append   bool `json:"append"`
}

func (o OptsDTO) options(d move.Options) move.Options {
	d.Bounded = o.Bounded
	d.NewReplica = o.NewReplica
	d.TargetDir, d.Clone, d.Worktree = o.TargetDir, o.Clone, move.WorktreeMode(nonEmpty(o.Worktree, string(move.WorktreeAuto)))
	if o.ReposDir != "" {
		d.ReposDir = o.ReposDir
	}
	d.Fork, d.RemoteControl, d.Notify, d.Redact, d.App = o.Fork, o.RemoteControl, o.Notify, o.Redact, o.App
	d.OperationID, d.TargetSession = o.OperationID, o.TargetSession
	d.TargetProfile = o.TargetProfile
	d.Mark, d.SyncCode, d.Push, d.StopLocal, d.Conflict = o.Mark, o.SyncCode, o.Push, o.StopLocal, o.Conflict
	d.Fidelity, d.Native, d.Note, d.Go = convert.Fidelity(nonEmpty(o.Fidelity, string(convert.History))), o.Native, strings.TrimSpace(o.Note), o.Go
	d.RuleFiles = o.RuleFiles
	d.CarryRules, d.CodeOnly, d.AppendOriginal = o.CarryRules, o.CodeOnly, o.Append
	if o.Via == move.ViaImport {
		d.Via = move.ViaImport
	}
	return d
}

// CanDTO is what the receiving agent can do, for the options shown.
type CanDTO struct {
	AppWhy        string `json:"appWhy,omitempty"`
	Fork          bool   `json:"fork"`
	RemoteControl bool   `json:"remoteControl"`
	App           bool   `json:"app"`
	Native        bool   `json:"native"`
	Import        bool   `json:"import"`
}

// ContinueDTO is the conversion part of a plan.
type ContinueDTO struct {
	Instructions []move.InstructionSource `json:"instructions"`
	From         string                   `json:"from"`
	Fidelity     string                   `json:"fidelity"`
	Relation     string                   `json:"relation"`
	AppendTo     string                   `json:"appendTo,omitempty"` // the title of the copy here that gets the new work
	Report       convert.Report           `json:"report"`
	Briefing     string                   `json:"briefing"`
	Via          string                   `json:"via,omitempty"`
}

// PlanDTO is a plan as the window shows it.
type PlanDTO struct {
	SourceProfile string           `json:"sourceProfile,omitempty"`
	TargetProfile string           `json:"targetProfile,omitempty"`
	NoWork        bool             `json:"noWork"`
	Destinations  []agent.Summary  `json:"destinations,omitempty"`
	Kind          string           `json:"kind"` // move | continue
	Title         string           `json:"title"`
	Agent         string           `json:"agent"`     // the agent it lands in
	FromAgent     string           `json:"fromAgent"` // the agent it comes from
	SourceHost    string           `json:"sourceHost"`
	SourceOS      string           `json:"sourceOs"`
	SourceCWD     string           `json:"sourceCwd"`
	TargetCWD     string           `json:"targetCwd"`
	Live          bool             `json:"live"`
	Repo          move.RepoPlan    `json:"repo"`
	Mappings      []agent.Mapping  `json:"mappings"`
	Files         int              `json:"files"`
	Bytes         int64            `json:"bytes"`
	Mark          string           `json:"mark"`
	Sync          string           `json:"sync"`
	FromSource    bool             `json:"syncFromSource"`
	Push          bool             `json:"push"`
	StopHere      bool             `json:"stopHere"`
	Conflict      string           `json:"conflict"`
	Warnings      []string         `json:"warnings"`
	Blockers      []string         `json:"blockers"`
	NewName       string           `json:"newName"`
	OtherAcct     bool             `json:"otherAccount"`
	Continue      *ContinueDTO     `json:"continue,omitempty"`
	NativeCopy    *move.NativeCopy `json:"nativeCopy,omitempty"`
	Can           CanDTO           `json:"can"`
	SetAside      int              `json:"setAside"`
	Options       move.Options     `json:"-"`
	SessionKey    agent.SessionKey `json:"-"`
	SourceAgent   agent.ID         `json:"sourceAgent"`
	Machine       string           `json:"machine,omitempty"` // a push: the machine it goes to
	Fetch         *move.FetchPlan  `json:"fetch,omitempty"`   // bringing it from a cloud
}

// Plan works out how a session comes here: in its own agent (target "") or continued in
// another. Nothing changes.
func (a *App) Plan(machine, key, target string, o OptsDTO) (*PlanDTO, error) {
	core := a.snapshot()
	a.mu.Lock()
	e, err := a.find(machine, key)
	inv := a.inv
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return a.planEntry(core, inv, e, target, o)
}

// planEntry plans a session (a row, or a cloud session not listed) and keeps the plan for
// Apply.
func (a *App) planEntry(core *app.App, inv *app.Inventory, e app.Entry, target string, o OptsDTO) (*PlanDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	opts := o.options(core.DefaultOptions())
	if e.Location.IsCloud() {
		opts.TargetDir = nonEmpty(o.TargetDir, e.Checkout) // the repository's checkout here
	}
	p, in, err := core.Plan(ctx, inv, e, agent.ID(target), opts)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.plan, a.input, a.res = p, in, nil
	a.mu.Unlock()
	if p.Kind == move.KindFetch {
		return fetchPlanDTO(p, e), nil
	}
	d := planDTO(p, e, in.Target.Module)
	if checker, ok := in.Target.Module.(agent.AppChecker); ok {
		if err := checker.CheckApp(in.Target.Install, p.Placement.Key, agent.ResumeOptions{App: true}); err != nil {
			d.Can.App = false
			d.Can.AppWhy = err.Error()
		}
	}
	return d, nil
}

func planDTO(p *move.Plan, e app.Entry, tm agent.Module) *PlanDTO {
	d := &PlanDTO{SourceProfile: p.Source.ProfileName, TargetProfile: p.Target.ProfileName, NoWork: p.NoWork, Destinations: p.Destinations, Kind: p.Kind, Title: p.Title, Agent: p.Agent, FromAgent: e.AgentName, SourceHost: p.Source.Location,
		SourceOS: p.Source.OS, SourceCWD: p.Source.CWD, TargetCWD: p.Target.CWD, Live: p.Live, Repo: p.Repo,
		Mappings: p.Placement.Mappings, Files: len(p.Files.Files), Bytes: p.Bytes, Mark: p.Mark, Sync: p.Sync,
		FromSource: p.SyncFromSource, Push: p.Push, StopHere: p.StopHere, Conflict: p.Conflict,
		Warnings: p.Warnings, Blockers: p.Blockers, NewName: p.NewName, OtherAcct: p.Placement.OtherAccount,
		SetAside: len(p.SetAside), NativeCopy: p.NativeCopy, Options: p.Options, SessionKey: p.Key, SourceAgent: e.Agent,
		Can: CanDTO{Fork: agent.Has(tm, agent.CapFork), RemoteControl: agent.Has(tm, agent.CapRemoteControl),
			App: agent.Has(tm, agent.CapApp), Native: !p.Options.OtherAccount && agent.Has(tm, agent.CapNativeReplay),
			Import: !p.Options.OtherAccount && importsFrom(tm, e.Agent)}}
	if c := p.Continue; c != nil {
		d.Continue = &ContinueDTO{Instructions: c.Instructions, From: c.From, Fidelity: string(c.Fidelity), Relation: c.Relation, Report: c.Report, Briefing: c.Briefing, Via: c.Via}
		if c.AppendTo != nil {
			d.Continue.AppendTo = c.AppendTo.Title
		}
	}
	if d.Mappings == nil {
		d.Mappings = []agent.Mapping{}
	}
	return d
}

// importsFrom reports whether a module's own importer reads the source agent's sessions.
func importsFrom(m agent.Module, from agent.ID) bool {
	imp, ok := m.(agent.Importer)
	return ok && imp.CanImport(from)
}

// DoneDTO reports a finished move or continuation.
type DoneDTO struct {
	NoWork           bool     `json:"noWork"`
	Kind             string   `json:"kind"`
	Title            string   `json:"title"`
	Agent            string   `json:"agent"`
	Command          string   `json:"command"`
	Files            int      `json:"files"`
	Bytes            string   `json:"bytes"`
	Paths            int      `json:"paths"`
	Secrets          int      `json:"secrets"`
	Redacted         bool     `json:"redacted"`
	Cloned           bool     `json:"cloned"`
	Worktree         string   `json:"worktree"`
	Journal          string   `json:"journal"`
	SourceHost       string   `json:"sourceHost"`
	Stopped          bool     `json:"stopped"`
	Pushed           string   `json:"pushed"`
	PushError        string   `json:"pushError"`
	SyncNote         string   `json:"syncNote"`
	SyncState        string   `json:"syncState"`
	Mark             string   `json:"mark"`
	MarkError        string   `json:"markError"`
	Notice           string   `json:"notice"`
	Warnings         []string `json:"warnings"`
	InApp            bool     `json:"inApp"` // it opens in the agent's desktop app
	ContinuationHint string   `json:"continuationHint,omitempty"`
	Machine          string   `json:"machine,omitempty"` // a push: where it went (start it there)
	AuditDir         string   `json:"auditDir"`
	// Fetch is a session brought from a cloud: waiting for the user's terminal, or the
	// code only.
	Fetch *BroughtDTO `json:"fetch,omitempty"`
}

// Apply carries out the last plan, sending each step to the window.
func (a *App) Apply() (*DoneDTO, error) {
	core := a.snapshot()
	a.mu.Lock()
	p, in := a.plan, a.input
	a.mu.Unlock()
	if p == nil {
		return nil, errors.New("no plan; choose the session again")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	res, err := core.Apply(ctx, p, in, func(step string) { a.emit(ProgressEvent, step) })
	if err != nil {
		var ge *repos.GitError
		if errors.As(err, &ge) && p.Repo.Action == move.RepoClone {
			return nil, fmt.Errorf("%w. Clone it yourself, then choose it with “I already have it…”", err)
		}
		return nil, err
	}
	a.mu.Lock()
	a.res = res
	a.mu.Unlock()
	d := &DoneDTO{NoWork: p.NoWork, Kind: p.Kind, Title: p.Title, Agent: p.Agent, Command: res.Command, Files: res.Files, Bytes: move.Human(res.Bytes),
		Secrets: res.Secrets.Total, Redacted: p.Options.Redact, Cloned: res.Cloned, Worktree: res.Worktree, Journal: res.Journal,
		SourceHost: p.Source.Location, Stopped: res.Stopped, Pushed: res.Pushed, PushError: res.PushError, SyncNote: res.SyncNote,
		Mark: res.Mark, MarkError: res.MarkError, Notice: res.Notice, Warnings: res.Warnings, InApp: p.Options.App,
		AuditDir: filepath.Join(config.StateDir(), "log"), ContinuationHint: p.ContinuationHint()}
	if p.NoWork && p.SyncTo != nil && p.SyncTo.Title != "" {
		d.Title = p.SyncTo.Title
	}
	if c := p.Continue; c != nil && c.AppendTo != nil && c.AppendTo.Title != "" {
		d.Title = c.AppendTo.Title // the session it went back into
	}
	for _, n := range res.Rewrite.Replacements {
		d.Paths += n
	}
	if res.Sync != nil {
		d.SyncState = res.Sync.State
	}
	if p.Kind == move.KindFetch {
		b := BroughtDTO{Journal: res.Journal, Cloud: p.Fetch.Cloud, CloudTitle: p.Fetch.CloudTitle, Session: string(p.Fetch.Session), URL: p.Fetch.URL,
			Title: p.Title, Agent: p.Agent, Worktree: res.Worktree, Outcome: res.Fetch.Outcome, Branch: res.Fetch.Branch, Command: res.Command}
		if f, err := move.LoadFetch(core.StateDir, res.Journal); err == nil {
			b = core.Brought(f)
		}
		d.Fetch = &b
	}
	return d, nil
}

// OpenResult starts the session that was just moved or continued: in the agent's desktop
// app when that was chosen, otherwise in a tab of the hopsesh Terminal window or the user's
// terminal app (where as for ResumeSession). A bring-back still waiting for the agent's own
// command (claude --teleport) runs that command, in a tab unless the user chose their
// terminal app.
func (a *App) OpenResult(where string) (*OpenedDTO, error) {
	a.mu.Lock()
	p, res := a.plan, a.res
	setting := a.core.Cfg.AppResume()
	a.mu.Unlock()
	if p == nil || res == nil {
		return nil, errors.New("nothing was moved")
	}
	if p.Options.App {
		return &OpenedDTO{Where: "app"}, start(p.Resume)
	}
	if res.Command == "" || len(res.Run.Argv) == 0 {
		return nil, errors.New("there is nothing to open")
	}
	l := app.Launch{Kind: termapp.KindSession, Run: res.Run, Key: p.Placement.Key,
		Labels: termapp.Labels{Title: p.Title, Agent: p.Agent, Machine: app.LocalName()}}
	if p.Kind == move.KindFetch && res.Fetch != nil {
		if res.Fetch.Outcome == move.FetchWaiting {
			// The agent's own command that brings the session (claude --teleport).
			l.Kind, l.Key, l.Labels.Agent = termapp.KindStep, agent.SessionKey{}, p.Fetch.CloudTitle
			return a.bringTab(l, res.Journal, p.Fetch.Cloud, p.Fetch.CloudTitle, p.Agent, p.Title, where)
		}
		l.Key, _ = agent.ParseKey(res.Fetch.Key)
	}
	key := l.Key.String()
	if a.Terms != nil {
		if id := a.Terms.liveSessionTab(app.LocalName(), key); id != "" {
			a.Terms.Focus(id)
			return &OpenedDTO{Where: WhereShown, Tab: id}, nil
		}
	}
	switch route(where, setting) {
	case WhereTerminal:
		return &OpenedDTO{Where: WhereTerminal}, a.openLaunch(l)
	}
	return a.sessionTab(res.Run, l, TabMeta{Kind: TabSession, Agent: p.Agent, Machine: app.LocalName(), Key: key}, p.Title)
}

// bringTab runs a bring-back's command (claude --teleport, which saves the copy only once
// the user sends a message in it) in a tab with that said, or in the user's terminal app.
func (a *App) bringTab(l app.Launch, journal, cloud, cloudTitle, agentName, title, where string) (*OpenedDTO, error) {
	a.mu.Lock()
	setting := a.core.Cfg.AppResume()
	a.mu.Unlock()
	if a.Terms == nil || route(where, setting) == WhereTerminal {
		return &OpenedDTO{Where: WhereTerminal}, a.openLaunch(l)
	}
	spec, err := a.tabSpec(l.Run, tabTitle("Bring here", title, strings.TrimSuffix(filepath.Base(l.Run.Argv[0]), ".exe")))
	if err == nil {
		var info pty.Info
		info, err = a.Terms.Open(spec, TabSetup{
			Meta: TabMeta{Kind: TabBring, Command: displayCommand(l.Run.Argv), Agent: agentName, Cloud: cloud, CloudTitle: cloudTitle,
				Journal: journal, External: true},
			External: func(id string) error { return a.moveOut(id, l) },
		})
		if err == nil {
			return &OpenedDTO{Where: WhereHere, Tab: info.ID}, nil
		}
	}
	if oerr := a.openLaunch(l); oerr != nil {
		return nil, fmt.Errorf("%v; and %w", err, oerr)
	}
	return &OpenedDTO{Where: WhereTerminal, Notice: a.fellBack(err)}, nil
}

// ResumeEntry continues a session that is already on this machine, in a terminal or (inApp)
// the agent's desktop app.
func (a *App) ResumeEntry(machine, key string, inApp bool) error {
	core := a.snapshot()
	a.mu.Lock()
	e, err := a.find(machine, key)
	inv := a.inv
	a.mu.Unlock()
	if err != nil {
		return err
	}
	if a.Terms != nil && a.Terms.liveSessionTab(machine, key) != "" {
		return fmt.Errorf("%w in the hopsesh Terminal window: show that tab instead", app.ErrOpenElsewhere)
	}
	c, err := core.Resume(inv, e, agent.ResumeOptions{App: inApp})
	if err != nil {
		return err
	}
	// One live tab per session: never a second copy writing to the same conversation.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := core.OpenElsewhere(ctx, e.Session.Key, e.Live); err != nil {
		return err
	}
	if inApp {
		return start(c)
	}
	return a.openLaunch(app.Launch{Kind: termapp.KindSession, Run: c, Key: e.Session.Key,
		Labels: termapp.Labels{Title: titleOf(e.Session), Agent: e.AgentName, Machine: e.Machine}})
}

// Undo reverses a move or continuation by its journal id; force undoes it even when the
// session was used since.
func (a *App) Undo(journal string, force bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	_, err := a.snapshot().Undo(ctx, journal, force)
	return err
}

// start runs an agent command without a terminal (it opens the agent's own window).
func start(c agent.Command) error {
	if len(c.Argv) == 0 {
		return errors.New("no command")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	if c.TTY {
		defer cancel()
		return startDesktopTTY(ctx, c)
	}
	if !c.Wait {
		cancel()
		ctx = context.Background()
	}
	defer cancel()
	cmd := proc.CommandContext(ctx, c.Argv[0], c.Argv[1:]...)
	cmd.Dir = c.Dir
	cmd.Env = append(host.Without(os.Environ(), c.Unset), c.Env...)
	if c.Wait {
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("opening desktop app: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return nil
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func nonEmpty(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func (a *App) ArchiveLineage(machine, key string) error {
	core := a.snapshot()
	a.mu.Lock()
	e, err := a.find(machine, key)
	inv := a.inv
	a.mu.Unlock()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, err = core.ArchiveLineage(ctx, inv, e)
	return err
}

// Claude's desktop launcher rejects redirected stdin/stdout. Reuse the app's
// cross-platform PTY backend without publishing a terminal tab or sending input.
func startDesktopTTY(ctx context.Context, c agent.Command) error {
	mgr := pty.NewManager(pty.Options{MaxTabs: 1, Scrollback: 32 << 10})
	defer mgr.CloseAll()
	s, err := mgr.Start(pty.Spec{Argv: c.Argv, Dir: c.Dir,
		Env: pty.Env{Unset: c.Unset, Set: c.Env}, Capture: pty.CaptureStep})
	if err != nil {
		return fmt.Errorf("opening desktop app: %w", err)
	}
	code, err := s.Wait(ctx)
	if err != nil {
		return fmt.Errorf("opening desktop app: %w", err)
	}
	output, _ := s.StepOutput()
	if code != 0 {
		return fmt.Errorf("opening desktop app (exit %d): %s", code, strings.TrimSpace(output.Text))
	}
	return nil
}
