// Package tui is hopsesh's interactive terminal UI: machines → repositories → sessions,
// then plan, confirm and move, ending with the command to resume.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/link"
	"github.com/roeehrl/hopsesh/internal/core/moved"
	"github.com/roeehrl/hopsesh/internal/core/transport"
	"github.com/roeehrl/hopsesh/internal/engine"
	"github.com/roeehrl/hopsesh/internal/inventory"
)

// Exit is what the TUI asks its caller to do after it closes.
type Exit struct {
	RunDir  string   // start claude here...
	RunArgv []string // ...with these arguments (argv[0] is "claude")
}

// Deps are what the TUI needs from the CLI.
type Deps struct {
	Config   config.Config
	StateDir string
	Log      *audit.Log
	Roots    []string
	Describe func(*inventory.Session) string // branch/worktree line
	// Passwords answers ssh password questions for machines that log in with one.
	Passwords func(config.Host) transport.PasswordFunc
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
	header  string
	machine *inventory.Machine
	session *inventory.Session
	copies  []inventory.Copy
}

type model struct {
	deps     Deps
	copied   bool // the resume command was copied on the done screen
	mode     mode
	width    int
	height   int
	machines []*inventory.Machine
	rows     []row
	cursor   int
	offset   int
	filter   string
	editing  bool
	opts     engine.Options
	plan     *engine.Plan
	sel      row
	result   *engine.Result
	err      error
	exit     *Exit
	started  time.Time
}

type scanDone struct{ machines []*inventory.Machine }
type planDone struct {
	plan *engine.Plan
	err  error
}
type applyDone struct {
	res *engine.Result
	err error
}

// Run starts the TUI and returns what to do afterwards (nil = nothing).
func Run(d Deps) (*Exit, error) {
	m := &model{deps: d, mode: modeLoading, started: time.Now(), opts: engine.Options{
		ReposDir: d.Config.ReposDir, GHQLayout: d.Config.Layout == "ghq", Worktree: engine.WorktreeAuto,
		Fork: d.Config.LivePolicy == "fork", RemoteCtl: d.Config.RemoteCtl,
		MarkSource: d.Config.MarkMovedOn(), SyncCode: d.Config.SyncCodeOn(), PushSource: d.Config.PushSource}}
	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return nil, err
	}
	fm := final.(*model)
	for _, mc := range fm.machines {
		mc.Close()
	}
	return fm.exit, nil
}

func (m *model) Init() tea.Cmd {
	d := m.deps
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		sc := &inventory.Scanner{StateDir: d.StateDir, Log: d.Log, Passwords: d.Passwords}
		return scanDone{sc.Scan(ctx, d.Config.Hosts, true)}
	}
}

