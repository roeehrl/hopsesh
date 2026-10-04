package tui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/launch"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/proc"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Clouds in the terminal UI: the header lists them after the machines, cloud sessions are
// rows like any other, enter on one plans bringing it here, and the done view hands the
// terminal to the agent's own command (claude --teleport) before showing what came back.

var cloudSt = lipgloss.NewStyle().Foreground(lipgloss.Color("#6aa3d8"))

// cloudHeader is a cloud in the header line: a dot (half for a partial listing), its
// name, and its count or why there is none.
func cloudHeader(c *app.Cloud) string {
	switch c.Status {
	case app.CloudReady, app.CloudCLIOld:
		dot := okSt.Render("●")
		if c.Partial {
			dot = okSt.Render("◐")
		}
		return dot + fmt.Sprintf(" %s %d", c.Name, c.Sessions)
	case app.CloudNotAllowed:
		return dim.Render("○ " + c.Name + " off")
	case app.CloudSignedOut:
		return warnSt.Render("○") + " " + c.Name + " " + warnSt.Render("sign in")
	case app.CloudCLIMissing:
		return dim.Render("○ " + c.Name + " not installed")
	case app.CloudNotEligible:
		return dim.Render("○ " + c.Name + " not available")
	}
	return errSt.Render("●") + " " + c.Name + " " + c.Status
}

// shownClouds are the clouds hopsesh can do something with.
func (m *model) shownClouds() []*app.Cloud {
	var out []*app.Cloud
	for _, c := range m.inv.Clouds {
		if c.Listable || c.Fetchable {
			out = append(out, c)
		}
	}
	return out
}

// partialCloud is a ready cloud that can list only part of its sessions (the browse view
// says so, with the keys for the rest).
func (m *model) partialCloud() *app.Cloud {
	for _, c := range m.shownClouds() {
		if c.Partial && c.Fetchable && (c.Status == app.CloudReady || c.Status == app.CloudCLIOld) {
			return c
		}
	}
	return nil
}

// mirrorWords names where a session is mirrored: "mirrored on claude.ai".
func mirrorWords(mr *agent.CloudLink) string {
	where := mr.Cloud
	if u, err := url.Parse(mr.URL); err == nil && u.Host != "" {
		where = u.Host
	}
	return "mirrored on " + where
}

// agentName is a module's name.
func (m *model) agentName(id agent.ID) string {
	if mod, ok := m.deps.App.Reg.Get(id); ok {
		return mod.Spec().Name
	}
	return string(id)
}

// link is text that opens url in terminals that know OSC 8 links.
func link(url, text string) string { return lipgloss.NewStyle().Hyperlink(url).Render(text) }

// checkout is the repository checkout a pasted link or the vendor's picker brings a
// session into: the folder hopsesh runs in when it is one, else the selected row's.
func (m *model) checkout() string {
	if wd, err := os.Getwd(); err == nil {
		if states, err := repos.ProbeLocal(context.Background(), []string{wd}, nil); err == nil && len(states) == 1 && states[0].IsRepo {
			return wd
		}
	}
	if m.cursor < len(m.rows) && m.rows[m.cursor].item != nil {
		e := m.rows[m.cursor].item.Entry
		if e.Checkout != "" {
			return e.Checkout
		}
		if g := e.Git; g != nil && e.Machine == m.inv.Local().Name {
			return nonEmpty(g.MainWorktree, g.Toplevel)
		}
		if g := e.Git; g != nil && g.Identity != "" {
			if found := repos.FindLocal(g.Identity, m.deps.App.LocalRoots()); len(found) > 0 {
				return found[0].Path
			}
		}
	}
	return ""
}

// fetchCmd plans bringing a cloud session here by its link or id ("" : the vendor's
// picker chooses), into the checkout here.
func (m *model) fetchCmd(cloud string, id agent.SessionID) tea.Cmd {
	m.planning = true
	a, inv, opts, target := m.deps.App, m.inv, m.opts, m.target
	opts.TargetDir = m.checkout()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		e, err := inv.CloudEntry(a, cloud, id)
		if err != nil {
			return planDone{err: err}
		}
		p, in, err := a.Plan(ctx, inv, e, target, opts)
		return pickedDone{entry: e, plan: planDone{p, in, err}}
	}
}

type pickedDone struct {
	entry app.Entry
	plan  planDone
}

