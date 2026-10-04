package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Handing a session off to a cloud in the terminal UI: c on a session opens "Hand off to
// ▸" (a cloud hopsesh cannot use says why), the plan pane shows what goes and what stays
// (b edits the briefing in $EDITOR, u toggles the untracked files, L lists everything that
// stays), and the done view gives the session's link (an OSC 8 link) and its id.

// handoff is the hand-off being chosen or planned.
type handoff struct {
	picking  bool
	targets  []app.HandoffTarget
	at       int
	cloud    string // the cloud being planned ("" : not a hand-off)
	opts     move.Options
	showLoss bool
	notice   string
}

type briefEdited struct {
	text string
	err  error
}

// openHandoff opens the Hand off to ▸ picker for the selected row.
func (m *model) openHandoff() {
	if m.cursor >= len(m.rows) || m.rows[m.cursor].item == nil {
		return
	}
	e := m.rows[m.cursor].item.Entry
	if e.Location.IsCloud() {
		return
	}
	m.sel, m.picked = m.rows[m.cursor], nil
	m.ho = handoff{picking: true, targets: m.deps.App.HandoffTargets(m.inv, e)}
	for i, t := range m.ho.targets {
		if t.OK {
			m.ho.at = i
			break
		}
	}
}

// pickKeys move through the picker; enter plans the hand-off to an enabled cloud.
func (m *model) pickKeys(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "esc", "q", "c":
		m.ho.picking = false
	case "up", "k":
		m.ho.at = max(0, m.ho.at-1)
	case "down", "j":
		m.ho.at = min(len(m.ho.targets)-1, m.ho.at+1)
	case "enter":
		if m.ho.at < len(m.ho.targets) && m.ho.targets[m.ho.at].OK {
			t := m.ho.targets[m.ho.at]
			m.ho.picking, m.ho.cloud = false, t.Cloud
			m.ho.opts = m.deps.App.HandoffDefaults(t.Cloud)
			m.ho.opts.Bundle = m.ho.opts.Bundle || t.Bundle
			return m, m.planCmd()
		}
	}
	return m, nil
}

// handoffPlanCmd plans the hand-off with the current choices.
func (m *model) handoffPlanCmd() tea.Cmd {
	m.planning = true
	a, inv, e, cloud, opts := m.deps.App, m.inv, m.sel.item.Entry, m.ho.cloud, m.ho.opts
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		p, err := a.PlanHandoff(ctx, inv, e, cloud, opts)
		return planDone{plan: p, err: err}
	}
}

// handoffKeys are the plan pane's keys for a hand-off.
func (m *model) handoffKeys(k string) (tea.Model, tea.Cmd) {
	hp := m.plan.Handoff
	switch k {
	case "esc", "q":
		m.mode, m.ho.cloud, m.ho.showLoss = modeBrowse, "", false
		return m, nil
	case "y", "enter":
		if !m.planning && len(m.plan.Blockers) == 0 {
			m.mode = modeApplying
			return m, m.applyCmd()
		}
		return m, nil
	case "L":
		m.ho.showLoss = !m.ho.showLoss
		return m, nil
	case "b":
		return m, editBrief(hp.Brief)
	case "u":
		if len(hp.Untracked) > 0 {
			m.ho.opts.Untracked = nil
		} else {
			m.ho.opts.Untracked = nil
			for _, c := range hp.Offered {
				m.ho.opts.Untracked = append(m.ho.opts.Untracked, c.Path)
			}
		}
	case "h":
		m.ho.opts.HistoryFile = !m.ho.opts.HistoryFile
	case "m":
		m.ho.opts.Mark = !m.ho.opts.Mark
	case "U":
		if hp.CanBundle {
			m.ho.opts.Bundle = !m.ho.opts.Bundle
		}
	case "e":
		// The next environment hopsesh knows of (the plan lists them).
		if !hp.EnvNeeded || len(hp.Envs) == 0 {
			return m, nil
		}
		next := 0
		for i, e := range hp.Envs {
			if e.Value == hp.Env {
				next = (i + 1) % len(hp.Envs)
			}
		}
		m.ho.opts.Env = hp.Envs[next].Value
	case "S":
		if hp.CanStartingDiff {
			m.ho.opts.StartingDiff = !m.ho.opts.StartingDiff
		}
	default:
		return m, nil
	}
	return m, m.planCmd()
}

