package gui

import (
	"testing"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// A Quick list published before the default account was registered names a session
// without its profile. It resolves to that session in the default profile only.
func TestFindUnprofiledKeyMeansTheDefaultProfile(t *testing.T) {
	entry := func(profile string, def bool) app.Entry {
		return app.Entry{Machine: "here", Session: agent.Summary{Key: agent.SessionKey{Agent: "codex", Profile: profile, Session: "s1"}},
			Profile: &agent.RuntimeProfile{ID: profile, Default: def}}
	}
	a := &App{inv: &app.Inventory{Entries: []app.Entry{entry("work", false), entry("main", true)}}}
	e, err := a.find("here", "codex/s1")
	if err != nil || e.Session.Key.Profile != "main" {
		t.Fatalf("unprofiled key did not resolve to the default profile: %+v %v", e.Session.Key, err)
	}
	if e, err = a.find("here", "codex@work/s1"); err != nil || e.Session.Key.Profile != "work" {
		t.Fatalf("an exact key must still win: %+v %v", e.Session.Key, err)
	}
	a.inv.Entries = []app.Entry{entry("work", false)}
	if _, err = a.find("here", "codex/s1"); err == nil {
		t.Fatal("an unprofiled key resolved to a non-default profile")
	}
}