// pasteKey edits the pasted link; enter plans bringing that session here.
func (m *model) pasteKey(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "esc":
		m.pasting, m.paste = false, ""
	case "enter":
		m.pasting = false
		_, cl, id, ok := m.deps.App.ParseCloudLink(m.paste)
		if !ok {
			m.err, m.mode = fmt.Errorf("%q is not a cloud session's link or id", strings.TrimSpace(m.paste)), modeError
			return m, nil
		}
		m.target = ""
		return m, m.fetchCmd(cl, id)
	case "backspace":
		if m.paste != "" {
			r := []rune(m.paste)
			m.paste = string(r[:len(r)-1])
		}
	default:
		if len([]rune(k)) == 1 {
			m.paste += k
		} else if k == "space" {
			m.paste += " "
		}
	}
	return m, nil
}

// fetchKeys are the plan pane's keys for bringing a session from a cloud.
func (m *model) fetchKeys(k string) (tea.Model, tea.Cmd, bool) {
	switch k {
	case "o":
		m.opts.CodeOnly = !m.opts.CodeOnly
	case "p":
		if m.plan.Fetch.CanAppend || m.opts.AppendOriginal {
			m.opts.AppendOriginal = !m.opts.AppendOriginal
		}
	case "B":
		m.opts.Conflict = map[bool]string{true: "", false: move.ConflictKeepBoth}[m.opts.Conflict == move.ConflictKeepBoth]
	case "a":
		m.target = m.nextAgent()
	default:
		return m, nil, false
	}
	return m, m.replan(), true
}

// replan plans the selected row again with the current choices (a pasted session keeps
// its own entry).
func (m *model) replan() tea.Cmd {
	if m.picked != nil {
		m.planning = true
		a, inv, e, target, opts := m.deps.App, m.inv, *m.picked, m.target, m.opts
		opts.TargetDir = nonEmpty(opts.TargetDir, e.Checkout)
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			p, in, err := a.Plan(ctx, inv, e, target, opts)
			return planDone{p, in, err}
		}
	}
	return m.planCmd()
}

func (m *model) viewFetchPlan(b *strings.Builder) {
	p := m.plan
	fp := p.Fetch
	verb := "Bring"
	if fp.CodeOnly {
		verb = "Bring the code of"
	}
	fmt.Fprintf(b, "\n  %s %q here from %s\n", bold.Render(verb), p.Title, fp.CloudTitle)
	if m.planning {
		b.WriteString("  updating the plan…\n")
	}
	if fp.Session != "" {
		fmt.Fprintf(b, "  from  %s  %s\n", cloudSt.Render(fp.Cloud), link(fp.URL, string(fp.Session)))
	} else {
		fmt.Fprintf(b, "  from  %s  chosen in %s's own picker\n", cloudSt.Render(fp.Cloud), m.agentName(p.Key.Agent))
	}
	fmt.Fprintf(b, "  to    this machine  %s, in a new worktree\n", nonEmpty(fp.Worktree, "?"))
	fmt.Fprintf(b, "  conversation  %s\n", fp.Conversation)
	if fp.Checkout != "" {
		fmt.Fprintf(b, "  repo  %s at %s\n", fp.Repo, fp.Checkout)
	}
	switch {
	case fp.Diff && fp.BranchState == move.BranchPushed:
		fmt.Fprintf(b, "  base  %s (the branch the %s started from) → %s\n", fp.CloudBranch, fp.Noun, fp.Ref)
	case fp.BranchState == move.BranchPushed:
		fmt.Fprintf(b, "  branch  %s → %s\n", fp.CloudBranch, fp.Ref)
	case fp.BranchState == move.BranchMissing:
		fmt.Fprintf(b, "  branch  %s is not on origin\n", fp.CloudBranch)
	}
	switch {
	case fp.Diff:
		changes := ""
		if fp.Changes != "" {
			changes = " (" + fp.Changes + ")"
		}
		fmt.Fprintf(b, "  code  the %s's patch%s, committed on %s\n", fp.Noun, changes, fp.LocalBranch)
	case fp.FastForward:
		fmt.Fprintf(b, "  local branch  %s, here already: it moves forward to the cloud's work\n", fp.LocalBranch)
	case fp.LocalBranch != "" && fp.LocalBranch != fp.CloudBranch:
		fmt.Fprintf(b, "  local branch  %s (renamed from %s)\n", fp.LocalBranch, fp.CloudBranch)
	case fp.Rename && !fp.CodeOnly:
		fmt.Fprintf(b, "  local branch  a claude/… branch is renamed under hopsesh/from/%s/\n", fp.Cloud)
	}
	if fp.Command != "" {
		fmt.Fprintf(b, "  runs in this terminal  %s\n", fp.Command)
	}
	if fp.Write && !fp.CodeOnly {
		fmt.Fprintf(b, "  writes  a new %s session (%d messages) in the worktree\n", fp.Writer, fp.Messages)
	}
	for _, c := range fp.Checks {
		switch c.State {
		case "ok":
			b.WriteString("  " + okSt.Render("✓") + " " + c.Text + "\n")
		case "warn":
			b.WriteString("  " + warnSt.Render("! "+c.Text) + "\n")
		default:
			b.WriteString("  " + errSt.Render("✗ "+c.Text) + "\n")
		}
	}
	on := func(v bool) string {
		if v {
			return okSt.Render("on ")
		}
		return dim.Render("off")
	}
	fmt.Fprintf(b, "\n  [a] continue in %s  [o] the code only %s", p.Agent, on(m.opts.CodeOnly))
	if fp.CanAppend {
		fmt.Fprintf(b, "  [p] add to %q instead %s", fp.Original.Title, on(m.opts.AppendOriginal))
	}
	if p.Conflict != "" {
		fmt.Fprintf(b, "  [B] keep both %s", on(m.opts.Conflict == move.ConflictKeepBoth))
	}
	b.WriteString("\n")
	if len(p.Blockers) == 0 {
		b.WriteString(dim.Render("\n  y/enter: go · esc: back\n"))
	} else {
		b.WriteString(dim.Render("\n  resolve the ✗ first · esc: back\n"))
	}
}

