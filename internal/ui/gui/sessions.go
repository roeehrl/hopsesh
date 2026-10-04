package gui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/convert"
	"github.com/roeehrl/hopsesh/internal/core/launch"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/proc"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// ProgressEvent carries the steps of a move or continuation to the window.
const ProgressEvent = "hopsesh:progress"

// MachineDTO summarises one scanned machine.
type MachineDTO struct {
	Name     string   `json:"name"`
	Status   string   `json:"status"`
	Hint     string   `json:"hint"`
	Error    string   `json:"error"`
	OS       string   `json:"os"`
	Sessions int      `json:"sessions"`
	Local    bool     `json:"local"`
	Agents   []string `json:"agents"`  // "Claude Code 2.1.284"
	Hopsesh  string   `json:"hopsesh"` // hopsesh's version there ("" when not installed)
}

// AgentOpt is an agent a session can continue in here.
type AgentOpt struct {
	ID           agent.ID `json:"id"`
	Name         string   `json:"name"`
	Experimental bool     `json:"experimental,omitempty"`
}

// EntryDTO is one session row.
type EntryDTO struct {
	Machine    string     `json:"machine"`
	Agent      agent.ID   `json:"agent"`
	AgentName  string     `json:"agentName"`
	Key        string     `json:"key"` // agent/session
	Title      string     `json:"title"`
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
	History    []HopDTO   `json:"history"`          // where it has been, oldest first
	// Location is "machine" or "cloud"; a cloud session's row has Cloud (Machine is the
	// cloud's name), and a session the vendor mirrors (Remote Control) has Mirror.
	Location string         `json:"location"`
	Cloud    *CloudEntryDTO `json:"cloud,omitempty"`
	Mirror   *MirrorDTO     `json:"mirror,omitempty"`
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
	When string `json:"when"` // RFC 3339
	What string `json:"what"`
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
	Clouds   []CloudDTO   `json:"clouds"`  // the clouds hopsesh can do something with
	// Adopted are copies brought from a cloud that this scan picked up.
	Adopted []BroughtDTO `json:"adopted"`
}

// Scan reads this machine and every allowed machine, for every enabled agent.
func (a *App) Scan() (*ScanDTO, error) {
	a.mu.Lock()
	cfgErr := a.cfgErr
	a.mu.Unlock()
	if cfgErr != nil {
		return nil, cfgErr
	}
	core := a.snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	inv := core.Scan(ctx, app.ScanOptions{})
	a.mu.Lock()
	if a.inv != nil {
		a.inv.Close()
	}
	a.inv, a.plan, a.res = inv, nil, nil
	a.closePushLocked()
	a.mu.Unlock()

	out := &ScanDTO{Total: len(inv.Entries), Machines: []MachineDTO{}, Groups: []GroupDTO{}, Peers: []string{}, Updated: time.Now().Format(time.RFC3339),
		Clouds: shownClouds(core, inv), Adopted: []BroughtDTO{}}
	for _, f := range inv.Adopted {
		out.Adopted = append(out.Adopted, core.Brought(f))
	}
	for _, m := range inv.Machines {
		d := MachineDTO{Name: m.Name, Status: m.Status, Hint: m.Hint, Error: m.Error, OS: m.OS, Local: m.Local, Hopsesh: m.Hopsesh, Agents: []string{}}
		for _, e := range inv.Entries {
			if e.Machine == m.Name {
				d.Sessions++
			}
		}
		for _, st := range m.Agents {
			if st.Install.Present || st.Install.Binary != "" {
				d.Agents = append(d.Agents, strings.TrimSpace(st.Name+" "+st.Install.Version))
			}
		}
		out.Machines = append(out.Machines, d)
		if !m.Local && m.Status == app.StatusOK && m.Hopsesh != "" {
			out.Peers = append(out.Peers, m.Name)
		}
	}
	targets := continueTargets(core, inv)
	for _, g := range inv.Groups(core.LocalRoots()) {
		gd := GroupDTO{Name: g.Name, Remote: g.Remote, Local: g.Local, NoRepo: g.Identity == "", NoRemote: strings.HasPrefix(g.Identity, "local:")}
		for _, it := range g.Items {
			gd.Entries = append(gd.Entries, entryDTO(core, inv, it, targets))
		}
		out.Groups = append(out.Groups, gd)
	}
	return out, nil
}

