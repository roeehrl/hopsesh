package tui

import (
	"strings"
	"testing"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func tuiComparisonFixture() *move.Comparison {
	return &move.Comparison{
		Verified: true, Classification: "independent", Reason: "Both sessions contain verified independent conversation work.", SharedRevisions: 8,
		Source: move.ComparisonSide{
			Identity:       move.ComparisonIdentity{Agent: "source-agent", AgentName: "Source Agent", Profile: "work", ProfileName: "Work account", Machine: "laptop", Title: "Source task", Key: agent.SessionKey{Agent: "source-agent", Profile: "work", Session: "source"}},
			ExclusiveKnown: true, Revisions: 4, Counts: move.ComparisonCounts{Nodes: 5, Messages: 3, UserMessages: 1, AssistantMessages: 2, Tools: 1, Other: 1},
			Preview: []move.ComparisonPreview{{Kind: ir.KindToolCall, Tool: "private-tool-name"}, {Kind: ir.KindMessage, Role: "user", Text: "Source request"}, {Kind: ir.KindMessage, Role: "assistant", Text: "Source reply"}, {Kind: ir.KindMessage, Role: "assistant", Text: "Omitted third source message"}},
		},
		Destination: move.ComparisonSide{
			Identity:       move.ComparisonIdentity{Agent: "target-agent", AgentName: "Target Agent", Profile: "personal", ProfileName: "Personal account", Machine: "studio", Title: "Destination task", Key: agent.SessionKey{Agent: "target-agent", Profile: "personal", Session: "original"}},
			ExclusiveKnown: true, Revisions: 1, Counts: move.ComparisonCounts{Nodes: 1, Messages: 1, AssistantMessages: 1},
			Preview: []move.ComparisonPreview{{Kind: ir.KindMessage, Role: "assistant", Text: "Destination independently fixed the bug"}},
		},
	}
}

func TestTUIComparisonShowsActualUniqueWorkAndIdentity(t *testing.T) {
	m := &model{width: 120, plan: &move.Plan{Kind: move.KindContinue, Agent: "Target Agent", Conflict: "independent work", Continue: &move.ContinuePlan{Comparison: tuiComparisonFixture()}}}
	var b strings.Builder
	m.viewPlan(&b)
	for _, want := range []string{"8 shared revisions", "Source Agent · Work account on laptop", "Source task", "source-agent@work/source", "Target Agent · Personal account on studio", "Destination task", "target-agent@personal/original", "unique: 4 revisions · 5 records · 3 messages (1 user, 2 assistant) · 1 tools · 1 other", "user: Source request", "assistant: Source reply", "assistant: Destination independently fixed the bug", "excerpts shortened or omitted", "[B] review separate Target Agent session; both originals preserved", "esc: cancel; changes neither original"} {
		if !strings.Contains(b.String(), want) {
			t.Fatalf("missing %q:\n%s", want, b.String())
		}
	}
	for _, absent := range []string{"Omitted third", "private-tool-name", "[R] replace", "both copies changed"} {
		if strings.Contains(b.String(), absent) {
			t.Fatalf("unexpected %q in %s", absent, b.String())
		}
	}
}

func TestTUIComparisonUnavailableNeverInventsUniqueCounts(t *testing.T) {
	c := tuiComparisonFixture()
	c.Verified, c.Classification, c.Reason = false, "unavailable", "Independent work is not established."
	c.Source.Reason, c.Destination.Reason = "Exclusive counts require verified destination evidence.", "Destination history could not be verified."
	m := &model{width: 120, plan: &move.Plan{Kind: move.KindContinue, Agent: "Target Agent", Conflict: "unverifiable work", Continue: &move.ContinuePlan{Comparison: c}}}
	var b strings.Builder
	m.viewPlan(&b)
	for _, want := range []string{"Comparison unavailable", "Independent work is not established", "unique work unknown", "Destination history could not be verified", "target-agent@personal/original"} {
		if !strings.Contains(b.String(), want) {
			t.Fatalf("missing %q in %s", want, b.String())
		}
	}
	for _, absent := range []string{"unique:", "Source request", "Destination independently", "both copies changed", "8 shared", "[R]"} {
		if strings.Contains(b.String(), absent) {
			t.Fatalf("unverified evidence displayed as fact: %q", absent)
		}
	}
}

func TestTUIComparisonBoundsAndSanitizesTerminalText(t *testing.T) {
	c := tuiComparisonFixture()
	c.Source.Identity.Title = "Unsafe\x1b\x07\r\n\u202etitle"
	c.Source.Preview[1].Text = "safe\x1b\x07\r\n\t\u202e text " + strings.Repeat("界", 1000)
	var b strings.Builder
	(&model{width: 80}).viewComparison(&b, c)
	for _, r := range b.String() {
		if unicode.IsControl(r) && r != '\n' || unicode.Is(unicode.Cf, r) {
			t.Fatalf("terminal control leaked: %U", r)
		}
	}
	if !strings.Contains(b.String(), "user: safe text") || !strings.Contains(b.String(), "…") || strings.Contains(b.String(), strings.Repeat("界", 62)) {
		t.Fatal("preview not sanitized and bounded", b.String())
	}
}

func TestTUIComparisonReviewKeepsChoiceAndCancelVisibleWhileScrolling(t *testing.T) {
	for _, width := range []int{40, 80, 120} {
		m := &model{width: width, height: 24, mode: modePlan, plan: &move.Plan{Kind: move.KindContinue, Agent: "Target Agent", Conflict: "independent work", Continue: &move.ContinuePlan{Comparison: tuiComparisonFixture()}, Blockers: []string{"choose --keep-both"}}}
		var all strings.Builder
		for _, key := range []string{"home", "pgdown", "end"} {
			m.key(key)
			view := ansi.Strip(m.View().Content)
			all.WriteString(view)
			if lines := strings.Count(strings.TrimSuffix(view, "\n"), "\n") + 1; lines > m.height {
				t.Fatalf("width %d: %d rows exceed height %d:\n%s", width, lines, m.height, view)
			}
			flat := strings.Join(strings.Fields(view), " ")
			for _, want := range []string{"[B] review separate Target Agent session; both originals preserved", "esc: cancel; changes neither original", "scroll review"} {
				if !strings.Contains(flat, want) {
					t.Fatalf("width %d: footer lost %q:\n%s", width, want, view)
				}
			}
		}
		// Both compared sessions can be reached even on a narrow terminal.
		for _, want := range []string{"source-agent@work/source", "target-agent@personal/original"} {
			if !strings.Contains(all.String(), want) {
				t.Fatalf("width %d: identity unreachable: %s", width, want)
			}
		}
	}
}

func TestTUIEscapeDiscardsPendingReviewAndLaterCompletion(t *testing.T) {
	for _, mode := range []mode{modePlan, modeReturns} {
		m := &model{mode: mode, planning: true, plan: &move.Plan{Kind: move.KindContinue}, opts: move.Options{Conflict: move.ConflictKeepBoth}}
		cmd := m.trackPlan(func() tea.Msg { return planDone{plan: &move.Plan{Kind: move.KindContinue}} })
		if _, next := m.key("esc"); next != nil || m.mode != modeBrowse || m.planning {
			t.Fatal("escape did not cancel pending review")
		}
		m.Update(cmd())
		if m.mode != modeBrowse || m.opts.Conflict != "" {
			t.Fatal("canceled review reopened after planning finished")
		}
	}
}

func TestTUIConflictCannotConfirmWithoutReviewedExplicitChoice(t *testing.T) {
	m := &model{mode: modePlan, plan: &move.Plan{Kind: move.KindContinue, Conflict: "independent work"}}
	if _, cmd := m.key("enter"); cmd != nil || m.mode != modePlan {
		t.Fatal("unselected conflict applied")
	}
	m.opts.Conflict = move.ConflictKeepBoth
	if _, cmd := m.key("enter"); cmd != nil || m.mode != modePlan {
		t.Fatal("unreviewed choice applied")
	}
}
