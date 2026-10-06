// Package tui is hopsesh's interactive terminal UI: sessions of every agent grouped by
// repository, then plan, confirm and move (or continue in another agent), ending with the
// command to continue.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/launch"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Exit is what the TUI asks its caller to do after it closes.
type Exit struct {
	RunDir  string   // start the agent here...
	RunArgv []string // ...with these arguments
	Prompt  string   // a file holding the last argument, if any
	// Unset are variables the program runs without (a cloud driver's).
	Unset []string
	// Adopt is a fetch (its journal) the program brings a session for: once it ends, the
	// caller adopts what it wrote and opens the TUI again on what came back (Deps.Adopted).
	Adopt string
	// Hop is the hop (its journal) whose first leg the program is: once it ends, the caller
	// adopts the copy, takes the hop on, and opens the TUI on it (Deps.Hop).
	Hop string
	// Key, Title and Agent name the session a resume runs (Key empty: a driver's
	// command), for the tab's labels and hopsesh's record of where it runs.
	Key   agent.SessionKey
	Title string
	Agent string
}

// Deps are what the TUI needs from the CLI.
type Deps struct {
	App      *app.App
	Describe func(app.Entry) string // branch/worktree line
	// Adopted is a fetch (its journal) to show first: what came back from a cloud.
	Adopted string
	// Hop is a hop (its journal) to show first.
	Hop string
}

type mode int

const (
	modeLoading mode = iota
	modeBrowse
	modePlan
	modeApplying
	modeDone
	modeError
	modeBrought // what came back from a cloud
	modeJourney
)

type row struct {
	header string
	item   *app.Item
}

type model struct {
	deps          Deps
	copied        bool // the resume command was copied on the done screen
	mode          mode
	width         int
	height        int
	inv           *app.Inventory
	rows          []row
	cursor        int
	offset        int
	filter        string
	editing       bool
	opts          move.Options
	target        agent.ID // the agent to continue in ("" = the session's own)
	plan          *move.Plan
	destinations  []agent.Summary
	journeyOffset int
	planning      bool // a new plan is being worked out; the one shown is out of date
	input         move.Input
	sel           row
	result        *move.Result
	err           error
	exit          *Exit
	started       time.Time
	// Clouds: a session planned from a pasted link or the vendor's picker (not a row), what
	// came back from a cloud, and the link being pasted.
	picked  *app.Entry
	brought *app.Brought
	pasting bool
	paste   string
	notice  string
	// ho is a hand-off to a cloud being chosen or planned.
	ho handoff
	// stepPaste asks for the link of a session a terminal step did not show.
	stepPaste *stepPaste
	// steps receives what the hand-off sends the program (tests: no program runs).
	steps chan tea.Msg
}

type scanDone struct{ inv *app.Inventory }
type planDone struct {
	plan  *move.Plan
	input move.Input
	err   error
}
type applyDone struct {
	res *move.Result
	err error
}

// Run starts the TUI and returns what to do afterwards (nil = nothing).
func Run(d Deps) (*Exit, error) {
	opts := d.App.DefaultOptions()
	opts.Worktree = move.WorktreeAuto
	m := &model{deps: d, mode: modeLoading, started: time.Now(), opts: opts}
	prog := tea.NewProgram(m)
	// A hand-off whose driver needs a terminal gets this one, the UI paused meanwhile.
	prev := d.App.Steps
	d.App.Steps = stepper(prog.Send)
	defer func() { d.App.Steps = prev }()
	final, err := prog.Run()
	if err != nil {
		return nil, err
	}
	fm := final.(*model)
	if fm.inv != nil {
		fm.inv.Close()
	}
	return fm.exit, nil
}

func (m *model) Init() tea.Cmd {
	a := m.deps.App
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		return scanDone{a.Scan(ctx, app.ScanOptions{})}
	}
}

func (m *model) buildRows() {
	m.rows = m.rows[:0]
	groups := m.inv.Groups(m.deps.App.LocalRoots())
	f := strings.ToLower(m.filter)
	for _, g := range groups {
		var rows []row
		for i := range g.Items {
			it := &g.Items[i]
			e := it.Entry
			s := e.Session
			if f != "" && !strings.Contains(strings.ToLower(s.Title+" "+s.LastPrompt+" "+s.CWD+" "+g.Name+" "+e.Machine+" "+e.AgentName), f) {
				continue
			}
			rows = append(rows, row{item: it})
		}
		if len(rows) == 0 {
			continue
		}
		h := g.Name
		if g.Remote != "" {
			h += "  " + g.Remote
		}
		if g.Local != "" {
			h += "  · here: " + g.Local
		} else if g.Identity != "" && !strings.HasPrefix(g.Identity, "local:") {
			h += "  · not cloned here"
		}
		m.rows = append(m.rows, row{header: h})
		m.rows = append(m.rows, rows...)
	}
	m.cursor = m.nextSelectable(0, 1)
	m.offset = 0
}

