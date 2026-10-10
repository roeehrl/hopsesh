package cli

import (
	"bytes"
	"strings"
	"testing"
	"unicode"

	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func cliComparisonFixture() *move.Comparison {
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

func TestCLIComparisonShowsActualUniqueWorkAndIdentity(t *testing.T) {
	var out bytes.Buffer
	r := &run{out: &out}
	r.renderPlan(&move.Plan{Kind: move.KindContinue, Agent: "Target Agent", Conflict: "independent work", Continue: &move.ContinuePlan{Comparison: cliComparisonFixture()}})
	for _, want := range []string{"8 shared revisions", "Source Agent · Work account on laptop", "Source task", "source-agent@work/source", "Target Agent · Personal account on studio", "Destination task", "target-agent@personal/original", "unique: 4 revisions · 5 records · 3 messages (1 user, 2 assistant) · 1 tools · 1 other", "user: Source request", "assistant: Source reply", "assistant: Destination independently fixed the bug", "excerpts shortened or omitted", "add --keep-both to review a separate Target Agent session; both originals preserved", "changes neither original"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q:\n%s", want, out.String())
		}
	}
	for _, absent := range []string{"Omitted third", "private-tool-name", "--replace"} {
		if strings.Contains(out.String(), absent) {
			t.Fatalf("unexpected %q in %s", absent, out.String())
		}
	}
}

func TestCLIComparisonUnavailableNeverInventsUniqueCounts(t *testing.T) {
	c := cliComparisonFixture()
	c.Verified, c.Classification, c.Reason = false, "unavailable", "Independent work is not established."
	c.Source.Reason, c.Destination.Reason = "Exclusive counts require verified destination evidence.", "Destination history could not be verified."
	var out bytes.Buffer
	(&run{out: &out}).renderPlan(&move.Plan{Kind: move.KindContinue, Agent: "Target Agent", Conflict: "unverifiable work", Continue: &move.ContinuePlan{Comparison: c}})
	for _, want := range []string{"comparison unavailable", "Independent work is not established", "unique work unknown", "Destination history could not be verified", "target-agent@personal/original"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %s", want, out.String())
		}
	}
	for _, absent := range []string{"unique:", "Source request", "Destination independently", "both copies changed", "8 shared"} {
		if strings.Contains(out.String(), absent) {
			t.Fatalf("unverified evidence displayed as fact: %q", absent)
		}
	}
}

func TestCLIComparisonBoundsAndSanitizesTerminalText(t *testing.T) {
	c := cliComparisonFixture()
	c.Source.Identity.Title = "Unsafe\x1b\x07\r\n\u202etitle"
	c.Source.Preview[1].Text = "safe\x1b\x07\r\n\t\u202e text " + strings.Repeat("界", 1000)
	var out bytes.Buffer
	(&run{out: &out}).renderComparison(c)
	for _, r := range out.String() {
		if unicode.IsControl(r) && r != '\n' || unicode.Is(unicode.Cf, r) {
			t.Fatalf("terminal control leaked: %U", r)
		}
	}
	if !strings.Contains(out.String(), "user: safe text") || !strings.Contains(out.String(), "…") || strings.Contains(out.String(), strings.Repeat("界", 240)) {
		t.Fatal("preview not sanitized and bounded", out.String())
	}
}

func TestCLIExplicitSeparatePlanNamesConfirmedOutcome(t *testing.T) {
	var out bytes.Buffer
	(&run{out: &out}).renderPlan(&move.Plan{Kind: move.KindContinue, Agent: "Target Agent", Continue: &move.ContinuePlan{Comparison: cliComparisonFixture()}, Options: move.Options{Conflict: move.ConflictKeepBoth}})
	if !strings.Contains(out.String(), "create a separate Target Agent session; both originals preserved") || !strings.Contains(out.String(), "Destination-only work is not combined") {
		t.Fatal(out.String())
	}
}

func TestCLIComparisonUnverifiedSavedHistoryIsInspectable(t *testing.T) {
	c := cliComparisonFixture()
	c.Verified = false
	c.Source.PreviewBasis, c.Destination.PreviewBasis = "saved-history", "saved-history"
	var out bytes.Buffer
	(&run{out: &out}).renderComparison(c)
	for _, want := range []string{"Recent saved messages; relationship unverified", "user: Source request", "assistant: Destination independently fixed the bug", "unique work remains unknown"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q: %s", want, out.String())
		}
	}
	for _, absent := range []string{"unique:", "counts above are exact", "8 shared"} {
		if strings.Contains(out.String(), absent) {
			t.Fatalf("saved excerpts grant causal proof: %s", out.String())
		}
	}
}
