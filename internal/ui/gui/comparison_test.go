package gui

import (
	"encoding/json"
	"testing"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Local Plan and remote PushPlan use the same DTO conversion. Evidence must
// survive JSON transport with distinct identities rather than cached labels.
func TestPlanDTOExportsFreshConversationComparison(t *testing.T) {
	c := &move.Comparison{Verified: true, Classification: "independent", SharedRevisions: 3,
		Source:      move.ComparisonSide{Identity: move.ComparisonIdentity{Agent: "codex", Machine: "source", Title: "Incoming", Key: agent.SessionKey{Agent: "codex", Session: "new"}}, ExclusiveKnown: true, Counts: move.ComparisonCounts{Messages: 2}},
		Destination: move.ComparisonSide{Identity: move.ComparisonIdentity{Agent: "claude", Machine: "target", Title: "Original", Key: agent.SessionKey{Agent: "claude", Session: "old"}}, ExclusiveKnown: true, Counts: move.ComparisonCounts{Messages: 1}}}
	p := &move.Plan{Kind: move.KindContinue, Conflict: "independent histories", Continue: &move.ContinuePlan{Comparison: c, Relation: move.RelationDiverged}}
	d := planDTO(p, app.Entry{}, claude.New())
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip PlanDTO
	if err = json.Unmarshal(b, &roundTrip); err != nil {
		t.Fatal(err)
	}
	got := roundTrip.Continue.Comparison
	if got == nil || !got.Verified || got.Source.Identity.Title != "Incoming" || got.Destination.Identity.Key.Session != "old" || got.Destination.Counts.Messages != 1 {
		t.Fatalf("comparison lost in transport: %s", b)
	}
}
