package move

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Only the generic module interface is needed: comparison must never invoke an
// agent-specific reader, writer, live checker, or install path.
type comparisonModule struct{ agent.Module }

func (comparisonModule) Spec() agent.Spec { return agent.Spec{ID: "test", Name: "Test agent"} }

func comparisonMessage(anchor, text string, actor ir.Actor) ir.Node {
	return ir.Node{Kind: ir.KindMessage, Actor: actor, Text: text, Native: &ir.Native{Anchor: anchor}}
}

func comparisonSegment(nodes ...ir.Node) ir.Segment {
	ir.Chain(nodes, "")
	seg := ir.Segment{Nodes: nodes, Cursor: ir.Cursor{Offset: int64(len(nodes) * 10)}}
	if len(nodes) > 0 {
		seg.Cursor.Head = nodes[len(nodes)-1].ID
	}
	return seg
}

type comparisonFixture struct {
	p      *Plan
	in     Input
	c      Copy
	source ir.Segment
	target ir.Segment
	st     lineage.State
}

func newComparisonFixture(t *testing.T, sourceExtra, targetExtra []ir.Node, native bool) comparisonFixture {
	t.Helper()
	key := agent.SessionKey{Agent: "test", Profile: "source-profile", Session: "source-session"}
	targetKey := agent.SessionKey{Agent: "test", Profile: "target-profile", Session: "target-session"}
	if native {
		targetKey.Session = key.Session
	}
	srcSide := Side{Machine: &host.Machine{Name: "Source Mac", Facts: host.Facts{Endpoint: "source-machine"}}, Module: comparisonModule{}, Install: agent.Install{Agent: "test", Profile: &agent.RuntimeProfile{ID: key.Profile, Name: "Source label", Binding: "source-binding"}}}
	tgtSide := Side{Machine: &host.Machine{Name: "Destination Mac", Facts: host.Facts{Endpoint: "target-machine"}}, Module: comparisonModule{}, Install: agent.Install{Agent: "test", Profile: &agent.RuntimeProfile{ID: targetKey.Profile, Name: "Destination label", Binding: "target-binding"}}}
	in := Input{Source: srcSide, Target: tgtSide, Session: agent.Summary{Key: key, Title: "Source title"}}
	c := Copy{Summary: agent.Summary{Key: targetKey, Title: "Destination title"}}
	m := lineage.NewNative(srcSide.Machine.Facts.Endpoint, key)
	sourceID := m.Upsert(lineage.Replica{Endpoint: srcSide.Machine.Facts.Endpoint, Binding: srcSide.Install.BindingID(), Key: key})
	targetID := m.Upsert(lineage.Replica{Endpoint: tgtSide.Machine.Facts.Endpoint, Binding: tgtSide.Install.BindingID(), Key: targetKey})
	base := comparisonSegment(comparisonMessage("shared", "common question", ir.User))
	baseState, err := m.Observe(sourceID, &base)
	if err != nil {
		t.Fatal(err)
	}
	baseTarget := comparisonSegment(comparisonMessage("imported", "common question", ir.User))
	if native {
		baseTarget = comparisonSegment(comparisonMessage("shared", "common question", ir.User))
	}
	projection := []ir.Projection{{Anchor: baseTarget.Nodes[0].Native.Anchor, Hash: ir.ContentHash(baseTarget.Nodes[0]), Coverage: base.Nodes[0].Coverage}}
	m.ReplaceProjection(targetID, baseTarget.Cursor, projection, baseState.Heads, nil)
	source := comparisonSegment(append(slicesOfNodes(base.Nodes), sourceExtra...)...)
	target := comparisonSegment(append(slicesOfNodes(baseTarget.Nodes), targetExtra...)...)
	sourceState, err := m.Observe(sourceID, &source)
	if err != nil {
		t.Fatal(err)
	}
	st, err := m.Observe(targetID, &target)
	if err != nil {
		t.Fatal(err)
	}
	p := &Plan{manifest: m, sourceState: sourceState, sourceReplica: sourceID, sourceLine: m.Branch}
	return comparisonFixture{p: p, in: in, c: c, source: source, target: target, st: st}
}

