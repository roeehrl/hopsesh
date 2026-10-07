package gui

import (
	"errors"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// RenameFamily changes a presentation label only, never the native session title
// or history. An empty label restores the title derived from the original branch.
func (a *App) RenameFamily(id, name string) error {
	if name != "" {
		var err error
		name, err = agent.CheckTitle(name)
		if err != nil {
			return err
		}
	}
	a.mu.Lock()
	if a.inv == nil {
		a.mu.Unlock()
		return errors.New("scan sessions first")
	}
	display := ""
	for _, r := range a.inv.Relationships() {
		if r.Family == id {
			display = r.Name
			break
		}
	}
	if display == "" {
		a.mu.Unlock()
		return errors.New("conversation family is no longer available; refresh sessions")
	}
	names := map[string]string{}
	for k, v := range a.core.Cfg.FamilyNames {
		names[k] = v
	}
	if name == "" {
		delete(names, id)
	} else {
		names[id] = name
		display = name
	}
	if len(names) > 1000 {
		a.mu.Unlock()
		return errors.New("too many saved family names")
	}
	old := a.core.Cfg.FamilyNames
	a.core.Cfg.FamilyNames = names
	err := a.save()
	if err != nil {
		a.core.Cfg.FamilyNames = old
	}
	a.mu.Unlock()
	if err != nil {
		return err
	}
	if a.Terms != nil {
		for _, tab := range a.Terms.Tabs() {
			if tab.Relationship != nil && tab.Relationship.Family == id {
				r := *tab.Relationship
				r.Name = display
				a.Terms.setMeta(tab.ID, func(m *TabMeta) { m.Relationship = &r })
			}
		}
	}
	return nil
}

// AssociateShell organizes a shell with a selected conversation. It does not
// claim that the shell runs the agent or write a historical lineage edge.
func (a *App) AssociateShell(id, machine, key string) error {
	if a.Terms == nil {
		return errors.New("terminal unavailable")
	}
	shell := false
	for _, tab := range a.Terms.Tabs() {
		if tab.ID == id && tab.Kind == TabShell {
			shell = true
		}
	}
	if !shell {
		return errors.New("only shell tabs can be organized manually")
	}
	var relationship *app.Relationship
	if key != "" {
		a.mu.Lock()
		if a.inv == nil {
			a.mu.Unlock()
			return errors.New("scan sessions first")
		}
		e, err := a.find(machine, key)
		if err != nil {
			a.mu.Unlock()
			return err
		}
		m := a.inv.Machine(e.Machine)
		if m == nil || !m.Local {
			a.mu.Unlock()
			return errors.New("the shell runs on this machine; choose a local conversation")
		}
		r := a.inv.Relationships()[app.EntryIdentity(e.Machine, key)]
		if name := a.core.Cfg.FamilyNames[r.Family]; name != "" {
			r.Name = name
		}
		relationship = &r
		a.mu.Unlock()
	}
	a.Terms.setMeta(id, func(m *TabMeta) {
		m.Relationship = relationship
		m.Machine = machine
		m.Key = key
		m.Association = "Organized with this conversation (shell)"
		if key == "" {
			m.Machine = ""
			m.Association = ""
		}
	})
	return nil
}