// editBrief opens the briefing in the user's editor ($VISUAL, $EDITOR, else vi).
func editBrief(text string) tea.Cmd {
	f, err := os.CreateTemp("", "hopsesh-brief-*.md")
	if err != nil {
		return func() tea.Msg { return briefEdited{err: err} }
	}
	_, err = f.WriteString(text)
	f.Close()
	if err != nil {
		return func() tea.Msg { return briefEdited{err: err} }
	}
	editor := strings.Fields(nonEmpty(os.Getenv("VISUAL"), nonEmpty(os.Getenv("EDITOR"), "vi")))
	cmd := exec.Command(editor[0], append(editor[1:], f.Name())...) //nolint:gosec // the user's own editor, on a file hopsesh made
	name := f.Name()
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.Remove(name)
		if err != nil {
			return briefEdited{err: err}
		}
		b, err := os.ReadFile(filepath.Clean(name))
		return briefEdited{text: string(b), err: err}
	})
}

// briefDone takes the edited briefing (the plan checks it for secrets again).
func (m *model) briefDone(msg briefEdited) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.ho.notice = "The editor failed: " + msg.err.Error()
		return m, nil
	}
	if strings.TrimSpace(msg.text) == strings.TrimSpace(m.plan.Handoff.Brief) {
		return m, nil
	}
	m.ho.opts.Brief = msg.text
	return m, m.planCmd()
}

// viewPicker is the Hand off to ▸ picker, under the list.
func (m *model) viewPicker(b *strings.Builder) {
	b.WriteString("\n  " + bold.Render("Hand off to ▸") + dim.Render("  a cloud gets a briefing, not this conversation") + "\n")
	for i, t := range m.ho.targets {
		cur := "  "
		if i == m.ho.at {
			cur = "▸ "
		}
		if t.OK {
			note := t.Note
			if len(t.Limits) > 0 {
				note += dim.Render(" · " + t.Limits[0])
			}
			fmt.Fprintf(b, "  %s%-14s %s\n", cur, cloudSt.Render(t.Cloud), note)
		} else {
			fmt.Fprintf(b, "  %s%-14s %s\n", cur, dim.Render(t.Cloud), errSt.Render("✕ "+t.Why))
		}
	}
	b.WriteString(dim.Render("  enter: plan the hand-off · esc: back") + "\n")
}