func slicesOfNodes(nodes []ir.Node) []ir.Node { return append([]ir.Node(nil), nodes...) }

func TestComparisonCausalClassification(t *testing.T) {
	for _, tc := range []struct {
		name, want       string
		source, target   bool
		sourceN, targetN int
	}{
		{name: "same", want: "same"},
		{name: "source only", source: true, sourceN: 1, want: "source-only"},
		{name: "destination only", target: true, targetN: 1, want: "destination-only"},
		{name: "diverged", source: true, target: true, sourceN: 1, targetN: 1, want: "diverged"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var source, target []ir.Node
			if tc.source {
				source = []ir.Node{comparisonMessage("source-new", "Source follow up", ir.User)}
				// Opposite clocks must have no effect on the coverage result.
				source[0].Time = time.Unix(1, 0)
			}
			if tc.target {
				target = []ir.Node{comparisonMessage("target-new", "Destination response", ir.Agent)}
				target[0].Time = time.Unix(100000, 0)
			}
			f := newComparisonFixture(t, source, target, false)
			got := BuildComparison(f.p, f.in, f.c, f.source, f.target, f.st, nil)
			if !got.Verified || got.Classification != tc.want || got.SharedRevisions != 1 || got.Source.Revisions != tc.sourceN || got.Destination.Revisions != tc.targetN {
				t.Fatalf("unexpected comparison: %+v", got)
			}
			if got.Source.Counts.Nodes != tc.sourceN || got.Destination.Counts.Nodes != tc.targetN || !got.Source.ExclusiveKnown || !got.Destination.ExclusiveKnown {
				t.Fatalf("unexpected exclusive counts: %+v", got)
			}
			if tc.source && (got.Source.Preview[0].Role != "user" || got.Source.Preview[0].Time.Unix() != 1) {
				t.Fatalf("source preview: %+v", got.Source.Preview)
			}
			if tc.target && got.Destination.Preview[0].Role != "assistant" {
				t.Fatalf("destination preview: %+v", got.Destination.Preview)
			}
		})
	}
}

func TestComparisonIdentityAndNoMutation(t *testing.T) {
	f := newComparisonFixture(t, []ir.Node{comparisonMessage("new", "follow up", ir.User)}, nil, true)
	beforePlan, _ := json.Marshal(f.p)
	beforeManifest := string(f.p.manifest.Encode())
	beforeSource, _ := json.Marshal(f.source)
	beforeTarget, _ := json.Marshal(f.target)
	beforeInput, _ := json.Marshal(f.in)
	got := BuildComparison(f.p, f.in, f.c, f.source, f.target, f.st, nil)
	want := ComparisonIdentity{Agent: "test", AgentName: "Test agent", Profile: "target-profile", ProfileName: "Destination label", Machine: "Destination Mac", MachineID: "target-machine", Title: "Destination title", Key: f.c.Summary.Key}
	if !reflect.DeepEqual(got.Destination.Identity, want) || got.Source.Identity.Key != f.in.Session.Key {
		t.Fatalf("identity mismatch: %+v", got)
	}
	afterPlan, _ := json.Marshal(f.p)
	afterSource, _ := json.Marshal(f.source)
	afterTarget, _ := json.Marshal(f.target)
	afterInput, _ := json.Marshal(f.in)
	if string(beforePlan) != string(afterPlan) || beforeManifest != string(f.p.manifest.Encode()) || string(beforeSource) != string(afterSource) || string(beforeTarget) != string(afterTarget) || string(beforeInput) != string(afterInput) {
		t.Fatal("comparison mutated its planning inputs")
	}
}

