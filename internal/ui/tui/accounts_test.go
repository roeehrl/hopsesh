package tui

import (
	"context"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"strings"
	"testing"
)

func TestAccountsFilterAndExplicitDestination(t *testing.T) {
	m := newModel(t)
	m.Update(m.Init()())
	defer func() { m.inv.Close() }()
	p, err := m.deps.App.RegisterAccount(context.Background(), "", "claude", "Another personal", "", []string{"personal", "Research"})
	if err != nil {
		t.Fatal(err)
	}
	_, cmd := m.key("a")
	m.Update(cmd())
	m.accts.filter = "Research"
	m.Update(m.loadAccounts()())
	if len(m.accts.rows) != 1 || m.accts.rows[0].ID != p.ID {
		t.Fatal("tag filter did not select the explicit profile")
	}
	if view := m.View().Content; !strings.Contains(view, "Another personal") {
		t.Fatal(view)
	}
	m.sel = m.rows[m.nextSelectable(0, 1)]
	m.accts.choosing = true
	_, cmd = m.accountKeys("r")
	m.Update(cmd())
	_, cmd = m.accountKeys("enter")
	if cmd == nil || m.opts.TargetProfile != p.ID || m.opts.TargetSession != "" {
		t.Fatal("destination selection lost profile identity")
	}
}

func TestAccountGroupsCollapseAndPreserveSeparateProfiles(t *testing.T) {
	m := &model{accts: accountView{group: "tag", rows: []agent.RuntimeProfile{{ID: "one", Name: "A", Tags: []string{"Personal", "Research"}}, {ID: "two", Name: "B", Tags: []string{"personal"}}, {ID: "three", Name: "C"}}}}
	m.accts.rebuild()
	if len(m.accts.items) != 7 {
		t.Fatalf("expected three headers and four memberships: %+v", m.accts.items)
	}
	m.accountKeys("left")
	if len(m.accts.items) != 5 {
		t.Fatal("personal group did not collapse")
	}
	m.accountKeys("left")
	if len(m.accts.items) != 5 {
		t.Fatal("left reopened collapsed group")
	}
	m.accountKeys("right")
	m.accountKeys("down")
	p, ok := m.accts.selected()
	if !ok || p.ID != "one" {
		t.Fatal("selection lost profile identity")
	}
	m.accountKeys("down")
	p, ok = m.accts.selected()
	if !ok || p.ID != "two" {
		t.Fatal("same tag collapsed distinct profiles")
	}
}