func (m *model) nextSelectable(from, dir int) int {
	for i := from; i >= 0 && i < len(m.rows); i += dir {
		if m.rows[i].item != nil {
			return i
		}
	}
	return m.cursor
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case scanDone:
		if m.inv != nil {
			m.inv.Close()
		}
		m.inv = msg.inv
		m.mode = modeBrowse
		m.buildRows()
		if j := m.deps.Hop; j != "" {
			m.deps.Hop = ""
			m.showHop(j)
		} else if j := m.deps.Adopted; j != "" {
			m.deps.Adopted = ""
			if f, err := m.deps.App.Adopt(context.Background(), j, false); err == nil {
				b := m.deps.App.Brought(f)
				m.brought, m.mode = &b, modeBrought
			}
		}
	case pickedDone:
		e := msg.entry
		m.picked = &e
		return m.Update(msg.plan)
	case undoDone:
		if msg.err != nil {
			m.err, m.mode = msg.err, modeError
			return m, nil
		}
		m.brought, m.notice, m.mode = nil, "", modeLoading
		return m, m.Init()
	case planDone:
		m.planning = false
		if msg.err != nil {
			m.err, m.mode = msg.err, modeError
		} else {
			m.plan, m.input, m.mode = msg.plan, msg.input, modePlan
			if len(msg.plan.Destinations) > 0 || m.opts.TargetSession == "" {
				m.destinations = msg.plan.Destinations
			}
		}
	case briefEdited:
		return m.briefDone(msg)
	case stepRun:
		return m.runStep(msg)
	case stepRan:
		return m.stepDone(msg)
	case tea.PasteMsg:
		if m.stepPaste != nil {
			m.stepPaste.text += strings.TrimSpace(msg.Content)
		}
	case applyDone:
		if msg.res != nil && msg.res.Hop != nil {
			m.result, m.mode = msg.res, modeDone // waiting, done, or stopped: the hop's view says
			return m, nil
		}
		if msg.res != nil && msg.res.Handoff != nil {
			m.result, m.mode = msg.res, modeDone // a failed step is shown with the steps
			return m, nil
		}
		if msg.err != nil {
			m.err, m.mode = msg.err, modeError
			break
		}
		m.result, m.mode = msg.res, modeDone
		if f := msg.res.Fetch; f != nil && f.Outcome == move.FetchComplete {
			// Written here at once (no terminal step): show what came back.
			if rec, err := m.deps.App.Adopt(context.Background(), msg.res.Journal, false); err == nil {
				b := m.deps.App.Brought(rec)
				m.brought, m.mode = &b, modeBrought
			}
		}
	case tea.KeyPressMsg:
		return m.key(msg.String())
	}
	return m, nil
}

