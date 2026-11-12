package gui

import (
	"testing"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestEndReturnDestinationRejectsStaleReviewAndConsumesPlanOnFailure(t *testing.T) {
	home(t)
	a := NewApp(all.Registry(), WithoutSessionWatching())
	a.plan = &move.Plan{}
	a.endDestinationToken = "latest"
	a.endDestinationPlan = a.plan
	for _, token := range []string{"", "older"} {
		if err := a.EndReturnDestination(token); err == nil || a.plan == nil {
			t.Fatalf("stale review consumed the plan: %v", err)
		}
	}
	// Another kind of plan replaced the return; its predecessor's token cannot act.
	old := a.plan
	a.plan = &move.Plan{}
	if err := a.EndReturnDestination("latest"); err == nil || a.plan == nil {
		t.Fatal("token accepted after plan replacement")
	}
	a.plan = old
	// This synthetic plan has no Stopper. A failed action still invalidates it.
	if err := a.EndReturnDestination("latest"); err == nil || a.plan != nil || a.endDestinationToken != "" {
		t.Fatalf("failure left a reusable plan: %v", err)
	}
	if err := a.EndReturnDestination("latest"); err == nil {
		t.Fatal("consumed token accepted twice")
	}
}

func TestEndingOriginalInvalidatesBothReviewedSidesOnly(t *testing.T) {
	source := agent.SessionKey{Agent: "codex", Profile: "work", Session: "moved"}
	target := agent.SessionKey{Agent: "claude", Profile: "work", Session: "original"}
	a := &App{inv: &app.Inventory{Entries: []app.Entry{
		{Machine: "studio", Session: agent.Summary{Key: source}, Live: agent.LiveInfo{State: agent.Ended}},
		{Machine: "studio", Session: agent.Summary{Key: target}, Live: agent.LiveInfo{State: agent.Live}},
		{Machine: "other", Session: agent.Summary{Key: target}, Live: agent.LiveInfo{State: agent.Live}},
	}}}
	p := &move.Plan{Key: source, Source: move.Endpoint{Location: "studio"}, Target: move.Endpoint{Location: "studio"}, Placement: agent.Placement{Key: target}}
	a.invalidateReturnSelection(p)
	for _, e := range a.inv.Entries[:2] {
		if !e.Cached || e.Live.State != agent.Unknown {
			t.Fatalf("next review can reuse stale evidence: %+v", e)
		}
	}
	if a.inv.Entries[2].Cached || a.inv.Entries[2].Live.State != agent.Live {
		t.Fatal("unrelated machine's session invalidated")
	}
}
