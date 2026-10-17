package tui

import (
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"testing"
	"time"
)

func TestTUIRegistrationPreservesSelectedFile(t *testing.T) {
	for _, update := range []string{"partial", "complete", "runtime"} {
		t.Run(update, func(t *testing.T) {
			m := newModel(t)
			m.mode, m.width, m.height = modeBrowse, 120, 40
			machine := app.LocalName()
			m.inv = &app.Inventory{Machines: []*app.Machine{{Name: machine, Local: true}}}
			for i, id := range []agent.SessionID{"first", "selected"} {
				m.inv.Entries = append(m.inv.Entries, app.Entry{Machine: machine, Location: agent.MachineLocation(machine), Agent: "claude", AgentName: "Claude", Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Session: id}, Path: "/native/" + string(id), Title: string(id), LastActivity: time.Now().Add(-time.Duration(i) * time.Hour)}})
			}
			m.buildRows()
			for i, r := range m.rows {
				if r.item != nil && r.item.Entry.Session.Key.Session == "selected" {
					m.cursor = i
				}
			}
			if m.rows[m.cursor].item == nil || m.rows[m.cursor].item.Entry.Session.Key.Session != "selected" {
				t.Fatal("fixture selection missing")
			}
			fresh := &app.Inventory{Machines: m.inv.Machines, Entries: append([]app.Entry(nil), m.inv.Entries...), Discovering: update == "partial"}
			for i := range fresh.Entries {
				e := &fresh.Entries[i]
				e.Cached = fresh.Discovering
				e.Session.Key.Profile = "default"
				e.Profile = &agent.RuntimeProfile{ID: "default", Agent: "claude", Default: true, Endpoint: "endpoint", Root: "/native"}
			}
			switch update {
			case "partial":
				m.Update(scanPartial{fresh, m.scanGeneration})
			case "complete":
				m.Update(scanDone{fresh, m.scanGeneration})
			case "runtime":
				m.inv = fresh
				m.rebuildRuntimeRows()
			}
			r := m.rows[m.cursor]
			if r.item == nil || r.item.Entry.Session.Key.Session != "selected" || r.item.Entry.Session.Key.Profile != "default" {
				t.Fatalf("registration moved selection to %+v", r)
			}
		})
	}
}

func TestTUIRegistrationRefusesOtherScopes(t *testing.T) {
	for _, name := range []string{"remote", "cloud", "different file", "different session", "explicit profile", "nondefault", "missing endpoint", "missing root", "mismatched profile", "cached", "ambiguous"} {
		t.Run(name, func(t *testing.T) {
			m := newModel(t)
			machine := app.LocalName()
			selected := app.Entry{Machine: machine, Location: agent.MachineLocation(machine), Agent: "claude", Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Session: "selected"}, Path: "/native/selected"}}
			e := selected
			e.Session.Key.Profile = "default"
			e.Profile = &agent.RuntimeProfile{ID: "default", Agent: "claude", Default: true, Endpoint: "endpoint", Root: "/native"}
			m.inv = &app.Inventory{Machines: []*app.Machine{{Name: machine, Local: true}}}
			switch name {
			case "remote":
				m.inv.Machines[0].Local = false
			case "cloud":
				selected.Location = agent.CloudLocation(machine)
				e.Location = selected.Location
			case "different file":
				e.Session.Path += ".other"
			case "different session":
				e.Session.Key.Session = "other"
			case "explicit profile":
				selected.Session.Key.Profile = "explicit"
			case "nondefault":
				e.Profile.Default = false
			case "missing endpoint":
				e.Profile.Endpoint = ""
			case "missing root":
				e.Profile.Root = ""
			case "mismatched profile":
				e.Profile.ID = "other"
			case "cached":
				e.Cached = true
			case "ambiguous":
				other := e
				p := *e.Profile
				p.ID = "other"
				other.Profile, other.Session.Key.Profile = &p, p.ID
				m.inv.Entries = append(m.inv.Entries, other)
			}
			m.inv.Entries = append(m.inv.Entries, e)
			m.buildRows()
			if i := m.registeredDefaultRow(selected); i != -1 {
				t.Fatalf("uncertain scope selected row %d", i)
			}
		})
	}
}