func TestComparisonNativeCopyUsesReceiptCoverage(t *testing.T) {
	f := newComparisonFixture(t, []ir.Node{comparisonMessage("new", "only new source work", ir.User)}, nil, true)
	// Legacy/full Reader IR may have content-chain IDs but no logical coverage.
	// The receipt, not these IDs, must establish the shared native copy's history.
	for i := range f.source.Nodes {
		f.source.Nodes[i].Coverage = nil
	}
	for i := range f.target.Nodes {
		f.target.Nodes[i].Coverage = nil
	}
	got := BuildComparison(f.p, f.in, f.c, f.source, f.target, f.st, nil)
	if !got.Verified || got.Classification != "source-only" || got.SharedRevisions != 1 || got.Source.Counts.Nodes != 1 || got.Destination.Counts.Nodes != 0 || got.Source.Preview[0].Text != "only new source work" {
		t.Fatalf("copied native history was counted as new work: %+v", got)
	}
	if f.source.Nodes[0].Coverage != nil || f.target.Nodes[0].Coverage != nil {
		t.Fatal("receipt hydration mutated Reader IR")
	}
}

func TestComparisonUnavailableDoesNotClaimIndependentWork(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		mutate func(*comparisonFixture)
	}{
		{name: "unsupported reader", err: agent.ErrUnsupported},
		{name: "rewritten history", err: agent.ErrDiverged},
		{name: "read error", err: errors.New("private reasoning and /private/path must not leak")},
		{name: "missing manifest", mutate: func(f *comparisonFixture) { f.p.manifest = nil }},
		{name: "missing target state", mutate: func(f *comparisonFixture) { f.st = lineage.State{} }},
		{name: "wrong binding", mutate: func(f *comparisonFixture) { f.in.Target.Install.Profile.Binding = "another binding" }},
		{name: "wrong profile", mutate: func(f *comparisonFixture) { f.in.Target.Install.Profile.ID = "another profile" }},
		{name: "wrong endpoint", mutate: func(f *comparisonFixture) { f.in.Target.Machine.Facts.Endpoint = "another endpoint" }},
		{name: "unproven target coverage", mutate: func(f *comparisonFixture) { f.target.Nodes[0].Coverage = []ir.NodeID{"unknown"} }},
		{name: "legacy copy without projection", mutate: func(f *comparisonFixture) {
			f.p.manifest.ReplaceProjection(f.st.Replica, ir.Cursor{}, nil, nil, nil)
			for i := range f.target.Nodes {
				f.target.Nodes[i].Coverage = nil
			}
			var err error
			f.st, err = f.p.manifest.Observe(f.st.Replica, &f.target)
			if err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newComparisonFixture(t, []ir.Node{comparisonMessage("source-new", "source delta", ir.User)}, []ir.Node{comparisonMessage("target-new", "target delta", ir.Agent)}, true)
			if tc.mutate != nil {
				tc.mutate(&f)
			}
			got := BuildComparison(f.p, f.in, f.c, f.source, f.target, f.st, tc.err)
			body, _ := json.Marshal(got)
			if got.Verified || got.Classification != "unavailable" || got.Source.ExclusiveKnown || got.Destination.ExclusiveKnown || got.Destination.Status != "unavailable" || strings.Contains(string(body), "/private/path") || strings.Contains(string(body), "private reasoning and") {
				t.Fatalf("unavailable evidence became a divergence claim or leaked details: %s", body)
			}
		})
	}
}

func TestComparisonCoalescedFragmentsAndGeneratedNodes(t *testing.T) {
	f := newComparisonFixture(t, []ir.Node{comparisonMessage("new", "exclusive fragment", ir.Agent)}, nil, false)
	covered := append(slicesOfIDs(f.source.Nodes[0].Coverage), f.source.Nodes[1].Coverage...)
	f.source.Nodes = []ir.Node{{Kind: ir.KindMessage, Actor: ir.Agent, Text: "shared fragment\nexclusive fragment", Coverage: covered, Fragments: []ir.Fragment{{Text: "shared fragment", Coverage: f.source.Nodes[0].Coverage}, {Text: "exclusive fragment", Coverage: f.source.Nodes[1].Coverage}}}, {Kind: ir.KindMessage, Actor: ir.User, Text: "generated briefing", Generated: true, Coverage: covered}}
	got := BuildComparison(f.p, f.in, f.c, f.source, f.target, f.st, nil)
	if !got.Verified || got.Source.Counts.Messages != 1 || len(got.Source.Preview) != 1 || got.Source.Preview[0].Text != "exclusive fragment" {
		t.Fatalf("shared/generated text leaked into exclusive preview: %+v", got)
	}
}

