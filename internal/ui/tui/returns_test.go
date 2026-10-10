package tui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestMovementDetailsFit24RowTerminal(t *testing.T) {
	for _, width := range []int{80, 120} {
		for _, movement := range []bool{false, true} {
			for _, returns := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d/movement=%t/returns=%t", width, movement, returns), func(t *testing.T) {
					m := &model{width: width, height: 24, mode: modeBrowse, inv: &app.Inventory{}, deps: Deps{Describe: func(app.Entry) string { return "main · clean" }}}
					e := app.Entry{Machine: "studio", Agent: "codex", AgentName: "Codex", Lineage: lineage.New("fixture"),
						Session: agent.Summary{Key: agent.SessionKey{Agent: "codex", Session: "fixture"}, Title: "Return checkpoint", LastPrompt: "Continue the work"}}
					if movement {
						e.Movement = &app.MovementNotice{Status: "prepared", Text: "Prepared in Claude Code; work has not yet been observed."}
					}
					if returns {
						e.Returns = []app.ReturnCandidate{{Status: "available"}}
					}
					for range 30 {
						m.rows = append(m.rows, row{item: &app.Item{Entry: e}})
					}
					m.cursor = len(m.rows) - 1
					m.scroll()
					view := m.View().Content
					lines := 0
					for _, line := range strings.Split(strings.TrimSuffix(view, "\n"), "\n") {
						lines += max(1, (lipgloss.Width(line)+width-1)/width)
					}
					if lines > 24 || m.cursor < m.offset || m.cursor >= m.offset+m.listHeight() {
						t.Fatalf("selected row or footer clipped: %d rows\n%s", lines, view)
					}
					if !strings.Contains(view, "q quit") || movement && !strings.Contains(view, "Movement [prepared]") || returns && !strings.Contains(view, "[R] move back") {
						t.Fatal("movement, return action or footer missing", view)
					}
				})
			}
		}
	}
}

func TestReturnSelectionExactProfileRequiresExplicitConflictChoice(t *testing.T) {
	m := newModel(t)
	e := app.Entry{Machine: app.LocalName(), Agent: "codex", Returns: []app.ReturnCandidate{
		{Agent: "claude", Profile: "first", Key: "claude@first/original", Local: true, Status: "available"},
		{Agent: "claude", Profile: "second", Key: "claude@second/original", Local: true, Status: "diverged"},
	}}
	m.rows = []row{{item: &app.Item{Entry: e}}}
	m.cursor = 0
	if !m.openReturns() {
		t.Fatal("no return chooser")
	}
	m.returnKeys("down")
	_, cmd := m.returnKeys("enter")
	if cmd == nil || m.opts.TargetProfile != "second" || m.opts.TargetSession != "claude@second/original" || m.opts.Conflict != "" {
		t.Fatalf("wrong return: %+v", m.opts)
	}
	if _, cmd = m.returnKeys("enter"); cmd != nil {
		t.Fatal("double plan while verifying")
	}
	// The initial blocked review is not consent to preserve a separate branch.
	m.Update(planDone{plan: &move.Plan{Kind: move.KindContinue, Agent: "Claude Code", Conflict: "independent work", Continue: &move.ContinuePlan{Relation: move.RelationDiverged}, Blockers: []string{"choose --keep-both"}}})
	if _, cmd = m.key("enter"); cmd != nil || m.mode != modePlan {
		t.Fatal("return applied without an explicit choice")
	}
	if _, cmd = m.key("R"); cmd != nil || m.opts.Conflict != "" {
		t.Fatal("unsupported replacement selected")
	}
	if _, cmd = m.key("B"); cmd == nil || m.opts.Conflict != move.ConflictKeepBoth || !m.planning || m.mode != modePlan {
		t.Fatal("B must request review, not apply", m.opts)
	}
	if _, cmd = m.key("enter"); cmd != nil {
		t.Fatal("applied the stale plan before separate-session review")
	}
	if _, cmd = m.key("B"); cmd != nil || m.opts.Conflict != move.ConflictKeepBoth {
		t.Fatal("changed the conflict choice during review")
	}
	m.Update(planDone{plan: &move.Plan{Kind: move.KindContinue, Agent: "Claude Code", Conflict: "independent work", Continue: &move.ContinuePlan{Relation: move.RelationNew}, Options: m.opts}})
	if _, cmd = m.key("enter"); cmd == nil || m.mode != modeApplying {
		t.Fatal("reviewed explicit choice could not be confirmed")
	}
}

func TestContinueConflictCannotSelectUnsupportedReplacement(t *testing.T) {
	for _, p := range []*move.Plan{
		{Kind: move.KindContinue, Conflict: "independent work"},
		{Continue: &move.ContinuePlan{}, Conflict: "unavailable evidence"},
	} {
		m := &model{mode: modePlan, plan: p}
		if _, cmd := m.key("R"); cmd != nil || m.opts.Conflict != "" {
			t.Fatal("continuation replacement selected", m.opts)
		}
		var b strings.Builder
		m.viewPlan(&b)
		if strings.Contains(b.String(), "[R] replace") || !strings.Contains(b.String(), "[B] review separate") {
			t.Fatal(b.String())
		}
	}
}

