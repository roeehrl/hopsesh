package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

type accountView struct {
	filter, root string
	tags         []string
	rows         []agent.RuntimeProfile
	cursor       int
	group        string
	collapsed    map[string]bool
	items        []accountRow
	editing      string
	input        string
	name         string
	agent        agent.ID
	busy         bool
	problem      string
	choosing     bool
}
type accountRow struct {
	profile int
	group   string
	count   int
}

func (a *accountView) selected() (agent.RuntimeProfile, bool) {
	if a.cursor < 0 || a.cursor >= len(a.items) || a.items[a.cursor].profile < 0 {
		return agent.RuntimeProfile{}, false
	}
	return a.rows[a.items[a.cursor].profile], true
}
func (a *accountView) rebuild() {
	a.items = nil
	if a.group == "" {
		for i := range a.rows {
			a.items = append(a.items, accountRow{profile: i})
		}
		a.cursor = min(a.cursor, max(0, len(a.items)-1))
		return
	}
	groups := map[string][]int{}
	labels := map[string]string{}
	for i, p := range a.rows {
		names := []string{p.Machine}
		if a.group == "agent" {
			names = []string{string(p.Agent)}
		}
		if a.group == "tag" {
			names = p.Tags
			if len(names) == 0 {
				names = []string{"Untagged"}
			}
		}
		for _, name := range names {
			if name == "" {
				name = "Machine unavailable"
			}
			k := strings.ToLower(name)
			groups[k] = append(groups[k], i)
			if labels[k] == "" {
				labels[k] = name
			}
		}
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		a.items = append(a.items, accountRow{profile: -1, group: labels[k], count: len(groups[k])})
		if !a.collapsed[a.group+":"+k] {
			for _, i := range groups[k] {
				a.items = append(a.items, accountRow{profile: i, group: labels[k]})
			}
		}
	}
	a.cursor = min(a.cursor, max(0, len(a.items)-1))
}

type accountsDone struct {
	inv  *app.Inventory
	rows []agent.RuntimeProfile
	err  error
}

func (m *model) loadAccounts() tea.Cmd {
	m.accts.busy = true
	return func() tea.Msg { ps, err := m.deps.App.Accounts(); return accountsDone{rows: ps, err: err} }
}
func (m *model) accountKeys(k string) (tea.Model, tea.Cmd) {
	a := &m.accts
	if a.busy {
		return m, nil
	}
	if a.editing != "" {
		switch k {
		case "esc":
			a.editing = ""
			a.input = ""
		case "backspace":
			if len(a.input) > 0 {
				_, n := utf8.DecodeLastRuneInString(a.input)
				a.input = a.input[:len(a.input)-n]
			}
		case "enter":
			value := strings.TrimSpace(a.input)
			field := a.editing
			if field == "new-name" {
				if value == "" {
					return m, nil
				}
				a.name = value
				a.input = ""
				a.editing = "new-tags"
				return m, nil
			}
			if field == "search" {
				a.filter = value
				a.editing = ""
				a.input = ""
				return m, m.loadAccounts()
			}
			if field == "new-tags" {
				a.tags = strings.Split(value, ",")
				a.editing = "new-root (empty creates a local profile)"
				a.input = ""
				return m, nil
			}
			if strings.HasPrefix(field, "new-root") {
				a.root = value
				a.editing = "new-machine (empty uses this machine)"
				a.input = ""
				return m, nil
			}
			selected, _ := a.selected()
			if field == "forget (type the account name)" && value != selected.Name {
				a.problem = "The name did not match; registration kept"
				return m, nil
			}
			name, agentID, root, tags := a.name, a.agent, a.root, a.tags
			a.editing = ""
			a.input = ""
			a.busy = true
			return m, func() tea.Msg {
				var err error
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				switch field {
				case "new-machine (empty uses this machine)":
					_, err = m.deps.App.RegisterAccount(ctx, value, agentID, name, root, tags)
				case "forget (type the account name)":
					err = m.deps.App.ForgetAccount(selected.ID, selected.Generation)
				case "rename":
					err = m.deps.App.EditAccount(selected.ID, value, selected.Tags, selected.Generation)
				case "tags":
					err = m.deps.App.EditAccount(selected.ID, selected.Name, strings.Split(value, ","), selected.Generation)
				}
				if err != nil {
					return accountsDone{err: err}
				}
				inv := m.deps.App.Scan(ctx, app.ScanOptions{SkipGit: true})
				ps, err := m.deps.App.Accounts()
				return accountsDone{rows: ps, err: err, inv: inv}
			}
		default:
			if k == "space" {
				a.input += " "
			} else if utf8.RuneCountInString(k) == 1 {
				a.input += k
			}
		}
		return m, nil
	}
	switch k {
	case "g":
		switch a.group {
		case "":
			a.group = "machine"
		case "machine":
			a.group = "agent"
		case "agent":
			a.group = "tag"
		default:
			a.group = ""
		}
		a.cursor = 0
		a.rebuild()
	case "u":
		if a.filter == "@untagged" {
			a.filter = ""
		} else {
			a.filter = "@untagged"
		}
		return m, m.loadAccounts()
	case "left", "right", "space":
		if a.cursor < len(a.items) && a.group != "" {
			item := a.items[a.cursor]
			groupKey := a.group + ":" + strings.ToLower(item.group)
			if a.collapsed == nil {
				a.collapsed = map[string]bool{}
			}
			a.collapsed[groupKey] = !a.collapsed[groupKey]
			if k == "left" {
				a.collapsed[groupKey] = true
			}
			if k == "right" {
				a.collapsed[groupKey] = false
			}
			// Keep selection on the same group header as its contents change.
			for a.cursor > 0 && a.items[a.cursor].profile >= 0 {
				a.cursor--
			}
			a.rebuild()
		}
	case "/":
		a.editing = "search"
		a.input = a.filter
	case "esc", "q":
		m.mode = modeBrowse
	case "up", "k":
		a.cursor = max(0, a.cursor-1)
	case "down", "j":
		a.cursor = min(max(0, len(a.items)-1), a.cursor+1)
	case "1", "2":
		a.agent = "claude"
		if k == "2" {
			a.agent = "codex"
		}
		a.editing = "new-name"
		a.input = ""
	case "r":
		a.busy = true
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			inv := m.deps.App.Scan(ctx, app.ScanOptions{SkipGit: true, ForceAccounts: true})
			ps, err := m.deps.App.Accounts()
			return accountsDone{rows: ps, err: err, inv: inv}
		}
	case "n", "t", "d":
		if p, ok := a.selected(); ok {
			a.editing = "rename"
			a.input = p.Name
			if k == "t" {
				a.editing = "tags"
				a.input = strings.Join(p.Tags, ", ")
			}
			if k == "d" {
				a.editing = "forget (type the account name)"
				a.input = ""
			}
		}
	case "enter":
		p, ok := a.selected()
		if !a.choosing || m.sel.item == nil || !ok {
			return m, nil
		}
		m.opts.TargetProfile = p.ID
		m.opts.TargetSession = ""
		m.target = p.Agent
		m.mode = modePlan
		return m, m.planCmd()
	case "l":
		if p, ok := a.selected(); ok {
			a.busy = true
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				c, err := m.deps.App.AccountLogin(ctx, p.ID)
				if err != nil {
					return accountsDone{err: err}
				}
				return accountLoginReady{command: c, profile: p.ID}
			}
		}
	}
	return m, nil
}

