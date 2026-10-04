package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/roeehrl/hopsesh/internal/core/move"
)

// Handing a cloud session on to another cloud in the terminal UI: c on a cloud row opens
// Hand off to ▸ with the other clouds; the plan pane shows both legs; a first leg that
// needs this terminal (Claude Code's teleport) runs here once the UI steps aside, and the
// hop goes on when it ends.

// hopPlanCmd plans the hop with the current choices.
func (m *model) hopPlanCmd() tea.Cmd {
	m.planning = true
	a, inv, e, cloud, opts := m.deps.App, m.inv, m.sel.item.Entry, m.ho.cloud, m.ho.opts
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		p, err := a.PlanHop(ctx, inv, e, cloud, "", opts)
		return planDone{plan: p, err: err}
	}
}

// hopKeys are the plan pane's keys for a hop.
func (m *model) hopKeys(k string) (tea.Model, tea.Cmd) {
	t := m.plan.Hop.Then
	switch k {
	case "esc", "q":
		m.mode, m.ho = modeBrowse, handoff{}
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
	case "m":
		m.ho.opts.Mark = !m.ho.opts.Mark
	case "e":
		if t == nil || !t.EnvNeeded || len(t.Envs) == 0 {
			return m, nil
		}
		next := 0
		for i, e := range t.Envs {
			if e.Value == t.Env {
				next = (i + 1) % len(t.Envs)
			}
		}
		m.ho.opts.Env = t.Envs[next].Value
	default:
		return m, nil
	}
	return m, m.planCmd()
}

// viewHopPlan is the plan pane of a hop.
func (m *model) viewHopPlan(b *strings.Builder) {
	p, hp := m.plan, m.plan.Hop
	fmt.Fprintf(b, "\n  %s %s\n", bold.Render("Hand on to "+hp.ToTitle), dim.Render(fmt.Sprintf("%q · from %s, through %s", p.Title, hp.FromTitle, hp.Via)))
	if m.planning {
		b.WriteString("  updating the plan…\n")
	}
	for i, l := range hp.Legs {
		fmt.Fprintf(b, "  %d. %-10s %s → %s %s\n     %s\n", i+1, l.Verb, cloudSt.Render(l.From), cloudSt.Render(l.To), dim.Render("("+l.Fidelity+")"), l.Words)
	}
	fmt.Fprintf(b, "\n  %s\n  Code  %s\n", hp.Conversation, hp.Code)
	if hp.Terminal != "" {
		b.WriteString("  " + warnSt.Render("! "+hp.Terminal) + "\n")
	}
	t := hp.Then
	if t != nil && t.EnvNeeded {
		if t.Env != "" {
			fmt.Fprintf(b, "  Environment  %s  %s\n", t.EnvName, dim.Render("[e] next"))
		} else {
			fmt.Fprintf(b, "  Environment  %s  %s\n", errSt.Render("none picked"), dim.Render("[e] "+envLabels(t.Envs)))
		}
	}
	checks := append([]move.Check(nil), hp.Bring.Fetch.Checks...)
	if t != nil {
		checks = append(checks, t.Checks...)
	}
	for _, c := range checks {
		switch c.State {
		case "ok":
			b.WriteString("  " + okSt.Render("✓") + " " + c.Text + "\n")
		case "warn":
			b.WriteString("  " + warnSt.Render("! "+c.Text) + "\n")
		default:
			b.WriteString("  " + errSt.Render("✗ "+c.Text) + "\n")
		}
	}
	if m.ho.showLoss {
		b.WriteString("\n  " + bold.Render("Everything the trip leaves behind") + "\n")
		for _, l := range hp.Loss {
			b.WriteString("   · " + l + "\n")
		}
	}
	mark := "off"
	if p.Mark == move.MarkNow {
		mark = "on"
	}
	keys := fmt.Sprintf("[m] mark the copy here: %s · [L] what stays", mark)
	if len(p.Blockers) == 0 {
		b.WriteString(dim.Render("\n  " + keys + "\n  y/enter: hand it on · esc: back\n"))
	} else {
		b.WriteString(dim.Render("\n  " + keys + "\n  resolve the ✗ first · esc: back\n"))
	}
}