func TestReturnEscapeCancelsSeparateSessionReview(t *testing.T) {
	m := newModel(t)
	m.mode = modePlan
	m.plan = &move.Plan{Kind: move.KindContinue, Conflict: "independent work"}
	m.returnTo = &app.ReturnCandidate{Local: true}
	m.opts.Conflict = move.ConflictKeepBoth
	if _, cmd := m.key("esc"); cmd != nil || m.mode != modeBrowse || m.returnTo != nil || m.opts.Conflict != "" {
		t.Fatal("escape did not cancel without applying", m.opts)
	}
}

func TestSynchronizedReturnSelectsDestinationWithoutPlan(t *testing.T) {
	m := newModel(t)
	c := app.ReturnCandidate{Machine: app.LocalName(), Key: "claude@work/original", Profile: "work", Local: true, Status: "same"}
	m.rows = []row{{item: &app.Item{Entry: app.Entry{Returns: []app.ReturnCandidate{c}}}}, {item: &app.Item{Entry: app.Entry{Machine: c.Machine, Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Profile: "work", Session: "original"}}}}}}
	m.cursor = 0
	m.openReturns()
	if _, cmd := m.returnKeys("enter"); cmd != nil || m.cursor != 1 || m.mode != modeBrowse {
		t.Fatal("synchronized return should show exact destination")
	}
}

func TestForkWithNoCandidatesCannotReturnToParent(t *testing.T) {
	m := newModel(t)
	m.rows = []row{{item: &app.Item{}}}
	m.cursor = 0
	if m.openReturns() {
		t.Fatal("inferred a parent return")
	}
}

func TestRemotePreparedResultNeverRunsDestinationLocally(t *testing.T) {
	m := newModel(t)
	m.mode = modeDone
	m.returnTo = &app.ReturnCandidate{Machine: "studio"}
	m.plan = &move.Plan{Target: move.Endpoint{Location: "studio"}}
	m.result = &move.Result{}
	if _, cmd := m.key("enter"); cmd != nil || m.exit != nil {
		t.Fatal("remote result launched here")
	}
	var b strings.Builder
	m.viewDone(&b)
	if strings.Contains(b.String(), "on this machine") || strings.Contains(b.String(), "enter: start") {
		t.Fatal(b.String())
	}
}

func TestSynchronizedReturnFindsHiddenInventoryReplica(t *testing.T) {
	m := newModel(t)
	key := agent.SessionKey{Agent: "claude", Profile: "work", Session: "original"}
	destination := app.Entry{Machine: app.LocalName(), Agent: "claude", Session: agent.Summary{Key: key, Title: "Original"}}
	source := app.Entry{Machine: app.LocalName(), Agent: "codex", Returns: []app.ReturnCandidate{{Machine: destination.Machine, Key: key.String(), Profile: "work", Local: true, Status: "same"}}}
	m.inv = &app.Inventory{Entries: []app.Entry{source, destination}}
	m.rows = []row{{item: &app.Item{Entry: source}}}
	m.cursor = 0
	m.openReturns()
	if _, cmd := m.returnKeys("enter"); cmd != nil {
		t.Fatal("planned a transfer")
	}
	if m.mode != modeBrowse || m.rows[m.cursor].item.Entry.Session.Key != key {
		t.Fatal("did not show hidden exact replica")
	}
}

func TestMissingReturnClearsExactSessionForNewSessionReview(t *testing.T) {
	m := newModel(t)
	m.rows = []row{{item: &app.Item{Entry: app.Entry{Machine: app.LocalName(), Returns: []app.ReturnCandidate{{Agent: "claude", Profile: "work", Key: "claude@work/missing", Local: true, Status: "missing"}}}}}}
	m.cursor = 0
	m.openReturns()
	_, cmd := m.returnKeys("enter")
	if cmd == nil || m.opts.TargetProfile != "work" || m.opts.TargetSession != "" || !m.opts.NewReplica || m.opts.Fork {
		t.Fatal("missing session was reused", m.opts)
	}
}

func TestUnverifiedReturnReviewsSameBranchWithoutBypassingConflict(t *testing.T) {
	m := newModel(t)
	m.mode = modePlan
	m.plan = &move.Plan{ReviewNewSession: true, Blockers: []string{"unverified account binding"}}
	m.returnTo = &app.ReturnCandidate{Profile: "work", Key: "claude@work/original", Local: true}
	m.sel = row{item: &app.Item{Entry: app.Entry{Machine: app.LocalName()}}}
	m.opts = move.Options{TargetProfile: "work", TargetSession: "claude@work/original", Fork: true, Conflict: move.ConflictKeepBoth}
	var b strings.Builder
	m.viewPlan(&b)
	if !strings.Contains(b.String(), "[N] review a new session on the same lineage branch") || !strings.Contains(b.String(), "does not mean you changed accounts") {
		t.Fatal(b.String())
	}
	m.planning = true
	if _, cmd := m.key("N"); cmd != nil {
		t.Fatal("changed a pending review")
	}
	m.planning = false
	if _, cmd := m.key("N"); cmd == nil || m.opts.TargetProfile != "work" || m.opts.TargetSession != "" || !m.opts.NewReplica || m.opts.Fork || m.opts.Conflict != "" {
		t.Fatalf("incorrect portable review options: %+v", m.opts)
	}
	m.planning = false
	m.plan.ReviewNewSession = false
	if _, cmd := m.key("N"); cmd != nil {
		t.Fatal("offered a resolution absent from the current plan")
	}
}