// viewHandoffPlan is the plan pane of a hand-off.
func (m *model) viewHandoffPlan(b *strings.Builder) {
	p := m.plan
	hp := p.Handoff
	fmt.Fprintf(b, "\n  %s %s\n", bold.Render("Hand off to "+hp.CloudTitle), dim.Render(fmt.Sprintf("%q · %s · %s · %s", p.Title, p.Agent, p.Source.Location, hp.Repo)))
	if m.planning {
		b.WriteString("  updating the plan…\n")
	}
	fmt.Fprintf(b, "  %s\n\n", hp.Conversation)
	masked := ""
	if hp.Masked > 0 {
		masked = fmt.Sprintf(" · %d secret(s) masked", hp.Masked)
	}
	edited := ""
	if hp.Edited {
		edited = " · your text"
	}
	fmt.Fprintf(b, "  Briefing  %d tokens%s%s  %s\n", hp.Tokens, masked, edited, dim.Render("[b] view/edit"))
	switch {
	case hp.Code == agent.ViaStartingDiff:
		fmt.Fprintf(b, "  Code      %d changed file(s) as a starting diff, on branch %s (nothing is pushed)\n", len(hp.Tracked)+len(hp.Untracked), hp.Branch)
	case hp.Code == agent.ViaBundle:
		fmt.Fprintf(b, "  Code      an upload by %s (branch %s here; nothing is pushed)\n", hp.Agent, hp.Branch)
	case hp.Reuse:
		fmt.Fprintf(b, "  Code      branch %s, already on %s\n", hp.Branch, hp.Host)
	case hp.Branch != "":
		fmt.Fprintf(b, "  Code      branch %s\n", hp.Branch)
		parts := []string{"← " + short(hp.Base)}
		if hp.Unpushed > 0 {
			parts = append(parts, fmt.Sprintf("+%d unpushed commit(s)", hp.Unpushed))
		}
		if n := len(hp.Tracked) + len(hp.Untracked); n > 0 {
			parts = append(parts, fmt.Sprintf("+%d changed file(s)", n))
		}
		fmt.Fprintf(b, "            %s\n", strings.Join(parts, " · "))
	}
	for _, u := range hp.Untracked {
		fmt.Fprintf(b, "            ☑ %s (untracked)  %s\n", u, dim.Render("[u] toggle"))
	}
	for _, c := range hp.Offered {
		fmt.Fprintf(b, "            ☐ %s (untracked)  %s\n", c.Path, dim.Render("[u] toggle"))
	}
	if len(hp.Withheld) > 0 {
		var names []string
		for _, w := range hp.Withheld {
			names = append(names, w.Path)
		}
		fmt.Fprintf(b, "  %s  %s\n", errSt.Render("Stays on this machine"), strings.Join(names, " · "))
	}
	box := func(on bool) string {
		if on {
			return okSt.Render("☑")
		}
		return "☐"
	}
	fmt.Fprintf(b, "  %s Also commit the conversation as %s  %s\n", box(hp.HistoryFile), hp.HistoryPath, dim.Render("[h]"))
	if hp.HistoryFile {
		b.WriteString("    " + warnSt.Render(hp.HistoryWarning) + "\n")
	}
	fmt.Fprintf(b, "  %s Mark this session %q  %s\n", box(p.Mark != move.MarkOff), strings.TrimPrefix(hp.MarkTitle, "↪ "), dim.Render("[m]"))
	if hp.CanBundle {
		fmt.Fprintf(b, "  %s Upload the repository instead of pushing a branch  %s\n", box(hp.Code == agent.ViaBundle), dim.Render("[U]"))
	}
	if hp.CanStartingDiff {
		fmt.Fprintf(b, "  %s Send the changes with the %s as a starting diff instead of a branch  %s\n", box(hp.Code == agent.ViaStartingDiff), hp.Noun, dim.Render("[S]"))
	}
	if hp.EnvNeeded {
		switch {
		case hp.Env != "" && len(hp.Envs) > 1:
			fmt.Fprintf(b, "  Environment  %s  %s\n", hp.EnvName, dim.Render("[e] next"))
		case hp.Env != "":
			fmt.Fprintf(b, "  Environment  %s\n", hp.EnvName)
		case len(hp.Envs) > 0:
			fmt.Fprintf(b, "  Environment  %s  %s\n", warnSt.Render("none picked"), dim.Render("[e] pick: "+envLabels(hp.Envs)))
		default:
			fmt.Fprintf(b, "  Environment  %s\n", warnSt.Render("none known"))
		}
	}
	var oks []string
	for _, c := range hp.Checks {
		switch c.State {
		case "ok":
			oks = append(oks, c.Text)
		case "warn":
			b.WriteString("  " + warnSt.Render("! "+c.Text) + "\n")
		}
	}
	if len(oks) > 0 {
		b.WriteString("  " + okSt.Render("✓") + " " + strings.Join(oks, " · ") + "\n")
	}
	for _, bl := range p.Blockers {
		b.WriteString("  " + errSt.Render("✗ "+bl) + "\n")
	}
	b.WriteString("  " + dim.Render(hp.Usage) + "\n")
	for _, l := range hp.Limits {
		b.WriteString("  " + dim.Render("· "+l) + "\n")
	}
	if hp.Tally.Messages > 0 || hp.Tally.ToolCalls > 0 {
		fmt.Fprintf(b, "  Loss      %d messages, %d tool calls, %d reasoning blocks  %s\n", hp.Tally.Messages, hp.Tally.ToolCalls, hp.Tally.Reasoning, dim.Render("[L] list"))
	}
	if m.ho.showLoss {
		for _, l := range hp.Loss {
			b.WriteString("    · " + l + "\n")
		}
	}
	if m.ho.notice != "" {
		b.WriteString("  " + warnSt.Render(m.ho.notice) + "\n")
	}
	keys := "enter hand off · b briefing in $EDITOR · u untracked files · L loss list · esc back"
	if len(p.Blockers) > 0 {
		keys = "resolve the ✗ first · b briefing in $EDITOR · L loss list · esc back"
	}
	if hp.EnvNeeded && len(hp.Envs) > 0 {
		keys = strings.Replace(keys, " · L loss list", " · e environment · L loss list", 1)
	}
	b.WriteString(dim.Render("\n  "+keys) + "\n")
}