// continueTargets are the agents on this machine that can take a converted session.
func continueTargets(core *app.App, inv *app.Inventory) []AgentOpt {
	here := inv.Local()
	if here == nil {
		return nil
	}
	var out []AgentOpt
	for _, st := range here.Agents {
		m, ok := core.Module(st.Agent)
		if !ok || !st.Install.Present || !agent.Has(m, agent.CapWrite) {
			continue
		}
		out = append(out, AgentOpt{ID: st.Agent, Name: st.Name, Experimental: m.Spec().Stability == agent.Experimental})
	}
	return out
}

func entryDTO(core *app.App, inv *app.Inventory, it app.Item, targets []AgentOpt) EntryDTO {
	e, s := it.Entry, it.Entry.Session
	d := EntryDTO{Machine: e.Machine, Agent: e.Agent, AgentName: e.AgentName, Key: s.Key.String(), Title: s.Title,
		Status: e.Status(), Live: e.Live.State == agent.Live, LastActive: s.LastActivity.Format(time.RFC3339),
		LastPrompt: s.LastPrompt, CWD: s.CWD, SizeKB: s.Size / 1024, ContinueIn: []AgentOpt{},
		Needs:   e.Live.State == agent.Live && strings.HasPrefix(e.Live.Status, "waiting"),
		History: history(core, e.Lineage), Location: string(e.Location.Kind), Cloud: cloudEntryDTO(core, e), Mirror: mirrorDTO(s.Mirror)}
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
	}
	return d
}

// history tells a session's hops from its lineage, oldest first.
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
	hops := append([]lineage.Hop(nil), m.Hops...)
	sort.Slice(hops, func(i, j int) bool { return hops[i].Time.Before(hops[j].Time) })
	for i, h := range hops {
		if h.From < 0 || h.From >= len(m.Replicas) || h.To < 0 || h.To >= len(m.Replicas) {
			continue
		}
		from, to := m.Replicas[h.From], m.Replicas[h.To]
		if i == 0 {
			out = append(out, HopDTO{When: from.Time.Format(time.RFC3339), What: fmt.Sprintf("In %s on %s", name(from.Key.Agent), from.Location)})
		}
		what := fmt.Sprintf("Moved to %s", to.Location)
		switch {
		case h.Kind == lineage.HopFetch:
			what = fmt.Sprintf("Brought from %s to %s", from.Location, to.Location)
		case h.Kind == lineage.HopHandoff:
			what = fmt.Sprintf("Handed off to %s", to.Location)
		}
		if h.Kind == lineage.HopContinue {
			what = fmt.Sprintf("Continued in %s on %s", name(to.Key.Agent), to.Location)
		} else if i > 0 && hops[i-1].Kind == lineage.HopContinue && hops[i-1].Time.Equal(h.Time) {
			what = fmt.Sprintf("The %s copy kept on %s too", name(to.Key.Agent), to.Location) // the native copy
		}
		if h.Fork {
			what += " (both kept going)"
		}
		out = append(out, HopDTO{When: h.Time.Format(time.RFC3339), What: what})
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
	Fidelity   string `json:"fidelity"` // history | note
	Native     bool   `json:"native"`
	Note       string `json:"note"`
	Go         bool   `json:"go"`
	CarryRules bool   `json:"carryRules"`
	Via        string `json:"via"` // "" or "import"
	// Bringing a session from a cloud: the branch only; add its work to the session it was
	// handed off from.
	CodeOnly bool `json:"codeOnly"`
	Append   bool `json:"append"`
}

