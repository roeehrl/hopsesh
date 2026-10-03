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
	"github.com/roeehrl/hopsesh/internal/core/launch"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Exit is what the TUI asks its caller to do after it closes.
type Exit struct {
	RunDir  string   // start the agent here...
	RunArgv []string // ...with these arguments
	Prompt  string   // a file holding the last argument, if any
}

// Deps are what the TUI needs from the CLI.
type Deps struct {
	App      *app.App
	Describe func(app.Entry) string // branch/worktree line
}

type mode int

const (
	modeLoading mode = iota
	modeBrowse
	modePlan
	modeApplying
	modeDone
	modeError
)

type row struct {
	header string
	item   *app.Item
}

type model struct {
	deps    Deps
	copied  bool // the resume command was copied on the done screen
	mode    mode
	width   int
	height  int
	inv     *app.Inventory
	rows    []row
	cursor  int
	offset  int
	filter  string
	editing bool
	opts    move.Options
	target  agent.ID // the agent to continue in ("" = the session's own)
	plan    *move.Plan
	input   move.Input
	sel     row
	result  *move.Result
	err     error
	exit    *Exit
	started time.Time
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
	final, err := tea.NewProgram(m).Run()
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
	case planDone:
		if msg.err != nil {
			m.err, m.mode = msg.err, modeError
		} else {
			m.plan, m.input, m.mode = msg.plan, msg.input, modePlan
		}
	case applyDone:
		if msg.err != nil {
			m.err, m.mode = msg.err, modeError
		} else {
			m.result, m.mode = msg.res, modeDone
		}
	case tea.KeyPressMsg:
		return m.key(msg.String())
	}
	return m, nil
}

func (m *model) key(k string) (tea.Model, tea.Cmd) {
	if k == "ctrl+c" {
		return m, tea.Quit
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
	case modeBrowse:
		switch k {
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
				m.sel, m.target = m.rows[m.cursor], ""
				return m, m.planCmd()
			}
		}
	case modePlan:
		switch k {
		case "esc", "q":
			m.mode = modeBrowse
		case "y", "enter":
			if len(m.plan.Blockers) == 0 {
				m.mode = modeApplying
				return m, m.applyCmd()
			}
		case "c":
			m.opts.Clone = !m.opts.Clone
			return m, m.planCmd()
		case "a":
			m.target = m.nextAgent()
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
		switch k {
		case "enter":
			m.exit = &Exit{RunDir: m.plan.Resume.Dir, RunArgv: m.plan.Resume.Argv, Prompt: m.result.PromptFile}
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
	case modePlan:
		m.viewPlan(&b)
	case modeApplying:
		fmt.Fprintf(&b, "\n  Moving %q… (copying or converting, rewriting, verifying)\n", m.plan.Title)
	case modeDone:
		m.viewDone(&b)
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
	b.WriteString(dim.Render(fmt.Sprintf("%d sessions · ", len(m.inv.Entries))) + strings.Join(mach, "   ") + "\n")
	if m.editing || m.filter != "" {
		cur := ""
		if m.editing {
			cur = "▏"
		}
		b.WriteString("  / " + m.filter + cur + "\n")
	} else {
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
		if e.Live.State == agent.Live {
			status = liveSt.Render(status)
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
		fmt.Fprintf(b, "  %s  %s %s  %s\n", e.Machine, e.AgentName, s.AgentVersion, s.CWD)
		if m.deps.Describe != nil {
			if d := m.deps.Describe(e); d != "" {
				fmt.Fprintf(b, "  %s\n", d)
			}
		}
		fmt.Fprintf(b, "  %s\n", dim.Render(truncate("last prompt: “"+s.LastPrompt+"”", w-4)))
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
	b.WriteString(dim.Render("\n  ↑↓ move · enter bring here (a: in another agent) · / search · r refresh · q quit\n"))
}

func (m *model) viewPlan(b *strings.Builder) {
	p := m.plan
	verb := "Move"
	if p.Kind == move.KindContinue {
		verb = "Continue in " + p.Agent
	}
	fmt.Fprintf(b, "\n  %s %q\n", bold.Render(verb), p.Title)
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
	if c := p.Continue; c != nil {
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
	if p.Kind == move.KindContinue {
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
