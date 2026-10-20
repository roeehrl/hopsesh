package move

import (
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func TestPreparedPlanRestoresAcceptedIDsAndPrivateConversionState(t *testing.T) {
	p := &Plan{Kind: KindContinue, OperationID: "stable-transfer", Key: agent.SessionKey{Agent: "claude", Session: "source"}, Source: Endpoint{ID: "source-endpoint", Binding: "source-account"}, Target: Endpoint{ID: "target-endpoint", Binding: "target-account", CWD: "/repo"}, Placement: agent.Placement{Key: agent.SessionKey{Agent: "codex", Session: "accepted"}}, StartPrompt: "continue with accepted briefing", Continue: &ContinuePlan{header: ir.Header{CWD: "/repo"}, expect: ir.Cursor{Offset: 123}, head: ir.Cursor{Offset: 456}, archive: []byte("portable transcript")}}
	body, err := p.FreezePrepared()
	if err != nil {
		t.Fatal(err)
	}
	current := *p
	current.Placement.Key.Session = "new-random-plan"
	current.Continue = &ContinuePlan{}
	restored, err := RestorePrepared(body, &current)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Placement.Key.Session != "accepted" || restored.Continue.expect.Offset != 123 || restored.Continue.head.Offset != 456 || string(restored.Continue.archive) != "portable transcript" || restored.StartPrompt != p.StartPrompt {
		t.Fatal("prepared state was replaced by fresh planning")
	}
	for _, change := range []func(*Plan){func(p *Plan) { p.Target.Binding = "other-account" }, func(p *Plan) { p.Target.CWD = "/other" }, func(p *Plan) { p.Source.ID = "foreign-peer" }, func(p *Plan) { p.Key.Session = "other-fork" }} {
		changed := current
		change(&changed)
		if _, err := RestorePrepared(body, &changed); err == nil {
			t.Fatal("restore crossed accepted transfer boundary")
		}
	}
}