func (o OptsDTO) options(d move.Options) move.Options {
	d.TargetDir, d.Clone, d.Worktree = o.TargetDir, o.Clone, move.WorktreeMode(nonEmpty(o.Worktree, string(move.WorktreeAuto)))
	if o.ReposDir != "" {
		d.ReposDir = o.ReposDir
	}
	d.Fork, d.RemoteControl, d.Notify, d.Redact, d.App = o.Fork, o.RemoteControl, o.Notify, o.Redact, o.App
	d.Mark, d.SyncCode, d.Push, d.StopLocal, d.Conflict = o.Mark, o.SyncCode, o.Push, o.StopLocal, o.Conflict
	d.Fidelity, d.Native, d.Note, d.Go = convert.Fidelity(nonEmpty(o.Fidelity, string(convert.History))), o.Native, strings.TrimSpace(o.Note), o.Go
	d.CarryRules, d.CodeOnly, d.AppendOriginal = o.CarryRules, o.CodeOnly, o.Append
	if o.Via == move.ViaImport {
		d.Via = move.ViaImport
	}
	return d
}

// CanDTO is what the receiving agent can do, for the options shown.
type CanDTO struct {
	Fork          bool `json:"fork"`
	RemoteControl bool `json:"remoteControl"`
	App           bool `json:"app"`
	Notify        bool `json:"notify"`
	Native        bool `json:"native"`
	Import        bool `json:"import"`
}

// ContinueDTO is the conversion part of a plan.
type ContinueDTO struct {
	From     string         `json:"from"`
	Fidelity string         `json:"fidelity"`
	Relation string         `json:"relation"`
	AppendTo string         `json:"appendTo,omitempty"` // the title of the copy here that gets the new work
	Report   convert.Report `json:"report"`
	Briefing string         `json:"briefing"`
	Via      string         `json:"via,omitempty"`
}

// PlanDTO is a plan as the window shows it.
type PlanDTO struct {
	Kind        string           `json:"kind"` // move | continue
	Title       string           `json:"title"`
	Agent       string           `json:"agent"`     // the agent it lands in
	FromAgent   string           `json:"fromAgent"` // the agent it comes from
	SourceHost  string           `json:"sourceHost"`
	SourceOS    string           `json:"sourceOs"`
	SourceCWD   string           `json:"sourceCwd"`
	TargetCWD   string           `json:"targetCwd"`
	Live        bool             `json:"live"`
	Repo        move.RepoPlan    `json:"repo"`
	Mappings    []agent.Mapping  `json:"mappings"`
	Files       int              `json:"files"`
	Bytes       int64            `json:"bytes"`
	Mark        string           `json:"mark"`
	Sync        string           `json:"sync"`
	FromSource  bool             `json:"syncFromSource"`
	Push        bool             `json:"push"`
	StopHere    bool             `json:"stopHere"`
	Conflict    string           `json:"conflict"`
	Warnings    []string         `json:"warnings"`
	Blockers    []string         `json:"blockers"`
	NewName     string           `json:"newName"`
	OtherAcct   bool             `json:"otherAccount"`
	Continue    *ContinueDTO     `json:"continue,omitempty"`
	NativeCopy  *move.NativeCopy `json:"nativeCopy,omitempty"`
	Can         CanDTO           `json:"can"`
	SetAside    int              `json:"setAside"`
	Options     move.Options     `json:"-"`
	SessionKey  agent.SessionKey `json:"-"`
	SourceAgent agent.ID         `json:"sourceAgent"`
	Machine     string           `json:"machine,omitempty"` // a push: the machine it goes to
	Fetch       *move.FetchPlan  `json:"fetch,omitempty"`   // bringing it from a cloud
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
	return planDTO(p, e, in.Target.Module), nil
}