func (m *model) buildRows() {
	m.rows = m.rows[:0]
	groups := inventory.GroupByRepo(m.machines, m.deps.Roots)
	f := strings.ToLower(m.filter)
	byName := map[string]*inventory.Machine{}
	for _, mc := range m.machines {
		byName[mc.Name] = mc
	}
	for _, g := range groups {
		var rows []row
		for _, e := range g.Entries {
			s := e.Session
			if f != "" && !strings.Contains(strings.ToLower(s.Title+" "+s.LastPrompt+" "+s.CWD+" "+g.Name+" "+e.Machine), f) {
				continue
			}
			rows = append(rows, row{machine: byName[e.Machine], session: s, copies: e.Copies})
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
		if m.rows[i].session != nil {
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
		m.machines = msg.machines
		m.mode = modeBrowse
		m.buildRows()
	case planDone:
		if msg.err != nil {
			m.err, m.mode = msg.err, modeError
		} else {
			m.plan, m.mode = msg.plan, modePlan
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
			if m.cursor < len(m.rows) && m.rows[m.cursor].session != nil {
				m.sel = m.rows[m.cursor]
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
		case "w":
			m.opts.Worktree = map[engine.WorktreeMode]engine.WorktreeMode{engine.WorktreeAuto: engine.WorktreeCreate,
				engine.WorktreeCreate: engine.WorktreeMain, engine.WorktreeMain: engine.WorktreeAuto}[m.opts.Worktree]
			return m, m.planCmd()
		case "r":
			m.opts.RemoteCtl = !m.opts.RemoteCtl
			return m, m.planCmd()
		case "n":
			m.opts.NotifyOld = !m.opts.NotifyOld
			return m, m.planCmd()
		case "f":
			m.opts.Fork = !m.opts.Fork
			return m, m.planCmd()
		case "x":
			m.opts.Redact = !m.opts.Redact
			return m, m.planCmd()
		case "m":
			m.opts.MarkSource = !m.opts.MarkSource
			return m, m.planCmd()
		case "s":
			m.opts.SyncCode = !m.opts.SyncCode
			return m, m.planCmd()
		case "p":
			m.opts.PushSource = !m.opts.PushSource
			return m, m.planCmd()
		case "k":
			m.opts.StopLocal = !m.opts.StopLocal
			return m, m.planCmd()
		case "R":
			m.opts.Conflict = map[bool]string{true: "", false: "replace"}[m.opts.Conflict == "replace"]
			return m, m.planCmd()
		case "B":
			m.opts.Conflict = map[bool]string{true: "", false: "keep-both"}[m.opts.Conflict == "keep-both"]
			return m, m.planCmd()
		}
	case modeDone:
		switch k {
		case "enter":
			argv := m.plan.Resume.Argv()
			m.exit = &Exit{RunDir: m.plan.TargetCWD, RunArgv: argv}
			return m, tea.Quit
		case "c":
			m.copied = true
			return m, tea.SetClipboard(m.plan.Resume.Shell(link.DefaultShell()))
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
	sel, opts := m.sel, m.opts
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		tgt := inventory.LocalTarget(ctx)
		s := sel.session
		p, err := engine.BuildPlan(ctx, sel.machine.PlanSource(ctx), tgt, engine.Input{Summary: &s.Summary, Git: s.Git, Live: s.Live}, opts)
		return planDone{p, err}
	}
}

func (m *model) applyCmd() tea.Cmd {
	p, src, d := m.plan, m.sel.machine.Source(), m.deps
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		res, err := engine.Apply(ctx, p, src, engine.Env{StateDir: d.StateDir, Log: d.Log})
		return applyDone{res, err}
	}
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
	title := bold.Render("hopsesh") + dim.Render("  sessions on your machines")
	b.WriteString(title + "\n")
	switch m.mode {
	case modeLoading:
		fmt.Fprintf(&b, "\n  Scanning this machine and %d allowed machine(s)… %s\n", countAllowed(m.deps.Config), dim.Render(time.Since(m.started).Truncate(time.Second).String()))
	case modeBrowse:
		m.viewBrowse(&b)
	case modePlan:
		m.viewPlan(&b)
	case modeApplying:
		fmt.Fprintf(&b, "\n  Moving %q… (copying, rewriting, verifying)\n", m.plan.Title)
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

func countAllowed(c config.Config) int {
	n := 0
	for _, h := range c.Hosts {
		if h.Allowed {
			n++
		}
	}
	return n
}

func (m *model) viewBrowse(b *strings.Builder) {
	var mach []string
	total := 0
	for _, mc := range m.machines {
		s := fmt.Sprintf("%s %d", mc.Name, len(mc.Sessions))
		switch mc.Status {
		case inventory.StatusOK, inventory.StatusLocal:
			s = okSt.Render("●") + " " + s
		default:
			s = errSt.Render("●") + " " + mc.Name + " " + mc.Status
		}
		mach = append(mach, s)
		total += len(mc.Sessions)
	}
	b.WriteString(dim.Render(fmt.Sprintf("%d sessions · ", total)) + strings.Join(mach, "   ") + "\n")
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
		s := r.session
		status := "ended"
		if s.Live != nil {
			status = liveSt.Render("live " + s.Live.Status)
		}
		line := fmt.Sprintf("  %-14s %-44s %-11s %s", truncate(r.machine.Name, 14), truncate(s.Title, 44), ago(s.LastActivity), status)
		if i == m.cursor {
			line = selSt.Render(padRight(stripANSIWidth(line, w-1), w-1))
		}
		b.WriteString(line + "\n")
	}
	for i := end - m.offset; i < h; i++ {
		b.WriteString("\n")
	}
	if m.cursor < len(m.rows) && m.rows[m.cursor].session != nil {
		r := m.rows[m.cursor]
		s := r.session
		b.WriteString(dim.Render(strings.Repeat("─", min(w, 120))) + "\n")
		fmt.Fprintf(b, "  %s  %s\n", bold.Render(truncate(s.Title, w-20)), dim.Render(s.ID[:8]))
		fmt.Fprintf(b, "  %s  %s\n", r.machine.Name, s.CWD)
		if m.deps.Describe != nil {
			if d := m.deps.Describe(s); d != "" {
				fmt.Fprintf(b, "  %s\n", d)
			}
		}
		fmt.Fprintf(b, "  %s\n", dim.Render(truncate("last prompt: “"+s.LastPrompt+"”", w-4)))
		if len(r.copies) > 1 {
			var parts []string
			for _, c := range r.copies {
				p := c.Machine
				switch {
				case c.Newest:
					p += " (newest)"
				case c.MovedTo != "":
					p += " (moved to " + c.MovedTo + ")"
				default:
					p += " (older)"
				}
				parts = append(parts, p)
			}
			label := "copies: " + strings.Join(parts, ", ")
			if r.machine != nil && !r.machine.Local {
				label += " · enter brings the newest here"
			}
			fmt.Fprintf(b, "  %s\n", warnSt.Render(truncate(label, w-4)))
		}
	}
	b.WriteString(dim.Render("\n  ↑↓ move · enter hop here · / search · r refresh · q quit\n"))
}

func (m *model) viewPlan(b *strings.Builder) {
	p := m.plan
	fmt.Fprintf(b, "\n  %s %q\n", bold.Render("Hop"), p.Title)
	fmt.Fprintf(b, "  from  %s  %s\n  to    this machine  %s\n", p.SourceHost, p.SourceCWD, p.TargetCWD)
	r := p.Repo
	switch r.Action {
	case "use":
		fmt.Fprintf(b, "  repo  %s at %s (on %s)\n", r.Identity, r.LocalPath, r.LocalBranch)
	case "clone":
		fmt.Fprintf(b, "  repo  %s → will clone into %s\n", r.Identity, r.LocalPath)
	case "needs-clone":
		fmt.Fprintf(b, "  repo  %s is not here — press c to clone into %s\n", r.Identity, r.LocalPath)
	case "none":
		b.WriteString("  dir   not a git repository\n")
	}
	if r.SourceBranch != "" {
		where := "main folder"
		if r.ClaudeWT {
			where = "Claude worktree"
		} else if r.InWorktree {
			where = "worktree"
		}
		fmt.Fprintf(b, "  branch %s (%s on %s)\n", r.SourceBranch, where, p.SourceHost)
	}
	if r.Worktree != "" {
		fmt.Fprintf(b, "  worktree → %s\n", r.Worktree)
	}
	fmt.Fprintf(b, "  files %d (%s) · %d path mapping(s)\n", len(p.Files), engine.Human(p.TotalBytes), len(p.Mappings))
	if p.StopPID > 0 {
		fmt.Fprintf(b, "  first quit the copy running here (pid %d)\n", p.StopPID)
	}
	if p.Push {
		fmt.Fprintf(b, "  push  %d unpushed commit(s) on %s first\n", p.Repo.Unpushed, p.SourceHost)
	}
	if p.Sync != "" {
		fmt.Fprintf(b, "  code  %s\n", p.Sync)
	}
	switch p.Mark {
	case engine.MarkNow:
		fmt.Fprintf(b, "  mark  the copy on %s becomes %q\n", p.SourceHost, moved.Title(p.StartContext.TargetHost, p.Title))
	case engine.MarkWhenStopped:
		fmt.Fprintf(b, "  mark  the copy on %s is running; marked moved once it stops\n", p.SourceHost)
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
	fmt.Fprintf(b, "\n  [c] clone %s  [w] worktree %s  [r] Remote Control %s  [n] notify old %s  [f] fork %s  [x] redact %s\n",
		on(m.opts.Clone), string(m.opts.Worktree), on(m.opts.RemoteCtl), on(m.opts.NotifyOld), on(m.opts.Fork), on(m.opts.Redact))
	fmt.Fprintf(b, "  [m] mark old copy %s  [s] sync code %s  [p] push on %s %s  [k] quit copy running here %s\n",
		on(m.opts.MarkSource), on(m.opts.SyncCode), p.SourceHost, on(m.opts.PushSource), on(m.opts.StopLocal))
	if p.Conflict != "" || m.opts.Conflict != "" {
		fmt.Fprintf(b, "  the copy here changed too: [R] replace it %s  [B] keep both %s\n", on(m.opts.Conflict == "replace"), on(m.opts.Conflict == "keep-both"))
	}
	if len(p.Blockers) == 0 {
		b.WriteString(dim.Render("\n  y/enter: hop · esc: back\n"))
	} else {
		b.WriteString(dim.Render("\n  resolve the ✗ first · esc: back\n"))
	}
}

func (m *model) viewDone(b *strings.Builder) {
	p, res := m.plan, m.result
	fmt.Fprintf(b, "\n  %s %q is on this machine.\n", okSt.Render("✓"), p.Title)
	n := 0
	for _, v := range res.Rewrite.Replacements {
		n += v
	}
	fmt.Fprintf(b, "  %d paths rewritten · %d file(s), %s", n, res.Copied, engine.Human(res.Bytes))
	if res.Secrets.Total > 0 {
		fmt.Fprintf(b, " · %d likely secret(s)", res.Secrets.Total)
	}
	b.WriteString("\n")
	if res.PushError != "" {
		b.WriteString("  " + warnSt.Render("! could not push on "+p.SourceHost+": "+res.PushError) + "\n")
	} else if res.Pushed != "" {
		fmt.Fprintf(b, "  pushed %s on %s\n", p.Repo.SourceBranch, p.SourceHost)
	}
	if res.SyncNote != "" {
		fmt.Fprintf(b, "  code: %s\n", res.SyncNote)
	}
	switch res.Mark {
	case "done":
		fmt.Fprintf(b, "  the copy on %s is now marked moved\n", p.SourceHost)
	case "pending":
		fmt.Fprintf(b, "  the copy on %s will be marked moved once it stops running\n", p.SourceHost)
	case "failed":
		b.WriteString("  " + warnSt.Render("! could not mark the copy on "+p.SourceHost+": "+res.MarkError) + "\n")
	}
	b.WriteString("\n  Start it:\n\n")
	family := link.DefaultShell()
	for _, line := range wrapCommand(p.Resume.Shell(family), max(m.width, 80)-4, family) {
		b.WriteString("  " + line + "\n")
	}
	if m.copied {
		b.WriteString(okSt.Render("\n  Copied to the clipboard.") + "\n")
	}
	b.WriteString(dim.Render("\n  enter: start claude there now · c: copy the command · q: quit (undo with: hopsesh undo " + p.SessionID[:8] + ")\n"))
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

func stripANSIWidth(s string, n int) string { return truncate(s, n) }