// viewHandoffDone is the done view of a hand-off: the session's link and id, or the step
// that failed.
func (m *model) viewHandoffDone(b *strings.Builder) {
	r := m.result.Handoff
	if r.Failed != "" {
		fmt.Fprintf(b, "\n  %s %s\n", errSt.Render("✕"), bold.Render("The hand-off stopped at “"+stepLabel(r, r.Failed)+"”"))
		fmt.Fprintf(b, "   %s\n\n", errSt.Render(r.Message))
		for _, st := range r.Steps {
			mark := map[string]string{move.StepDone: okSt.Render("✓"), move.StepFailed: errSt.Render("✕"), move.StepTodo: dim.Render("○")}[st.State]
			line := st.Label
			if st.State == move.StepTodo {
				line = dim.Render(line + " · not done")
			}
			if st.Detail != "" {
				line += dim.Render(" · " + st.Detail)
			}
			fmt.Fprintf(b, "   %s %s\n", mark, line)
		}
		keys := []string{"u undo"}
		if r.Retry == "bundle" {
			keys = append(keys, "U retry as an upload")
		}
		keys = append(keys, "enter back to the list", "q quit")
		b.WriteString(dim.Render("\n   "+strings.Join(keys, " · ")) + "\n")
		return
	}
	fmt.Fprintf(b, "\n  %s %s\n", okSt.Render("✓"), bold.Render("Handed off to "+r.CloudTitle))
	fmt.Fprintf(b, "   %s %s is %s · %q\n", upperFirst(nonEmpty(r.Noun, "session")), shortID(r.Session), nonEmpty(r.State, "running"), m.plan.Title)
	fmt.Fprintf(b, "   %s\n", link(r.URL, r.URL))
	fmt.Fprintf(b, "   id      %s\n", r.Session)
	if r.Branch != "" && (r.Code == string(agent.ViaBranch) || r.Code == string(agent.ViaStartingDiff)) {
		branch := r.Branch
		if r.BranchURL != "" {
			branch = link(r.BranchURL, r.Branch)
		}
		fmt.Fprintf(b, "   branch  %s on %s\n", branch, r.Repo)
		if r.Code == string(agent.ViaStartingDiff) {
			b.WriteString("           the changes went with it as a starting diff\n")
		}
	}
	if r.EnvName != "" {
		fmt.Fprintf(b, "   env     %s\n", r.EnvName)
	}
	if len(r.Stayed) > 0 {
		fmt.Fprintf(b, "   stayed on this machine  %s\n", strings.Join(r.Stayed, " · "))
	}
	switch m.result.Mark {
	case "done":
		fmt.Fprintf(b, "   ↪ this session is now marked “%s”\n", strings.TrimPrefix(r.MarkText, "↪ "))
	case "pending":
		b.WriteString("   ↪ this session is marked once it ends\n")
	case "failed":
		b.WriteString("   " + warnSt.Render("! could not mark this session: "+m.result.MarkError) + "\n")
	}
	for _, w := range m.result.Warnings {
		b.WriteString("   " + warnSt.Render("! "+w) + "\n")
	}
	fmt.Fprintf(b, "\n   When it finishes: select the %s row and press enter to bring it here.\n", r.Cloud)
	if m.ho.notice != "" {
		b.WriteString("\n   " + okSt.Render(m.ho.notice) + "\n")
	}
	undo := "deletes the branch and the mark"
	if !r.Pushed {
		undo = "deletes the mark"
	}
	b.WriteString(dim.Render("\n   o open in browser · y copy link · u undo ("+undo+"; "+strings.TrimSuffix(lowerFirst(r.Manual), ".")+") · enter back to the list · q quit") + "\n")
}

// handoffDoneKeys are the done view's keys.
func (m *model) handoffDoneKeys(k string) (tea.Model, tea.Cmd) {
	r := m.result.Handoff
	switch k {
	case "q":
		return m, tea.Quit
	case "enter", "esc":
		m.ho = handoff{}
		m.mode = modeLoading
		return m, m.Init()
	case "u":
		return m, m.undoCmd(m.result.Journal)
	case "o":
		if r.URL != "" {
			_ = openBrowser(r.URL)
		}
	case "y":
		if r.URL != "" {
			m.ho.notice = "Copied the link."
			return m, tea.SetClipboard(r.URL)
		}
	case "U":
		if r.Retry == "bundle" {
			m.ho.opts.Bundle = true
			return m, m.planCmd()
		}
	}
	return m, nil
}

func stepLabel(r *move.HandoffResult, name string) string {
	for _, st := range r.Steps {
		if st.Name == name {
			return st.Label
		}
	}
	return name
}

func short(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

func shortID(s string) string {
	if len(s) > 18 {
		return s[:18] + "…"
	}
	return s
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// envLabels are the environments to pick from, in short.
func envLabels(envs []move.EnvChoice) string {
	var out []string
	for _, e := range envs {
		out = append(out, e.Name)
	}
	return strings.Join(out, ", ")
}