func planDTO(p *move.Plan, e app.Entry, tm agent.Module) *PlanDTO {
	d := &PlanDTO{Kind: p.Kind, Title: p.Title, Agent: p.Agent, FromAgent: e.AgentName, SourceHost: p.Source.Location,
		SourceOS: p.Source.OS, SourceCWD: p.Source.CWD, TargetCWD: p.Target.CWD, Live: p.Live, Repo: p.Repo,
		Mappings: p.Placement.Mappings, Files: len(p.Files.Files), Bytes: p.Bytes, Mark: p.Mark, Sync: p.Sync,
		FromSource: p.SyncFromSource, Push: p.Push, StopHere: p.StopHere, Conflict: p.Conflict,
		Warnings: p.Warnings, Blockers: p.Blockers, NewName: p.NewName, OtherAcct: p.Placement.OtherAccount,
		SetAside: len(p.SetAside), NativeCopy: p.NativeCopy, Options: p.Options, SessionKey: p.Key, SourceAgent: e.Agent,
		Can: CanDTO{Fork: agent.Has(tm, agent.CapFork), RemoteControl: agent.Has(tm, agent.CapRemoteControl),
			App: agent.Has(tm, agent.CapApp), Notify: agent.Has(tm, agent.CapNotify), Native: agent.Has(tm, agent.CapNativeReplay),
			Import: importsFrom(tm, e.Agent)}}
	if c := p.Continue; c != nil {
		d.Continue = &ContinueDTO{From: c.From, Fidelity: string(c.Fidelity), Relation: c.Relation, Report: c.Report, Briefing: c.Briefing, Via: c.Via}
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
	Kind       string   `json:"kind"`
	Title      string   `json:"title"`
	Agent      string   `json:"agent"`
	Command    string   `json:"command"`
	Files      int      `json:"files"`
	Bytes      string   `json:"bytes"`
	Paths      int      `json:"paths"`
	Secrets    int      `json:"secrets"`
	Redacted   bool     `json:"redacted"`
	Cloned     bool     `json:"cloned"`
	Worktree   string   `json:"worktree"`
	Journal    string   `json:"journal"`
	SourceHost string   `json:"sourceHost"`
	Stopped    bool     `json:"stopped"`
	Pushed     string   `json:"pushed"`
	PushError  string   `json:"pushError"`
	SyncNote   string   `json:"syncNote"`
	SyncState  string   `json:"syncState"`
	Mark       string   `json:"mark"`
	MarkError  string   `json:"markError"`
	Notice     string   `json:"notice"`
	Warnings   []string `json:"warnings"`
	InApp      bool     `json:"inApp"`             // it opens in the agent's desktop app
	Machine    string   `json:"machine,omitempty"` // a push: where it went (start it there)
	AuditDir   string   `json:"auditDir"`
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
	d := &DoneDTO{Kind: p.Kind, Title: p.Title, Agent: p.Agent, Command: res.Command, Files: res.Files, Bytes: move.Human(res.Bytes),
		Secrets: res.Secrets.Total, Redacted: p.Options.Redact, Cloned: res.Cloned, Worktree: res.Worktree, Journal: res.Journal,
		SourceHost: p.Source.Location, Stopped: res.Stopped, Pushed: res.Pushed, PushError: res.PushError, SyncNote: res.SyncNote,
		Mark: res.Mark, MarkError: res.MarkError, Notice: res.Notice, Warnings: res.Warnings, InApp: p.Options.App,
		AuditDir: filepath.Join(config.StateDir(), "log")}
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
// app when that was chosen, otherwise in a new terminal window.
func (a *App) OpenResult() error {
	a.mu.Lock()
	p, res := a.plan, a.res
	a.mu.Unlock()
	if p == nil || res == nil {
		return errors.New("nothing was moved")
	}
	if p.Options.App {
		return start(p.Resume)
	}
	if res.Command == "" {
		return errors.New("there is nothing to open")
	}
	return terminal(res.Command)
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
	c, err := core.Resume(inv, e, agent.ResumeOptions{App: inApp})
	if err != nil {
		return err
	}
	if inApp {
		return start(c)
	}
	return terminal(launch.Shell(c, "", launch.DefaultShell()))
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
	cmd := proc.Command(c.Argv[0], c.Argv[1:]...)
	cmd.Dir = c.Dir
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// openTerminal runs a shell line (built by hopsesh, never taken from the window) in a new
// terminal window.
func openTerminal(line string) error {
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf("tell application \"Terminal\"\n\tactivate\n\tdo script %s\nend tell", appleScriptString(line))
		return proc.Command("osascript", "-e", script).Run()
	case "windows":
		return openWindowsTerminal(line)
	}
	for _, t := range [][]string{{"x-terminal-emulator", "-e"}, {"gnome-terminal", "--"}, {"konsole", "-e"}, {"xterm", "-e"}} {
		if _, err := exec.LookPath(t[0]); err == nil {
			return proc.Command(t[0], append(t[1:], "sh", "-c", line+"; exec $SHELL")...).Start()
		}
	}
	return errors.New("no terminal emulator found; copy the command instead")
}

func appleScriptString(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

func nonEmpty(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