func slicesOfIDs(ids []ir.NodeID) []ir.NodeID { return append([]ir.NodeID(nil), ids...) }

func TestComparisonPreviewPrivacyAndCounts(t *testing.T) {
	extra := []ir.Node{
		comparisonMessage("question", "public user text", ir.User),
		comparisonMessage("answer", "public assistant text", ir.Agent),
		{Kind: ir.KindReasoning, Actor: ir.Agent, Text: "SECRET REASONING", Reasoning: &ir.Reasoning{Opaque: true}, Native: &ir.Native{Anchor: "reason", Payload: json.RawMessage(`{"secret":"SECRET PAYLOAD"}`)}},
		{Kind: ir.KindMessage, Actor: ir.Agent, Text: "SECRET MISCLASSIFIED", Reasoning: &ir.Reasoning{}, Native: &ir.Native{Anchor: "misclassified"}},
		{Kind: ir.KindToolCall, Actor: ir.Agent, Tool: &ir.ToolCall{Name: "Edit", Input: json.RawMessage(`{"secret":"SECRET INPUT"}`), Kind: ir.ToolEdit, Edit: &ir.Edit{Path: "/SECRET PATH", New: "SECRET CODE"}}, Native: &ir.Native{Anchor: "tool"}},
		{Kind: ir.KindToolResult, Result: &ir.ToolResult{Output: "SECRET OUTPUT"}, Native: &ir.Native{Anchor: "result"}},
		{Kind: ir.KindPlan, Plan: []ir.PlanEntry{{Content: "SECRET PLAN"}}, Native: &ir.Native{Anchor: "plan"}},
		{Kind: ir.KindToolCall, Tool: &ir.ToolCall{Name: "SECRET THINK TOOL", Kind: ir.ToolThink}, Native: &ir.Native{Anchor: "think"}},
	}
	f := newComparisonFixture(t, extra, nil, false)
	got := BuildComparison(f.p, f.in, f.c, f.source, f.target, f.st, nil)
	body, _ := json.Marshal(got)
	want := ComparisonCounts{Nodes: 8, Messages: 2, UserMessages: 1, AssistantMessages: 1, Tools: 1, Other: 5}
	if !got.Verified || got.Source.Counts != want || len(got.Source.Preview) != 3 || got.Source.Preview[2].Tool != "Edit" || strings.Contains(string(body), "SECRET") {
		t.Fatalf("privacy/count failure: %s", body)
	}
	if got.Source.Truncated || got.Source.PreviewOmitted != 0 {
		t.Fatal("private records should be excluded, not treated as truncated public previews")
	}
}

func TestComparisonHistoricalExclusiveWorkDoesNotInferWritingTime(t *testing.T) {
	old := time.Unix(1, 0)
	var target []ir.Node
	target = append(target, comparisonMessage("historical-question", "Check the earlier migration", ir.User), comparisonMessage("historical-answer", "The earlier migration needs verification", ir.Agent))
	for i := 0; i < 3; i++ {
		target = append(target, ir.Node{Kind: ir.KindToolCall, Tool: &ir.ToolCall{Name: "Bash", Kind: ir.ToolExecute, Input: json.RawMessage(`{"command":"private command"}`)}, Native: &ir.Native{Anchor: string(rune('a' + i))}})
	}
	for i := 0; i < 6; i++ {
		target = append(target, ir.Node{Kind: ir.KindToolResult, Result: &ir.ToolResult{Output: "private output"}, Native: &ir.Native{Anchor: string(rune('d' + i))}})
	}
	for i := range target {
		target[i].Time = old
	}
	f := newComparisonFixture(t, []ir.Node{comparisonMessage("source-work", "Source continuation", ir.User)}, target, false)
	got := BuildComparison(f.p, f.in, f.c, f.source, f.target, f.st, nil)
	if !got.Verified || got.Classification != "diverged" || got.Destination.Counts != (ComparisonCounts{Nodes: 11, Messages: 2, UserMessages: 1, AssistantMessages: 1, Tools: 3, Other: 6}) {
		t.Fatalf("historical exclusive work misclassified: %+v", got)
	}
	if got.Reason != "Each conversation contains work not represented in the other. Append would omit destination work." || len(got.Destination.Preview) != 5 || got.Destination.Preview[0].Text != "Check the earlier migration" {
		t.Fatalf("missing actual safe preview or causal wording: %+v", got)
	}
	for _, p := range got.Destination.Preview[2:] {
		if p.Tool != "Bash" || p.Time == nil || !p.Time.Equal(old) {
			t.Fatalf("historical tool evidence missing: %+v", p)
		}
	}
}

