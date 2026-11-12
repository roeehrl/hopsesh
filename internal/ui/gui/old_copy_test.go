package gui

import (
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// A copy a move left behind is never continued, moved, sent, handed off or planned from:
// each refusal starts with app.MovedOriginal (the window offers "Continue this old copy
// anyway…" for it) and names where the session went. A released copy, or a separate
// fork's original, goes on as any session.
func TestOldCopyIsNotContinuedOrMoved(t *testing.T) {
	a := updateApp(t)
	noReads(t, nil)
	here := app.LocalName()
	at := time.Date(2026, 10, 22, 12, 39, 0, 0, time.UTC)
	dep := &app.MovementNotice{Operation: "op", Status: "continued", Machine: here, Agent: "claude", AgentName: "Claude Code", Key: "claude/9f5c/s1", At: at}
	remote := app.Entry{Machine: "roee-macbook", Agent: "claude", AgentName: "Claude Code", Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Profile: "9f5c", Session: "s1"}, Title: "sn9 - release and fleet"}, Departure: dep}
	local := app.Entry{Machine: here, Agent: "claude", AgentName: "Claude Code", Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Profile: "0d5e", Session: "s2"}}, Departure: &app.MovementNotice{Operation: "op2", Status: "prepared", Machine: "roee-macbook", AgentName: "Claude Code"}}
	a.mu.Lock()
	a.inv = &app.Inventory{Machines: []*app.Machine{{Name: here, Local: true, Status: app.StatusOK}, {Name: "roee-macbook", Status: app.StatusOK, Hopsesh: "0.6.4", Presence: app.PresenceLogin}},
		Entries: []app.Entry{remote, local}}
	a.mu.Unlock()
	rk, lk := remote.Session.Key.String(), local.Session.Key.String()
	refused := func(what string, err error) {
		t.Helper()
		if err == nil || !strings.HasPrefix(err.Error(), app.MovedOriginal) || !strings.Contains(err.Error(), "moved to Claude Code on") {
			t.Errorf("%s: %v", what, err)
		}
	}
	_, err := a.ContinueOnMachine("roee-macbook", rk)
	refused("continue there", err)
	_, err = a.OpenOnMachine("roee-macbook", rk)
	refused("open there", err)
	_, err = a.Plan("roee-macbook", rk, "", OptsDTO{})
	refused("bring here", err)
	_, err = a.Plan("roee-macbook", rk, "codex", OptsDTO{})
	refused("continue in another agent", err)
	_, err = a.PlanHandoff("roee-macbook", rk, "claude-cloud", HandoffOptsDTO{})
	refused("hand off", err)
	_, err = a.PlanHop("roee-macbook", rk, "claude-cloud", HandoffOptsDTO{})
	refused("hop", err)
	_, err = a.PlanChangeDir("roee-macbook", rk, "/tmp", false, false)
	refused("change folder", err)
	_, err = a.PushPlan(lk, "roee-macbook", "", OptsDTO{})
	refused("send from here", err)
	_, err = a.Plan(here, lk, "codex", OptsDTO{})
	refused("continue the one here in another agent", err)

	// Released (the user continued it anyway): it goes on, so whatever fails next is not
	// the refusal.
	if err := a.snapshot().ReleaseOriginal(remote.Session.Key, "op"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ContinueOnMachine("roee-macbook", rk); err == nil || strings.HasPrefix(err.Error(), app.MovedOriginal) {
		t.Fatalf("released: %v", err)
	}
	// A separate fork's original stays current.
	a.mu.Lock()
	a.inv.Entries[1].Departure.Status = "forked"
	a.mu.Unlock()
	if _, err := a.PushPlan(lk, "roee-macbook", "", OptsDTO{}); err != nil && strings.HasPrefix(err.Error(), app.MovedOriginal) {
		t.Fatalf("fork's original: %v", err)
	}
	if _, err := a.ContinueOriginalAnyway(here, lk); err == nil {
		t.Fatal("continue anyway here: release it and resume it here instead")
	}
}