// viewFetchDone is the done view of a fetch: the worktree is ready and the agent's own
// command waits for this terminal.
func (m *model) viewFetchDone(b *strings.Builder) {
	p, res := m.plan, m.result
	if res.Fetch.Outcome == move.FetchCode {
		fmt.Fprintf(b, "\n  %s The code of %q is in %s on %s.\n", okSt.Render("✓"), p.Title, res.Worktree, res.Fetch.Branch)
		b.WriteString(dim.Render("\n  q: quit (undo with: hopsesh undo " + res.Journal + ")\n"))
		return
	}
	fmt.Fprintf(b, "\n  %s The worktree is ready: %s\n", okSt.Render("✓"), res.Worktree)
	fmt.Fprintf(b, "  %s copies %q here in this terminal:\n\n", p.Fetch.Run.Argv[0], p.Title)
	for _, line := range wrapCommand(res.Command, max(m.width, 80)-4, launch.DefaultShell()) {
		b.WriteString("  " + line + "\n")
	}
	b.WriteString("\n  hopsesh then checks its message count and renames the cloud's branch.\n")
	if m.copied {
		b.WriteString(okSt.Render("\n  Copied to the clipboard.") + "\n")
	}
	b.WriteString(dim.Render("\n  enter: run it now · c: copy the command · u: undo · q: quit (hopsesh picks it up on its next look)\n"))
}

// viewBrought is what came back from a cloud.
func (m *model) viewBrought(b *strings.Builder) {
	r := m.brought
	if r == nil {
		return
	}
	switch r.Outcome {
	case move.FetchWaiting:
		fmt.Fprintf(b, "\n  %s\n  %s\n", r.Message, dim.Render("It runs in this terminal: "+r.Command))
	case move.FetchComplete:
		fmt.Fprintf(b, "\n  %s %s\n", okSt.Render("✓"), bold.Render("Brought “"+r.Title+"” from "+r.CloudTitle))
		fmt.Fprintf(b, "   %s\n", r.Message)
	case move.FetchPartial:
		fmt.Fprintf(b, "\n  %s %s\n", warnSt.Render("!"), bold.Render("Brought “"+r.Title+"” from "+r.CloudTitle+": partial"))
		fmt.Fprintf(b, "   %s\n", warnSt.Render(linkIssue(r)))
	case move.FetchEmpty:
		fmt.Fprintf(b, "\n  %s %s\n", errSt.Render("✕"), bold.Render("Nothing came back from “"+r.Title+"”"))
		fmt.Fprintf(b, "   %s\n", errSt.Render(linkIssue(r)))
	}
	if r.Outcome != move.FetchWaiting {
		switch {
		case r.NoBranch:
			fmt.Fprintf(b, "   The cloud session never pushed its work, so there is no code to bring. The worktree: %s\n", r.Worktree)
		case r.Written && r.Branch != "":
			fmt.Fprintf(b, "   The code is here: %s (branch %s). The cloud %s is untouched.\n", r.Worktree, r.Branch, nonEmpty(r.Noun, "session"))
		case r.Renamed != "":
			fmt.Fprintf(b, "   The code is here in full: %s (branch %s, renamed from %s). The cloud session is untouched.\n", r.Worktree, r.Branch, r.Renamed)
		case r.Branch != "":
			fmt.Fprintf(b, "   The code is here in full: %s (branch %s). The cloud session is untouched.\n", r.Worktree, r.Branch)
		}
	}
	if r.Outcome == move.FetchEmpty && r.MirrorOf != "" {
		machine, _, _ := strings.Cut(r.MirrorOf, ":")
		fmt.Fprintf(b, "   The session itself runs on %s. Hop it here machine to machine instead: the whole conversation comes along.\n", machine)
	}
	if r.URL != "" {
		fmt.Fprintf(b, "   %s\n", link(r.URL, r.URL))
	}
	for _, w := range r.Warnings {
		b.WriteString("   " + warnSt.Render("! "+w) + "\n")
	}
	for _, l := range r.Loss {
		b.WriteString("   " + dim.Render("· "+l) + "\n")
	}
	if r.Outcome == move.FetchComplete || r.Outcome == move.FetchPartial {
		fmt.Fprintf(b, "\n   %s\n", r.Command)
	}
	if m.notice != "" {
		b.WriteString("\n   " + okSt.Render(m.notice) + "\n")
	}
	var keys []string
	switch r.Outcome {
	case move.FetchComplete, move.FetchPartial:
		keys = append(keys, "r resume it now")
		if r.ContinueName != "" && !r.Written {
			keys = append(keys, "i continue in "+r.ContinueName)
		}
	}
	if r.Outcome == move.FetchPartial && !r.Kept {
		keys = append(keys, "k keep the partial copy")
	}
	if r.Outcome != move.FetchWaiting {
		keys = append(keys, "u undo")
	}
	if r.URL != "" {
		keys = append(keys, "o open the session in the browser", "y copy link")
	}
	keys = append(keys, "enter back to the list", "q quit")
	b.WriteString(dim.Render("\n   "+strings.Join(keys, " · ")) + "\n")
}