type accountLoginReady struct {
	command agent.Command
	profile string
}

func (m *model) viewAccounts(b *strings.Builder) {
	a := &m.accts
	b.WriteString("\n  Accounts · esc: Back to sessions\n")
	fmt.Fprintf(b, "  %d accounts · Group: %s · Filter: %s\n", len(a.rows), nonEmptyAccount(a.group, "none"), nonEmptyAccount(a.filter, "all"))
	if a.choosing {
		b.WriteString("  Choose the destination account. Transfers use portable conversation history.\n")
	}
	if a.busy {
		b.WriteString("  Checking accounts…\n")
	}
	if a.problem != "" {
		fmt.Fprintf(b, "  %s\n", errSt.Render(a.problem))
	}
	start := max(0, a.cursor-max(1, (m.height-12)/3)+1)
	for i := start; i < len(a.items) && i < start+max(1, (m.height-12)/3); i++ {
		item := a.items[i]
		mark := "  "
		if i == a.cursor {
			mark = "→ "
		}
		if item.profile < 0 {
			indicator := "▾"
			if a.collapsed[a.group+":"+strings.ToLower(item.group)] {
				indicator = "▸"
			}
			fmt.Fprintf(b, "  %s%s %s · %d\n", mark, indicator, item.group, item.count)
			continue
		}
		p := a.rows[item.profile]
		label := "not checked"
		if p.Account != nil {
			label = "signed out"
			if p.Account.LoggedIn {
				label = "signed in · " + nonEmptyAccount(p.Account.Email, p.Account.Label)
			}
		}
		if p.Error != "" {
			label = p.Error
		}
		fmt.Fprintf(b, "  %s%s · %s [%s]\n    %s\n    %s\n", mark, p.Name, p.Agent, strings.Join(p.Tags, ", "), truncate(label, max(20, m.width-6)), truncate(p.Root, max(20, m.width-6)))
	}
	if a.editing != "" {
		fmt.Fprintf(b, "\n  %s: %s▌\n  enter: save/next · esc: cancel\n", a.editing, a.input)
	} else {
		b.WriteString("\n  ↑↓ select · / search · u untagged · g group · ←/→/space collapse group\n  n rename · t tags · d forget · l sign in · r scan\n  1 add Claude · 2 add Codex · enter choose destination\n")
	}
}

func nonEmptyAccount(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