// viewHopDone is where a hop stands: its first leg waiting for this terminal, done, or
// stopped.
func (m *model) viewHopDone(b *strings.Builder) {
	r, title := m.result.Hop, m.plan.Title
	switch r.State {
	case move.HopWaiting:
		fmt.Fprintf(b, "\n  %s\n", bold.Render(fmt.Sprintf("Bringing %q here from %s first", title, m.plan.Hop.FromTitle)))
		fmt.Fprintf(b, "   It runs in this terminal:\n\n")
		for _, line := range wrapCommand(r.Command, max(m.width, 80)-4, "sh") {
			b.WriteString("   " + line + "\n")
		}
		if r.Note != "" {
			b.WriteString("\n   " + warnSt.Render(r.Note+".") + "\n")
		}
		fmt.Fprintf(b, "\n   When it ends, hopsesh hands the copy on to %s.\n", r.ToTitle)
		b.WriteString(dim.Render("\n   enter: run it now · u undo · q quit (it goes on later with: hopsesh clouds continue "+m.result.Journal+")") + "\n")
		return
	case move.HopFailed:
		fmt.Fprintf(b, "\n  %s %s\n   %s\n", errSt.Render("✕"), bold.Render("The hop stopped"), errSt.Render(r.Message))
		b.WriteString(dim.Render("\n   u undo both legs · enter back to the list · q quit") + "\n")
		return
	}
	fmt.Fprintf(b, "\n  %s %s\n", okSt.Render("✓"), bold.Render("Handed on to "+r.ToTitle))
	fmt.Fprintf(b, "   %q came here from %s first: %s\n", title, m.plan.Hop.FromTitle, r.Key)
	if h := m.result.Handoff; h != nil {
		fmt.Fprintf(b, "   %s %s is %s\n", upperFirst(nonEmpty(h.Noun, "session")), shortID(h.Session), nonEmpty(h.State, "running"))
		if h.URL != "" {
			fmt.Fprintf(b, "   %s\n", link(h.URL, h.URL))
		}
		if h.Branch != "" {
			fmt.Fprintf(b, "   branch  %s\n", h.Branch)
		}
		if h.MarkText != "" {
			fmt.Fprintf(b, "   ↪ the copy here is marked “%s”\n", strings.TrimPrefix(h.MarkText, "↪ "))
		}
	}
	for _, w := range m.result.Warnings {
		b.WriteString("   " + warnSt.Render("! "+w) + "\n")
	}
	if m.ho.notice != "" {
		b.WriteString("\n   " + okSt.Render(m.ho.notice) + "\n")
	}
	b.WriteString(dim.Render("\n   o open in browser · u undo both legs · enter back to the list · q quit") + "\n")
}

// hopDoneKeys are the keys of a hop's done view.
func (m *model) hopDoneKeys(k string) (tea.Model, tea.Cmd) {
	r := m.result.Hop
	switch k {
	case "q":
		return m, tea.Quit
	case "esc":
		m.ho, m.mode = handoff{}, modeLoading
		return m, m.Init()
	case "enter":
		if r.State == move.HopWaiting && len(r.Run.Argv) > 0 {
			m.exit = &Exit{RunDir: r.Run.Dir, RunArgv: r.Run.Argv, Unset: r.Run.Unset, Adopt: r.Fetch, Hop: m.result.Journal}
			return m, tea.Quit
		}
		m.ho, m.mode = handoff{}, modeLoading
		return m, m.Init()
	case "u":
		return m, m.undoCmd(m.result.Journal)
	case "o":
		if h := m.result.Handoff; h != nil && h.URL != "" {
			_ = openBrowser(h.URL)
		}
	}
	return m, nil
}

// showHop opens the UI on a hop the caller took on after its first leg ran here.
func (m *model) showHop(id string) {
	a := m.deps.App
	rec, err := a.LoadHop(id)
	if err != nil {
		return
	}
	res := &move.Result{Journal: rec.Journal, Hop: &rec.Result}
	if rec.Handoff != "" {
		if h, err := move.LoadHandoff(a.StateDir, rec.Handoff); err == nil {
			res.Handoff = &move.HandoffResult{Cloud: h.Cloud, CloudTitle: rec.ToTitle, Session: string(h.Session.Session), URL: h.URL, Branch: h.Branch, State: "running"}
			if _, cl, ok := a.CloudModule(rec.To); ok {
				res.Handoff.Noun = cl.SessionNoun()
			}
		}
	}
	m.plan = &move.Plan{Kind: move.KindHop, Title: rec.Title, Hop: &move.HopPlan{From: rec.From, To: rec.To, ToTitle: rec.ToTitle, FromTitle: rec.From}}
	if _, cl, ok := a.CloudModule(rec.From); ok {
		m.plan.Hop.FromTitle = cl.Title
	}
	m.result, m.mode = res, modeDone
}
