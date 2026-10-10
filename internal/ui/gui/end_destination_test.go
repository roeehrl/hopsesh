package gui

import (
	"testing"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/core/move"
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
