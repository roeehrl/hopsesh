package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/move"
)

type returnPlanDone struct {
	push *app.Push
	err  error
}

func (m *model) openReturns() bool {
	if m.cursor >= len(m.rows) || m.rows[m.cursor].item == nil || len(m.rows[m.cursor].item.Entry.Returns) == 0 {
		return false
	}
	m.sel, m.returnCursor, m.mode = m.rows[m.cursor], 0, modeReturns
	m.notice = ""
	return true
}

func (m *model) returnKeys(k string) (tea.Model, tea.Cmd) {
	if m.planning {
		return m, nil
	}
	candidates := m.sel.item.Entry.Returns
	switch k {
	case "esc", "q":
		m.mode = modeBrowse
	case "down", "j":
		m.returnCursor = min(len(candidates)-1, m.returnCursor+1)
	case "up", "k":
		m.returnCursor = max(0, m.returnCursor-1)
	case "enter":
		r := candidates[m.returnCursor]
		switch r.Status {
		case "same", "behind", "live":
			for i, row := range m.rows {
				if row.item != nil && row.item.Entry.Machine == r.Machine && row.item.Entry.Session.Key.String() == r.Key {
					m.cursor, m.mode = i, modeBrowse
					m.scroll()
					return m, nil
				}
			}
			if m.inv != nil {
				for _, e := range m.inv.Entries {
					if e.Machine == r.Machine && e.Session.Key.String() == r.Key {
						// Select the exact raw replica, replacing this branch's representative.
						it := *m.sel.item
						it.Entry = e
						m.rows[m.cursor] = row{item: &it}
						m.sel = m.rows[m.cursor]
						m.mode = modeBrowse
						return m, nil
					}
				}
			}
			m.notice = "Destination is not in this scan. Refresh its machine to show it."
			return m, nil
		case "available", "verify", "diverged", "missing":
		default:
			return m, nil
		}
		if !r.Local && m.sel.item.Entry.Machine != app.LocalName() {
			m.notice = "Run Hopsesh on the source or destination machine to review this return."
			return m, nil
		}
		m.opts = m.deps.App.DefaultOptions()
		m.opts.TargetProfile, m.opts.TargetSession = r.Profile, r.Key
		if r.Status == "missing" {
			m.opts.TargetSession = ""
			m.opts.NewReplica = true
		}
		if r.Status == "diverged" {
			m.opts.Conflict = move.ConflictKeepBoth
		}
		m.target, m.returnTo, m.picked, m.ho = r.Agent, &r, nil, handoff{}
		return m, m.planCmd()
	}
	return m, nil
}

func (m *model) viewReturns(b *strings.Builder) {
	fmt.Fprintln(b, "\n  Move back to an existing session (same branch only)")
	candidates := m.sel.item.Entry.Returns
	height := max(1, m.height-9)
	start := max(0, m.returnCursor-height/3+1)
	for i := start; i < len(candidates) && i < start+max(1, height/3); i++ {
		r := candidates[i]
		prefix := "  "
		if i == m.returnCursor {
			prefix = "→ "
		}
		profile := r.ProfileLabel
		if profile == "" {
			profile = r.Profile
		}
		if profile == "" {
			profile = "Default account"
		}
		fmt.Fprintf(b, "  %s%s · %s on %s [%s]\n    %s\n    %s\n", prefix, r.AgentName, profile, r.Machine, r.Status, r.Key, r.Reason)
		if r.Status == "missing" {
			fmt.Fprintln(b, "    Review creates a NEW session there; the missing original is not reused.")
		}
	}
	if m.planning {
		fmt.Fprintln(b, "  Verifying exact destination and planning…")
	}
	if m.notice != "" {
		fmt.Fprintln(b, "  "+m.notice)
	}
	fmt.Fprintln(b, "\n  ↑↓ choose · enter review / show destination · esc back")
	fmt.Fprintln(b, "  Diverged copies stay separate. Offline destinations require verification.")
}

func (m *model) closeReturnPush() {
	if m.returnPush != nil {
		m.returnPush.Close()
		m.returnPush = nil
	}
}

func (m *model) returnPushPlanCmd() tea.Cmd {
	m.closeReturnPush()
	m.planning = true
	a, inv, e, target, opts, machine := m.deps.App, m.inv, m.sel.item.Entry, m.target, m.opts, m.returnTo.Machine
	return func() tea.Msg {
		to := a.Cfg.FindHost(machine)
		if to == nil || !to.Allowed {
			return returnPlanDone{err: fmt.Errorf("%s is not an allowed destination", machine)}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		p, err := a.StartPush(ctx, inv, e, *to, target, opts)
		return returnPlanDone{p, err}
	}
}