func (m *model) key(k string) (tea.Model, tea.Cmd) {
	if m.stepPaste != nil {
		return m.stepPasteKey(k)
	}
	if k == "ctrl+c" {
		return m, tea.Quit
	}
	if m.pasting {
		return m.pasteKey(k)
	}
	if m.mode == modeBrought {
		return m.broughtKeys(k)
	}
	if m.ho.picking {
		return m.pickKeys(k)
	}
	if m.mode == modePlan && m.plan.Kind == move.KindHandoff {
		return m.handoffKeys(k)
	}
	if m.mode == modePlan && m.plan.Kind == move.KindHop {
		return m.hopKeys(k)
	}
	if m.mode == modeDone && m.plan.Kind == move.KindHop {
		return m.hopDoneKeys(k)
	}
	if m.mode == modeDone && m.plan.Kind == move.KindHandoff {
		return m.handoffDoneKeys(k)
	}
	if m.editing {
		switch k {
		case "enter", "esc":
			m.editing = false
		case "backspace":
			if len(m.filter) > 0 {
				m.filter = m.filter[:len(m.filter)-1]
			}
			m.buildRows()
		default:
			if len(k) == 1 {
				m.filter += k
				m.buildRows()
			} else if k == "space" {
				m.filter += " "
				m.buildRows()
			}
		}
		return m, nil
	}
	switch m.mode {
	case modeJourney:
		switch k {
		case "q", "esc", "h":
			m.mode = modeBrowse
		case "up", "k":
			m.journeyOffset = max(0, m.journeyOffset-1)
		case "down", "j":
			m.journeyOffset = min(max(0, len(m.journeyLines())-max(1, m.height-5)), m.journeyOffset+1)
		case "pgdown":
			m.journeyOffset = min(max(0, len(m.journeyLines())-max(1, m.height-5)), m.journeyOffset+max(1, m.height-5))
		case "pgup":
			m.journeyOffset = max(0, m.journeyOffset-max(1, m.height-5))
		}
	case modeBrowse:
		switch k {
		case "h":
			if m.cursor < len(m.rows) && m.rows[m.cursor].item != nil && m.rows[m.cursor].item.Entry.Lineage != nil {
				m.sel, m.mode, m.journeyOffset = m.rows[m.cursor], modeJourney, 0
			}
		case "q", "esc":
			return m, tea.Quit
		case "up", "k":
			m.cursor = m.nextSelectable(m.cursor-1, -1)
		case "down", "j":
			m.cursor = m.nextSelectable(m.cursor+1, 1)
		case "pgdown":
			m.cursor = m.nextSelectable(min(m.cursor+m.listHeight(), len(m.rows)-1), -1)
		case "pgup":
			m.cursor = m.nextSelectable(max(m.cursor-m.listHeight(), 0), 1)
		case "/":
			m.editing = true
		case "r":
			m.mode = modeLoading
			return m, m.Init()
		case "enter":
			if m.cursor < len(m.rows) && m.rows[m.cursor].item != nil {
				m.sel, m.target, m.picked, m.ho = m.rows[m.cursor], "", nil, handoff{}
				m.opts.TargetSession, m.destinations = "", nil
				return m, m.planCmd()
			}
		case "c":
			m.openHandoff()
		case "i":
			if m.cursor < len(m.rows) && m.rows[m.cursor].item != nil {
				m.sel, m.target, m.picked, m.ho = m.rows[m.cursor], "", nil, handoff{}
				m.opts.TargetSession, m.destinations = "", nil
				if m.target = m.nextAgent(); m.target != "" {
					return m, m.planCmd()
				}
			}
		case "p":
			if m.partialCloud() != nil {
				m.pasting, m.paste = true, ""
			}
		case "f":
			if c := m.partialCloud(); c != nil {
				m.target = ""
				return m, m.fetchCmd(c.Name, "")
			}
		}
	case modePlan:
		if m.plan.Kind == move.KindFetch && !m.planning {
			if mm, cmd, ok := m.fetchKeys(k); ok {
				return mm, cmd
			}
		}
		switch k {
		case "esc", "q":
			m.mode = modeBrowse
		case "y", "enter":
			if !m.planning && len(m.plan.Blockers) == 0 { // never the plan being replaced
				m.mode = modeApplying
				return m, m.applyCmd()
			}
		case "d":
			if !m.planning && m.cycleDestination() {
				return m, m.planCmd()
			}
		case "c":
			m.opts.Clone = !m.opts.Clone
			return m, m.planCmd()
		case "a":
			m.target = m.nextAgent()
			m.opts.TargetSession, m.destinations = "", nil
			return m, m.planCmd()
		case "w":
			m.opts.Worktree = map[move.WorktreeMode]move.WorktreeMode{move.WorktreeAuto: move.WorktreeCreate,
				move.WorktreeCreate: move.WorktreeMain, move.WorktreeMain: move.WorktreeAuto}[m.opts.Worktree]
			return m, m.planCmd()
		case "r":
			m.opts.RemoteControl = !m.opts.RemoteControl
			return m, m.planCmd()
		case "n":
			m.opts.Notify = !m.opts.Notify
			return m, m.planCmd()
		case "f":
			m.opts.Fork = !m.opts.Fork
			return m, m.planCmd()
		case "x":
			m.opts.Redact = !m.opts.Redact
			return m, m.planCmd()
		case "m":
			m.opts.Mark = !m.opts.Mark
			return m, m.planCmd()
		case "s":
			m.opts.SyncCode = !m.opts.SyncCode
			return m, m.planCmd()
		case "p":
			m.opts.Push = !m.opts.Push
			return m, m.planCmd()
		case "k":
			m.opts.StopLocal = !m.opts.StopLocal
			return m, m.planCmd()
		case "R":
			m.opts.Conflict = map[bool]string{true: "", false: move.ConflictReplace}[m.opts.Conflict == move.ConflictReplace]
			return m, m.planCmd()
		case "B":
			m.opts.Conflict = map[bool]string{true: "", false: move.ConflictKeepBoth}[m.opts.Conflict == move.ConflictKeepBoth]
			return m, m.planCmd()
		}
	case modeDone:
		if m.plan.Kind == move.KindFetch {
			switch k {
			case "enter":
				if m.result.Fetch.Outcome == move.FetchWaiting {
					r := m.plan.Fetch.Run
					m.exit = &Exit{RunDir: r.Dir, RunArgv: r.Argv, Unset: r.Unset, Adopt: m.result.Journal}
					return m, tea.Quit
				}
			case "c":
				m.copied = true
				return m, tea.SetClipboard(m.result.Command)
			case "u":
				return m, m.undoCmd(m.result.Journal)
			case "q", "esc":
				return m, tea.Quit
			}
			return m, nil
		}
		switch k {
		case "enter":
			m.exit = &Exit{RunDir: m.plan.Resume.Dir, RunArgv: m.plan.Resume.Argv, Prompt: m.result.PromptFile,
				Key: m.plan.Placement.Key, Title: m.plan.Title, Agent: m.plan.Agent}
			return m, tea.Quit
		case "c":
			m.copied = true
			return m, tea.SetClipboard(m.result.Command)
		case "q", "esc":
			return m, tea.Quit
		}
	case modeError:
		switch k {
		case "q":
			return m, tea.Quit
		default:
			m.err, m.mode = nil, modeBrowse
		}
	case modeLoading, modeApplying:
		if k == "q" {
			return m, tea.Quit
		}
	}
	m.scroll()
	return m, nil
}

