package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/codex"
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
		"w": {Recorded: true, Email: "work@example.com", Current: true, Key: "k:k-work"},
		"p": {Recorded: true, Email: "me@example.com", Profile: "Personal", Key: "k:k-personal"},
		"x": {Recorded: true, Key: "k:k-gone"},
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

// Runs that name the user match only that user; a session that changed accounts says so;
// a run naming only a shared organisation names no one.
func TestOwnerOfPeopleRunsAndSharedOrganisations(t *testing.T) {
	inv := ownersInventory()
	work := inv.Machines[0].Agents[0].Install.Profile
	work.Account.Person = "u-roy"
	e := inv.Entries[0]
	e.Session.Mirror = nil
	e.Session.Owners = []agent.OwnerRun{{Key: "k-personal"}, {Key: "k-work", Person: "u-roy"}}
	l := inv.Logins()
	if got := l.OwnerOf(e); !got.Current || got.Email != "work@example.com" || got.Key != "p:u-roy" || got.Earlier != "me@example.com" {
		t.Fatalf("changed accounts: %+v", got)
	}
	e.Session.Owners = []agent.OwnerRun{{Key: "k-work", Person: "u-colleague"}}
	if got := l.OwnerOf(e); got.Current || got.Email != "" || OwnerLabel(got) != "another account" {
		t.Fatalf("a colleague in the same organisation is not the signed-in user: %+v", got)
	}
	work.Account.Label = "team"
	work.Account.Person = ""
	e.Session.Owners = []agent.OwnerRun{{Key: "k-work"}}
	if got := l.OwnerOf(e); got.Current || got.Email != "" {
		t.Fatalf("a shared organisation names no one: %+v", got)
	}
}

func TestCrossAccountResumeRefusesOnlyWhatWouldFail(t *testing.T) {
	dir := t.TempDir()
	enc, plain := filepath.Join(dir, "enc.jsonl"), filepath.Join(dir, "plain.jsonl")
	os.WriteFile(enc, []byte(`{"type":"response_item","payload":{"type":"reasoning","encrypted_content":"x"}}`+"\n"), 0o600)
	os.WriteFile(plain, []byte(`{"type":"response_item","payload":{"type":"message"}}`+"\n"), 0o600)
	in := agent.Install{Profile: &agent.RuntimeProfile{Account: &agent.Account{LoggedIn: true, Key: "a", Email: "me@example.com"}}}
	for _, tc := range []struct {
		name string
		mod  agent.Module
		runs []agent.OwnerRun
		path string
		fail bool
	}{
		{"same account", codex.New(), []agent.OwnerRun{{Key: "a"}}, enc, false},
		{"other account", codex.New(), []agent.OwnerRun{{Key: "b"}}, enc, true},
		{"changed accounts", codex.New(), []agent.OwnerRun{{Key: "b"}, {Key: "a"}}, enc, true},
		{"unknown, encrypted", codex.New(), nil, enc, true},
		{"unknown, plain", codex.New(), nil, plain, false},
		{"claude resumes across accounts", claude.New(), []agent.OwnerRun{{Key: "b"}}, enc, false},
	} {
		e := Entry{Session: agent.Summary{Path: tc.path, Owners: tc.runs}}
		if err := crossAccountResume(tc.mod, in, e); (err != nil) != tc.fail {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}

// Before its folder's sign-in is checked, a recorded account no folder is known to be
// signed in to is not called "another account".
func TestOwnerPendingUntilTheFolderIsChecked(t *testing.T) {
	e := Entry{Agent: "claude", Session: agent.Summary{Owners: []agent.OwnerRun{{Key: "org"}}}, Profile: &agent.RuntimeProfile{ID: "p"}}
	if o := (Logins{}).OwnerOf(e); !o.Pending || OwnerLabel(o) != "" {
		t.Fatalf("%+v %q", o, OwnerLabel(o))
	}
	e.Profile.Account = &agent.Account{LoggedIn: true, Key: "other"}
	if o := (Logins{}).OwnerOf(e); o.Pending || OwnerLabel(o) != "another account" {
		t.Fatalf("checked: %+v", o)
	}
}
