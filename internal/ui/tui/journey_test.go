package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestDestinationSelectionCannotApplyAnUnrefreshedPlan(t *testing.T) {
	m := newModel(t)
	m.Update(m.Init()())
	for _, r := range m.rows {
		if r.item != nil {
			m.sel = r
			break
		}
	}
	choices := []agent.Summary{{Key: agent.SessionKey{Agent: "claude", Session: "original"}, Title: "Original"}, {Key: agent.SessionKey{Agent: "claude", Session: "second"}, Title: "Second copy"}}
	m.Update(planDone{plan: &move.Plan{Kind: move.KindMove, Destinations: choices, Blockers: []string{"select a destination"}}})
	if _, cmd := m.key("y"); cmd != nil {
		t.Fatal("ambiguous destination applied")
	}
	if _, cmd := m.key("d"); cmd == nil || m.opts.TargetSession != "claude/original" || !m.planning {
		t.Fatal("selection did not replan", m.opts.TargetSession)
	}
	if _, cmd := m.key("y"); cmd != nil {
		t.Fatal("stale plan applied during selection")
	}
	// The newly selected plan must retain the picker so another replica can be chosen.
	m.Update(planDone{plan: &move.Plan{Kind: move.KindMove}})
	if _, cmd := m.key("d"); cmd == nil || m.opts.TargetSession != "claude/second" {
		t.Fatal("second replica unreachable", m.opts.TargetSession)
	}
	m.Update(planDone{plan: &move.Plan{Kind: move.KindMove}})
	if view := m.View().Content; !strings.Contains(view, "choose destination session") || !strings.Contains(view, "claude/second") {
		t.Fatal(view)
	}
	m.inv.Close()
}

func TestJourneyShowsRealTransferAndCanReturnToTheList(t *testing.T) {
	m := newModel(t)
	m.Update(m.Init()())
	defer func() {
		if m.inv != nil {
			m.inv.Close()
		}
	}()
	ctx := context.Background()
	e, err := m.inv.Find(app.ParseRef("Find the codeword"))
	if err != nil {
		t.Fatal(err)
	}
	p, in, err := m.deps.App.Plan(ctx, m.inv, e, "codex", m.opts)
	if err != nil || len(p.Blockers) > 0 {
		t.Fatal(err, p.Blockers)
	}
	if _, err = m.deps.App.Apply(ctx, p, in, nil); err != nil {
		t.Fatal(err)
	}
	m.inv.Close()
	m.Update(scanDone{inv: m.deps.App.Scan(ctx, app.ScanOptions{Hosts: []string{app.LocalName()}})})
	for i, r := range m.rows {
		if r.item != nil && r.item.Entry.Agent == "codex" && r.item.Entry.Lineage != nil {
			m.cursor = i
			break
		}
	}
	m.width, m.height = 120, 10
	m.key("h")
	if m.mode != modeJourney {
		t.Fatal("journey inaccessible")
	}
	lines := strings.Join(m.journeyLines(), "\n")
	for _, want := range []string{"Branch:", "Family:", "1 transfers", "0 round trips to origin", "claude →", "/codex", "Operation:", "reasoning omitted"} {
		if !strings.Contains(lines, want) {
			t.Fatalf("missing %q: %s", want, lines)
		}
	}
	m.key("pgdown")
	if m.journeyOffset == 0 {
		t.Fatal("journey cannot scroll on a short screen")
	}
	m.key("esc")
	if m.mode != modeBrowse {
		t.Fatal("journey cannot return to sessions")
	}
}

func TestWorklessPlanAndResultExplainTheExistingSession(t *testing.T) {
	m := newModel(t)
	m.mode = modePlan
	m.plan = &move.Plan{Kind: move.KindMove, Title: "Original", NoWork: true, Placement: agent.Placement{Key: agent.SessionKey{Agent: "claude", Session: "original"}}}
	if v := m.View().Content; !strings.Contains(v, "Sync lineage receipts") || !strings.Contains(v, "claude/original") || !strings.Contains(v, "0 new messages, 0 transfers") {
		t.Fatal(v)
	}
	m.mode, m.result = modeDone, &move.Result{}
	if v := m.View().Content; !strings.Contains(v, "already synchronized") || !strings.Contains(v, "0 new messages, 0 transfers") {
		t.Fatal(v)
	}
}
