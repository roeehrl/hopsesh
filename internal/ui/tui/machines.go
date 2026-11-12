package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/roeehrl/hopsesh/internal/app"
)

// The machines panel (m): what hopsesh can do on each machine (app.MachineStatus), check
// by check, each as a symbol and words. The browse header carries a short mark per
// machine: ⌒ hopsesh is there (green: it receives sessions), ↑ it is older than the
// newest on your machines.

// machinesView is the panel's state: the last statuses read, the selected machine, and
// whether a read is under way (the last result stays shown meanwhile).
type machinesView struct {
	list    []app.MachineStatus
	cursor  int
	loading bool
	gen     uint64
}

type machinesDone struct {
	list []app.MachineStatus
	gen  uint64
}

// loadMachines reads every machine's status off the UI goroutine; refresh reads hooks,
// skill and command again however recent.
func (m *model) loadMachines(refresh bool) tea.Cmd {
	m.machines.loading = true
	m.machines.gen++
	gen, inv, a := m.machines.gen, m.inv, m.deps.App
	if inv == nil {
		m.machines.loading = false
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		return machinesDone{list: a.MachineStatuses(ctx, inv, app.StatusOptions{Refresh: refresh}), gen: gen}
	}
}

func (m *model) machineKeys(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "esc", "q", "m":
		m.mode = modeBrowse
	case "up", "k":
		m.machines.cursor = max(0, m.machines.cursor-1)
	case "down", "j":
		m.machines.cursor = min(max(0, len(m.machines.list)-1), m.machines.cursor+1)
	case "r":
		if !m.machines.loading {
			return m, m.loadMachines(true)
		}
	}
	return m, nil
}

// hopMark is a machine's mark in the browse header: ⌒ when hopsesh is there (green when
// it receives sessions, dim when not), ↑ when it is older than the newest.
func hopMark(installed, receiving, behind bool) string {
	out := ""
	switch {
	case !installed:
	case receiving:
		out = okSt.Render("⌒")
	default:
		out = dim.Render("⌒")
	}
	if behind {
		out += warnSt.Render("↑")
	}
	return out
}

// checkSym is a check's symbol: the words beside it carry the same meaning.
func checkSym(c app.Check) string {
	switch c.State {
	case app.CheckOK:
		return okSt.Render("✓")
	case app.CheckWarn:
		if c.Fix == app.FixUpdate {
			return warnSt.Render("↑")
		}
		return warnSt.Render("!")
	case app.CheckError:
		return errSt.Render("✕")
	case app.CheckOff:
		return dim.Render("○")
	}
	return dim.Render("?")
}

// renderMachines is the machines panel for a width (pure, for tests).
func renderMachines(v machinesView, width int) string {
	var b strings.Builder
	w := max(40, width)
	head := "machines"
	if v.loading {
		head += " · checking…"
	}
	b.WriteString("  " + bold.Render(head) + "\n")
	if len(v.list) == 0 {
		if v.loading {
			b.WriteString("  " + dim.Render("reading every machine…") + "\n")
		} else {
			b.WriteString("  " + dim.Render("no machines read yet") + "\n")
		}
		b.WriteString(dim.Render("\n  r check again · esc back") + "\n")
		return b.String()
	}
	for i, s := range v.list {
		cur := "  "
		if i == v.cursor {
			cur = "▸ "
		}
		name := s.Machine
		if s.Local {
			name += " (here)"
		}
		sum := checkSym(s.Hopsesh) + " " + nonEmptyText(s.Hopsesh.Text, "–")
		if s.Todo > 0 {
			sum += dim.Render(fmt.Sprintf(" · %d to do", s.Todo))
		} else if s.Reach.State == app.CheckOK && s.Version != "" {
			sum += dim.Render(" · all set")
		}
		fmt.Fprintf(&b, "  %s%-18s %s\n", cur, truncate(name, 18), sum)
	}
	s := v.list[min(v.cursor, len(v.list)-1)]
	fmt.Fprintf(&b, "\n  %s\n", dim.Render("─ "+s.Machine+" "+strings.Repeat("─", max(2, min(w-8-len(s.Machine), 50)))))
	line := func(label string, c app.Check, extra string) {
		text := nonEmptyText(c.Text, "–") + extra
		if c.Reason != "" && c.State != app.CheckOK {
			text += dim.Render(" — " + c.Reason)
		}
		fmt.Fprintf(&b, "  %s %-12s %s\n", checkSym(c), label, ansi.Truncate(text, w-20, "…"))
	}
	if s.Reach.State != app.CheckOK {
		line("reached", s.Reach, "")
	}
	extra := ""
	if s.Newest != "" && s.Hopsesh.Fix == app.FixUpdate {
		extra = fmt.Sprintf(" (newest %s, %s)", s.Newest, s.NewestOn)
	}
	hop := s.Hopsesh
	if extra != "" {
		hop.Reason = ""
	}
	line("hopsesh", hop, extra)
	line("receiving", s.Receiving, "")
	line("login", s.Login, "")
	line("command", s.CLI, "")
	for _, a := range s.Agents {
		where := a.Name
		if a.ProfileLabel != "" {
			where += " · " + a.ProfileLabel
		}
		line("hook", a.Hook, dim.Render("  "+where))
		line("skill", a.Skill, dim.Render("  "+where))
	}
	checked := s.CheckedAt
	if !s.DetailsAt.IsZero() {
		checked = s.DetailsAt
	}
	note := "checked " + ago(checked)
	if s.Stale {
		note += " · too old to trust; r checks again"
	}
	fmt.Fprintf(&b, "  %s\n", dim.Render(note))
	b.WriteString(dim.Render("\n  ↑↓ machine · r check again · esc back") + "\n")
	return b.String()
}

func nonEmptyText(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
