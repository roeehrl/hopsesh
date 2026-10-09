package app

import (
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"testing"
	"time"
)

func TestAccountGroupsKeepSameEmailAndNativeIDsSeparate(t *testing.T) {
	inv := &Inventory{}
	for _, id := range []string{"first", "second"} {
		p := &agent.RuntimeProfile{ID: id, Name: id, Account: &agent.Account{Email: "alice@example.com"}}
		inv.Entries = append(inv.Entries, Entry{Machine: "A", Agent: "claude", AgentName: "Claude Code", Profile: p, Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Profile: id, Session: "same"}}})
	}
	groups := inv.AccountGroups()
	if len(groups) != 2 || groups[0].Identity == groups[1].Identity {
		t.Fatal(groups)
	}
	for _, g := range groups {
		if len(g.Items) != 1 {
			t.Fatal("collapsed separate profile sessions", g)
		}
	}
}

func TestRecentAccountObservationRejectsFutureAndStaleChecks(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		at   time.Time
		want bool
	}{{now, true}, {now.Add(-6 * time.Minute), false}, {now.Add(time.Hour), false}, {time.Time{}, false}} {
		if got := recentAccountObservation(tc.at); got != tc.want {
			t.Fatalf("freshness %v: %v", tc.at, got)
		}
	}
}

func TestMacSSHLoginAbsenceDoesNotProveOwnerSignedOut(t *testing.T) {
	for _, tc := range []struct {
		local    bool
		os       string
		signedIn bool
		blocked  bool
	}{{false, "darwin", false, true}, {true, "darwin", false, false}, {false, "linux", false, false}, {false, "darwin", true, false}} {
		m := &host.Machine{Local: tc.local, Name: "laptop", Facts: host.Facts{OS: tc.os}}
		if err := accountProbeVisibility(m, agent.Account{LoggedIn: tc.signedIn}, nil); (err != nil) != tc.blocked {
			t.Fatalf("visibility %+v: %v", tc, err)
		}
	}
}