func (m *model) planCmd() tea.Cmd {
	if m.ho.cloud != "" {
		return m.handoffPlanCmd()
	}
	if m.picked != nil {
		return m.replan()
	}
	m.planning = true
	a, inv, e, target, opts := m.deps.App, m.inv, m.sel.item.Entry, m.target, m.opts
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		p, in, err := a.Plan(ctx, inv, e, target, opts)
		return planDone{p, in, err}
	}
}

func (m *model) applyCmd() tea.Cmd {
	a, p, in := m.deps.App, m.plan, m.input
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		res, err := a.Apply(ctx, p, in, nil)
		if err == nil && p.Handoff != nil && a.RememberEnv(p) {
			_ = config.Save(a.Cfg)
		}
		return applyDone{res, err}
	}
}

// nextAgent cycles the agent to continue in: the session's own, then each other agent
// installed here that can take a conversation.
func (m *model) nextAgent() agent.ID {
	here := m.inv.Local()
	if here == nil {
		return ""
	}
	own := m.sel.item.Entry.Agent
	var cands []agent.ID
	for _, a := range here.Agents {
		mod, ok := m.deps.App.Module(a.Agent)
		if a.Agent == own || !a.Install.Present || !ok || !agent.Has(mod, agent.CapWrite) {
			continue
		}
		cands = append(cands, a.Agent)
	}
	cur := m.target
	if om, ok := m.deps.App.Module(own); ok && !agent.Has(om, agent.CapWrite) {
		// A cloud-only agent keeps nothing here: its text goes into one of the others, the
		// plan's (its default) first.
		if cur == "" && m.plan != nil && m.plan.Fetch != nil {
			cur = m.plan.Fetch.ContinueIn
		}
		for i, id := range cands {
			if id == cur {
				return cands[(i+1)%len(cands)]
			}
		}
		if len(cands) > 0 {
			return cands[0]
		}
		return ""
	}
	if cur == "" {
		cur = own
	}
	all := append([]agent.ID{own}, cands...)
	for i, id := range all {
		if id == cur {
			next := all[(i+1)%len(all)]
			if next == own {
				return ""
			}
			return next
		}
	}
	return ""
}

func (m *model) listHeight() int {
	h := m.height - 12
	if h < 5 {
		h = 5
	}
	return h
}