func TestComparisonPreviewBoundsPreserveCounts(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		n          int
		wantShown  int
	}{
		{name: "entry cap", text: "short", n: 75, wantShown: 40},
		{name: "total character cap", text: strings.Repeat("界", 1000), n: 75, wantShown: 20},
		{name: "huge message", text: strings.Repeat("界", 1000000), n: 1, wantShown: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nodes := make([]ir.Node, tc.n)
			for i := range nodes {
				nodes[i] = comparisonMessage(string(rune('a'+i)), tc.text, ir.User)
			}
			side := ComparisonSide{Preview: []ComparisonPreview{}}
			comparisonSummarize(&side, nodes)
			if side.Counts.Messages != tc.n || side.Counts.Nodes != tc.n || len(side.Preview) != tc.wantShown || side.PreviewOmitted != tc.n-tc.wantShown || !side.Truncated {
				t.Fatalf("counts or caps wrong: %+v", side)
			}
			total := 0
			for _, p := range side.Preview {
				n := utf8.RuneCountInString(p.Text)
				total += n
				if !utf8.ValidString(p.Text) || n > comparisonTextLimit {
					t.Fatalf("invalid or oversized text: %d", n)
				}
			}
			if total > comparisonTotalLimit {
				t.Fatalf("total preview cap exceeded: %d", total)
			}
		})
	}
	text, truncated := comparisonText("safe\x1b\x00\u202E"+strings.Repeat("t", 200), comparisonToolLimit)
	if !truncated || utf8.RuneCountInString(text) != comparisonToolLimit || strings.ContainsAny(text, "\x1b\x00\u202E") {
		t.Fatal("tool-name cap or control filtering failed")
	}
}

func TestComparisonSavedHistoryDoesNotGrantCoverage(t *testing.T) {
	f := newComparisonFixture(t, []ir.Node{comparisonMessage("incoming", "incoming saved response", ir.Agent)}, []ir.Node{comparisonMessage("historical", "previously omitted saved response", ir.Agent)}, false)
	f.target.Nodes = append(f.target.Nodes, ir.Node{Kind: ir.KindReasoning, Text: "private reasoning sentinel", Reasoning: &ir.Reasoning{Opaque: true}}, ir.Node{Kind: ir.KindToolResult, Result: &ir.ToolResult{Output: "private result sentinel"}})
	before := f.p.manifest.Encode()
	got := BuildComparison(f.p, f.in, f.c, f.source, f.target, lineage.State{}, agent.ErrDiverged)
	for _, side := range []ComparisonSide{got.Source, got.Destination} {
		if got.Verified || got.Classification != "unavailable" || side.ExclusiveKnown || side.Revisions != 0 || side.Counts != (ComparisonCounts{}) || side.PreviewBasis != "saved-history" || len(side.Preview) == 0 {
			t.Fatalf("saved excerpts became causal proof: %+v", got)
		}
	}
	body, _ := json.Marshal(got)
	if !strings.Contains(string(body), "previously omitted saved response") || strings.Contains(string(body), "private reasoning sentinel") || strings.Contains(string(body), "private result sentinel") {
		t.Fatalf("saved history privacy: %s", body)
	}
	if !bytes.Equal(before, f.p.manifest.Encode()) {
		t.Fatal("preview changed lineage")
	}
}
