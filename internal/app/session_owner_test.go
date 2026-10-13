package app

import (
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// ownersInventory: the default root is signed in to work now; a personal profile is signed
// in to personal. Sessions in the default root recorded work, personal, an account no
// profile is signed in to, or nothing.
func ownersInventory() *Inventory {
	work := &agent.RuntimeProfile{ID: "default", Name: "Claude Code default", Default: true, Agent: "claude", Machine: "mac",
		Account: &agent.Account{LoggedIn: true, Email: "work@example.com", Key: "k-work"}}
	personal := &agent.RuntimeProfile{ID: "personal", Name: "Personal", Agent: "claude", Machine: "mac",
		Account: &agent.Account{LoggedIn: true, Email: "me@example.com", Key: "k-personal"}}
	inv := &Inventory{Machines: []*Machine{{Name: "mac", Agents: []AgentState{
		{Agent: "claude", Install: agent.Install{Agent: "claude", Present: true, Profile: work}},
		{Agent: "claude", Install: agent.Install{Agent: "claude", Present: true, Profile: personal}},
	}}}}
	for _, s := range []struct{ id, owner string }{{"w", "k-work"}, {"p", "k-personal"}, {"x", "k-gone"}, {"n", ""}} {
		sum := agent.Summary{Key: agent.SessionKey{Agent: "claude", Profile: "default", Session: agent.SessionID(s.id)}}
		if s.owner != "" {
			sum.Mirror = &agent.CloudLink{Cloud: "claude-cloud", ID: "cse_" + agent.SessionID(s.id), Account: s.owner}
		}
		inv.Entries = append(inv.Entries, Entry{Machine: "mac", Agent: "claude", AgentName: "Claude Code", Profile: work, Session: sum})
	}
	return inv
}

func TestOwnerOfUsesTheSessionsRecordNotTheRootsSignIn(t *testing.T) {
	inv := ownersInventory()
	l := inv.Logins()
	want := map[agent.SessionID]SessionOwner{
		"w": {Recorded: true, Email: "work@example.com", Current: true},
		"p": {Recorded: true, Email: "me@example.com", Profile: "Personal"},
		"x": {Recorded: true},
		"n": {},
	}
	for _, e := range inv.Entries {
		if got := l.OwnerOf(e); got != want[e.Session.Key.Session] {
			t.Errorf("%s: %+v, want %+v", e.Session.Key.Session, got, want[e.Session.Key.Session])
		}
	}
	if got := OwnerLabel(l.OwnerOf(inv.Entries[2])); got != "another account" {
		t.Errorf("unmatched record: %q", got)
	}
	if got := OwnerLabel(l.OwnerOf(inv.Entries[3])); got != "" {
		t.Errorf("no record claims %q", got)
	}
	if got := OwnerLine(l.OwnerOf(inv.Entries[1]), inv.Entries[1].Profile); got != "run by me@example.com · stored in Claude Code default (signed in as work@example.com now)" {
		t.Errorf("line: %q", got)
	}
}

func TestOwnerOfLeavesAmbiguousFingerprintsUnnamed(t *testing.T) {
	inv := ownersInventory()
	other := &agent.RuntimeProfile{ID: "other", Name: "Teammate", Agent: "claude", Machine: "mac",
		Account: &agent.Account{LoggedIn: true, Email: "teammate@example.com", Key: "k-personal"}}
	inv.Machines[0].Agents = append(inv.Machines[0].Agents, AgentState{Agent: "claude", Install: agent.Install{Agent: "claude", Present: true, Profile: other}})
	if got := inv.Logins().OwnerOf(inv.Entries[1]); got.Email != "" || !got.Recorded {
		t.Fatalf("two emails share the fingerprint, yet %+v", got)
	}
}

func TestAccountGroupsFileSessionsUnderTheAccountThatRanThem(t *testing.T) {
	var names []string
	for _, g := range ownersInventory().AccountGroups() {
		names = append(names, g.Name+" ("+string(rune('0'+len(g.Items)))+")")
	}
	got := strings.Join(names, "\n")
	for _, want := range []string{
		"work@example.com · Claude Code · mac (1)",
		"me@example.com · Claude Code · mac · in Claude Code default (1)",
		"another account · Claude Code · mac · in Claude Code default (1)",
		"Account not recorded · Claude Code · mac · in Claude Code default (1)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
}