// linkIssue is the outcome's message with the known problem as a link.
func linkIssue(r *app.Brought) string {
	if r.Issue == "" || r.IssueRef == "" {
		return r.Message
	}
	return strings.Replace(r.Message, r.IssueRef, link(r.Issue, r.IssueRef), 1)
}

// broughtKeys are the keys of the view of what came back.
func (m *model) broughtKeys(k string) (tea.Model, tea.Cmd) {
	r := m.brought
	switch k {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "enter", "esc":
		m.brought, m.notice, m.mode = nil, "", modeLoading
		return m, m.Init()
	case "r":
		if r.Outcome == move.FetchComplete || r.Outcome == move.FetchPartial {
			m.exit = &Exit{RunDir: r.Run.Dir, RunArgv: r.Run.Argv}
			return m, tea.Quit
		}
	case "i":
		if r.ContinueName != "" && !r.Written && (r.Outcome == move.FetchComplete || r.Outcome == move.FetchPartial) {
			key, err := agent.ParseKey(r.Key)
			if err != nil {
				return m, nil
			}
			for _, e := range m.inv.Entries {
				if e.Session.Key == key && e.Machine == m.inv.Local().Name {
					it := app.Item{Entry: e}
					m.sel, m.target, m.picked, m.brought = row{item: &it}, agent.ID(r.Continue), nil, nil
					return m, m.planCmd()
				}
			}
			m.notice = "The copy is not listed yet; refresh (enter) and continue it from the list."
		}
	case "k":
		if r.Outcome == move.FetchPartial {
			if err := m.deps.App.KeepPartial(r.Journal); err == nil {
				r.Kept, m.notice = true, "The partial copy stays as it is."
			}
		}
	case "u":
		if r.Outcome != move.FetchWaiting {
			return m, m.undoCmd(r.Journal)
		}
	case "o":
		if r.URL != "" {
			_ = openBrowser(r.URL)
		}
	case "y":
		if r.URL != "" {
			m.notice = "Copied the link."
			return m, tea.SetClipboard(r.URL)
		}
	}
	return m, nil
}

type undoDone struct {
	title string
	err   error
}

func (m *model) undoCmd(journalID string) tea.Cmd {
	a := m.deps.App
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		j, err := a.Undo(ctx, journalID, false)
		if errors.Is(err, journal.ErrChanged) {
			err = fmt.Errorf("%w\nUndoing now would lose that later work; hopsesh undo --force %s does it anyway", err, journalID)
		}
		title := journalID
		if j != nil {
			title = j.Title
		}
		return undoDone{title, err}
	}
}

// openBrowser opens a cloud session's page (a link hopsesh made, never one from a
// transcript).
func openBrowser(u string) error {
	if !strings.HasPrefix(u, "https://") {
		return errors.New("only https links open")
	}
	switch runtime.GOOS {
	case "darwin":
		return proc.Command("open", u).Start()
	case "windows":
		return proc.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	}
	return proc.Command("xdg-open", u).Start()
}

func nonEmpty(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