func (m *model) scroll() {
	h := m.listHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
		if m.offset > 0 && m.rows[m.offset-1].header != "" {
			m.offset--
		}
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
}

var (
	accent   = lipgloss.Color("#2bb3a3")
	dim      = lipgloss.NewStyle().Foreground(lipgloss.Color("#8a8778"))
	bold     = lipgloss.NewStyle().Bold(true)
	headerSt = lipgloss.NewStyle().Foreground(accent).Bold(true)
	selSt    = lipgloss.NewStyle().Background(lipgloss.Color("#0b6b62")).Foreground(lipgloss.Color("#ffffff"))
	warnSt   = lipgloss.NewStyle().Foreground(lipgloss.Color("#e3b341"))
	errSt    = lipgloss.NewStyle().Foreground(lipgloss.Color("#e0605a"))
	okSt     = lipgloss.NewStyle().Foreground(lipgloss.Color("#5cb85c"))
	liveSt   = lipgloss.NewStyle().Foreground(lipgloss.Color("#8fd19e"))
)

func (m *model) View() tea.View {
	var b strings.Builder
	title := bold.Render("hopsesh") + dim.Render("  coding-agent sessions on your machines")
	b.WriteString(title + "\n")
	switch m.mode {
	case modeLoading:
		fmt.Fprintf(&b, "\n  Scanning this machine and %d allowed machine(s)… %s\n", countAllowed(m.deps.App), dim.Render(time.Since(m.started).Truncate(time.Second).String()))
	case modeBrowse:
		m.viewBrowse(&b)
	case modeJourney:
		m.viewJourney(&b)
	case modePlan:
		switch m.plan.Kind {
		case move.KindFetch:
			m.viewFetchPlan(&b)
		case move.KindHandoff:
			m.viewHandoffPlan(&b)
		case move.KindHop:
			m.viewHopPlan(&b)
		default:
			m.viewPlan(&b)
		}
	case modeApplying:
		switch m.plan.Kind {
		case move.KindHandoff:
			if m.stepPaste != nil {
				m.viewStepPaste(&b)
				break
			}
			fmt.Fprintf(&b, "\n  Handing %q off to %s… (snapshot, push, start the cloud session)\n", m.plan.Title, m.plan.Handoff.CloudTitle)
			if t := m.plan.Handoff.Terminal; t != "" {
				b.WriteString("  " + dim.Render(t) + "\n")
			}
		case move.KindFetch:
			fmt.Fprintf(&b, "\n  Preparing a worktree for %q…\n", m.plan.Title)
		case move.KindHop:
			if m.stepPaste != nil {
				m.viewStepPaste(&b)
				break
			}
			fmt.Fprintf(&b, "\n  Handing %q on to %s: bringing it here first…\n", m.plan.Title, m.plan.Hop.ToTitle)
		default:
			fmt.Fprintf(&b, "\n  Moving %q… (copying or converting, rewriting, verifying)\n", m.plan.Title)
		}
	case modeDone:
		switch m.plan.Kind {
		case move.KindHop:
			m.viewHopDone(&b)
		case move.KindHandoff:
			m.viewHandoffDone(&b)
		case move.KindFetch:
			m.viewFetchDone(&b)
		default:
			m.viewDone(&b)
		}
	case modeBrought:
		m.viewBrought(&b)
	case modeError:
		fmt.Fprintf(&b, "\n  %s\n\n  %s\n", errSt.Render("Error"), m.err)
		b.WriteString(dim.Render("\n  any key: back · q: quit\n"))
	}
	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

func countAllowed(a *app.App) int {
	n := 0
	for _, h := range a.Cfg.Hosts {
		if h.Allowed {
			n++
		}
	}
	return n
}

func (m *model) viewBrowse(b *strings.Builder) {
	var mach []string
	for _, mc := range m.inv.Machines {
		n := 0
		for _, e := range m.inv.Entries {
			if e.Machine == mc.Name {
				n++
			}
		}
		if mc.Status == app.StatusOK {
			mach = append(mach, okSt.Render("●")+fmt.Sprintf(" %s %d", mc.Name, n))
		} else {
			mach = append(mach, errSt.Render("●")+" "+mc.Name+" "+mc.Status)
		}
	}
	for _, c := range m.shownClouds() {
		mach = append(mach, cloudHeader(c))
	}
	b.WriteString(dim.Render(fmt.Sprintf("%d sessions · ", len(m.inv.Entries))) + strings.Join(mach, "   ") + "\n")
	switch pc := m.partialCloud(); {
	case m.pasting:
		b.WriteString("  paste a link: " + m.paste + "▏" + dim.Render("  (enter: plan it · esc: cancel)") + "\n")
	case m.editing || m.filter != "":
		cur := ""
		if m.editing {
			cur = "▏"
		}
		b.WriteString("  / " + m.filter + cur + "\n")
	case pc != nil:
		b.WriteString(dim.Render(truncate(fmt.Sprintf("  %s: the sessions hopsesh started or brought here, and Remote Control mirrors · %s shows the rest: f find in %s · p paste a link",
			pc.Name, pc.AgentName, pc.AgentName), max(m.width, 80)-1)) + "\n")
	default:
		b.WriteString("\n")
	}
	h := m.listHeight()
	end := min(len(m.rows), m.offset+h)
	w := max(m.width, 80)
	for i := m.offset; i < end; i++ {
		r := m.rows[i]
		if r.header != "" {
			b.WriteString(headerSt.Render(truncate("▾ "+r.header, w-2)) + "\n")
			continue
		}
		e := r.item.Entry
		s := e.Session
		status := e.Status()
		switch {
		case e.Live.State == agent.Live:
			status = liveSt.Render(status)
		case e.Cloud != nil:
			status = cloudSt.Render(status)
		}
		if mr := s.Mirror; mr != nil {
			status += dim.Render(" · " + mirrorWords(mr))
		}
		line := fmt.Sprintf("  %-12s %-11s %-40s %-9s %s", truncate(e.Machine, 12), truncate(e.AgentName, 11), truncate(s.Title, 40), ago(s.LastActivity), status)
		if i == m.cursor {
			line = selSt.Render(padRight(truncate(line, w-1), w-1))
		}
		b.WriteString(line + "\n")
	}
	for i := end - m.offset; i < h; i++ {
		b.WriteString("\n")
	}
	if m.cursor < len(m.rows) && m.rows[m.cursor].item != nil {
		it := m.rows[m.cursor].item
		e := it.Entry
		s := e.Session
		b.WriteString(dim.Render(strings.Repeat("─", min(w, 120))) + "\n")
		fmt.Fprintf(b, "  %s  %s\n", bold.Render(truncate(s.Title, w-24)), dim.Render(s.Key.String()))
		if c := e.Cloud; c != nil {
			head := e.Location.Name + " · " + e.AgentName + " · " + e.Status() + "  "
			fmt.Fprintf(b, "  %s%s\n", cloudSt.Render(head), link(c.URL, truncate(c.URL, w-6-len([]rune(head)))))
			var bits []string
			if c.Repo != "" {
				bits = append(bits, c.Repo)
			}
			if c.Branch != "" {
				bits = append(bits, "branch "+c.Branch)
			}
			if e.Checkout != "" {
				bits = append(bits, "here at "+e.Checkout)
			}
			if len(bits) > 0 {
				fmt.Fprintf(b, "  %s\n", dim.Render(truncate(strings.Join(bits, " · "), w-4)))
			}
			fmt.Fprintf(b, "  %s\n", dim.Render("enter: bring it here · i: and continue in another agent · c: hand it on to another cloud"))
			if m.ho.picking {
				m.viewPicker(b)
				return
			}
			b.WriteString(dim.Render("\n  ↑↓ move · enter bring here · i bring and continue in · c hand on · / search · r refresh · q quit\n"))
			return
		}
		fmt.Fprintf(b, "  %s  %s %s  %s\n", e.Machine, e.AgentName, s.AgentVersion, s.CWD)
		if m.deps.Describe != nil {
			if d := m.deps.Describe(e); d != "" {
				fmt.Fprintf(b, "  %s\n", d)
			}
		}
		fmt.Fprintf(b, "  %s\n", dim.Render(truncate("last prompt: “"+s.LastPrompt+"”", w-4)))
		if e.Lineage != nil {
			j := e.Lineage.Journey()
			fork := ""
			if j.Fork {
				fork = " · separate fork"
			}
			fmt.Fprintf(b, "  %d transfers · %d round trips to origin · %d returns%s\n", j.Transfers, j.RoundTrips, j.Returns, fork)
		}
		if e.LineageError != "" {
			fmt.Fprintf(b, "  lineage unavailable: %s\n", e.LineageError)
		}
		if len(it.Copies) > 1 {
			var parts []string
			for _, c := range it.Copies {
				p := c.Machine + " " + c.AgentName
				switch {
				case c.Newest:
					p += " (newest)"
				case c.Mark != nil:
					p += " (" + app.MarkWords(*c.Mark) + ")"
				default:
					p += " (older)"
				}
				parts = append(parts, p)
			}
			fmt.Fprintf(b, "  %s\n", warnSt.Render(truncate("copies: "+strings.Join(parts, ", "), w-4)))
		}
	}
	if m.ho.picking {
		m.viewPicker(b)
		return
	}
	hint := "\n  ↑↓ move · enter bring here · i continue in · c hand off · h journey · / search · r refresh · q quit"
	if m.partialCloud() != nil {
		hint = "\n  ↑↓ move · enter resume/bring · i continue in · c hand off · p paste a cloud link · f find in a cloud · / search · r refresh · q quit"
	}
	b.WriteString(dim.Render(hint) + "\n")
}

func (m *model) viewPlan(b *strings.Builder) {
	p := m.plan
	verb := "Move"
	if p.Kind == move.KindContinue {
		verb = "Continue in " + p.Agent
	}
	if p.NoWork {
		verb = "Sync lineage receipts for"
	}
	fmt.Fprintf(b, "\n  %s %q\n", bold.Render(verb), p.Title)
	if len(m.destinations) > 0 {
		b.WriteString("  [d] choose destination session:\n")
		for _, destination := range m.destinations {
			selected := "  "
			if m.opts.TargetSession == destination.Key.String() {
				selected = "→ "
			}
			fmt.Fprintf(b, "    %s%s · %s\n", selected, destination.Title, destination.Key.String())
		}
	}
	if m.planning {
		b.WriteString("  updating the plan…\n")
	}
	fmt.Fprintf(b, "  from  %s  %s (%s)\n  to    this machine  %s\n", p.Source.Location, p.Source.CWD, p.Key.Agent, p.Target.CWD)
	r := p.Repo
	switch r.Action {
	case move.RepoUse:
		fmt.Fprintf(b, "  repo  %s at %s (on %s)\n", r.Identity, r.LocalPath, r.LocalBranch)
	case move.RepoClone:
		fmt.Fprintf(b, "  repo  %s → will clone into %s\n", r.Identity, r.LocalPath)
	case move.RepoNeedsClone:
		fmt.Fprintf(b, "  repo  %s is not here — press c to clone into %s\n", r.Identity, r.LocalPath)
	case move.RepoNone:
		b.WriteString("  dir   not a git repository\n")
	}
	if r.Worktree != "" {
		fmt.Fprintf(b, "  worktree → %s\n", r.Worktree)
	}
	if p.NoWork {
		fmt.Fprintf(b, "  existing session %s · 0 new messages, 0 transfers\n", p.Placement.Key)
	} else if c := p.Continue; c != nil {
		if c.Relation == move.RelationAppend {
			fmt.Fprintf(b, "  adds the new work to %s here\n", c.AppendTo.Key)
		}
		fmt.Fprintf(b, "  carries %s\n", c.Report.Summary)
	} else {
		fmt.Fprintf(b, "  files %d (%s) · %d path mapping(s)\n", len(p.Files.Files), move.Human(p.Bytes), len(p.Placement.Mappings))
	}
	if p.StopHere {
		b.WriteString("  first quit the copy open here\n")
	}
	if p.Push {
		fmt.Fprintf(b, "  push  %d unpushed commit(s) on %s first\n", p.Repo.Unpushed, p.Source.Location)
	}
	if p.Sync != "" {
		fmt.Fprintf(b, "  code  %s\n", p.Sync)
	}
	switch p.Mark {
	case move.MarkNow:
		fmt.Fprintf(b, "  mark  the copy on %s is marked\n", p.Source.Location)
	case move.MarkWhenStopped:
		fmt.Fprintf(b, "  mark  the copy on %s is open; marked once it ends\n", p.Source.Location)
	}
	for _, w := range p.Warnings {
		b.WriteString("  " + warnSt.Render("! "+w) + "\n")
	}
	for _, bl := range p.Blockers {
		b.WriteString("  " + errSt.Render("✗ "+bl) + "\n")
	}
	on := func(v bool) string {
		if v {
			return okSt.Render("on ")
		}
		return dim.Render("off")
	}
	target := p.Agent
	fmt.Fprintf(b, "\n  [a] agent %s  [c] clone %s  [w] worktree %s  [r] remote control %s  [n] notify old %s  [f] fork %s  [x] redact %s\n",
		target, on(m.opts.Clone), string(m.opts.Worktree), on(m.opts.RemoteControl), on(m.opts.Notify), on(m.opts.Fork), on(m.opts.Redact))
	fmt.Fprintf(b, "  [m] mark old copy %s  [s] sync code %s  [p] push on %s %s  [k] quit copy open here %s\n",
		on(m.opts.Mark), on(m.opts.SyncCode), p.Source.Location, on(m.opts.Push), on(m.opts.StopLocal))
	if p.Conflict != "" || m.opts.Conflict != "" {
		fmt.Fprintf(b, "  both copies changed: [R] replace the one here %s  [B] keep both %s\n", on(m.opts.Conflict == move.ConflictReplace), on(m.opts.Conflict == move.ConflictKeepBoth))
	}
	if len(p.Blockers) == 0 {
		b.WriteString(dim.Render("\n  y/enter: go · esc: back\n"))
	} else {
		b.WriteString(dim.Render("\n  resolve the ✗ first · esc: back\n"))
	}
}

func (m *model) viewDone(b *strings.Builder) {
	p, res := m.plan, m.result
	if p.NoWork {
		fmt.Fprintf(b, "\n  %s %q already synchronized · 0 new messages, 0 transfers.\n", okSt.Render("✓"), p.Title)
	} else if p.Kind == move.KindContinue {
		fmt.Fprintf(b, "\n  %s %q continues in %s.\n", okSt.Render("✓"), p.Title, p.Agent)
	} else {
		fmt.Fprintf(b, "\n  %s %q is on this machine: %d file(s), %s.\n", okSt.Render("✓"), p.Title, res.Files, move.Human(res.Bytes))
	}
	if res.Secrets.Total > 0 {
		fmt.Fprintf(b, "  %d likely secret(s)\n", res.Secrets.Total)
	}
	if res.PushError != "" {
		b.WriteString("  " + warnSt.Render("! could not push on "+p.Source.Location+": "+res.PushError) + "\n")
	} else if res.Pushed != "" {
		fmt.Fprintf(b, "  pushed %s on %s\n", p.Repo.SourceBranch, p.Source.Location)
	}
	if res.SyncNote != "" {
		fmt.Fprintf(b, "  code: %s\n", res.SyncNote)
	}
	switch res.Mark {
	case "done":
		fmt.Fprintf(b, "  the copy on %s is marked\n", p.Source.Location)
	case "pending":
		fmt.Fprintf(b, "  the copy on %s is marked once it ends\n", p.Source.Location)
	case "failed":
		b.WriteString("  " + warnSt.Render("! could not mark the copy on "+p.Source.Location+": "+res.MarkError) + "\n")
	}
	b.WriteString("\n  Continue it:\n\n")
	family := launch.DefaultShell()
	for _, line := range wrapCommand(res.Command, max(m.width, 80)-4, family) {
		b.WriteString("  " + line + "\n")
	}
	if m.copied {
		b.WriteString(okSt.Render("\n  Copied to the clipboard.") + "\n")
	}
	b.WriteString(dim.Render("\n  enter: start it there now · c: copy the command · q: quit (undo with: hopsesh undo " + res.Journal + ")\n"))
}

// wrapCommand breaks a long shell command at spaces outside quotes, ending each broken
// line with the shell's continuation character, so the command stays whole and
// copyable on a narrow terminal.
func wrapCommand(cmd string, width int, family string) []string {
	cont := " \\"
	if family == "powershell" {
		cont = " `"
	}
	var tokens []string
	var cur strings.Builder
	var quote rune
	depth := 0 // inside $( … )
	for _, r := range cmd {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case r == '(':
			depth++
		case r == ')' && depth > 0:
			depth--
		case r == ' ' && depth == 0:
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	var lines []string
	line := ""
	for _, t := range tokens {
		switch {
		case line == "":
			line = t
		case len(line)+1+len(t)+len(cont) > width:
			lines = append(lines, line+cont)
			line = "    " + t
		default:
			line += " " + t
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return t.Local().Format("2 Jan")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if n <= 0 || len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func padRight(s string, n int) string {
	if l := len([]rune(s)); l < n {
		return s + strings.Repeat(" ", n-l)
	}
	return s
}
