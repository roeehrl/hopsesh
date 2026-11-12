package move

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Comparison describes conversation coverage, never filesystem or code conflicts.
// Only verified coverage can establish independent work. An unavailable comparison
// must not be presented as evidence that both sessions changed.
type Comparison struct {
	Classification  string         `json:"classification"`
	Verified        bool           `json:"verified"`
	Reason          string         `json:"reason"`
	SharedRevisions int            `json:"sharedRevisions"`
	Source          ComparisonSide `json:"source"`
	Destination     ComparisonSide `json:"destination"`
}

// ComparisonIdentity keeps stable identity keys separate from display labels.
type ComparisonIdentity struct {
	Agent       agent.ID         `json:"agent"`
	AgentName   string           `json:"agentName"`
	Profile     string           `json:"profile"`
	ProfileName string           `json:"profileName"`
	Machine     string           `json:"machine"`
	MachineID   string           `json:"machineId"`
	Title       string           `json:"title"`
	Key         agent.SessionKey `json:"key"`
}

// ComparisonSide counts exclusive native IR nodes; Revisions counts exclusive
// logical revisions, which may be represented by fewer coalesced native nodes.
// Counts and Revisions are meaningful only when ExclusiveKnown is true.
type ComparisonSide struct {
	Identity       ComparisonIdentity  `json:"identity"`
	Status         string              `json:"status"`
	Reason         string              `json:"reason,omitempty"`
	ExclusiveKnown bool                `json:"exclusiveKnown"`
	Revisions      int                 `json:"revisions"`
	Counts         ComparisonCounts    `json:"counts"`
	Preview        []ComparisonPreview `json:"preview"`
	Truncated      bool                `json:"truncated"`
	PreviewOmitted int                 `json:"previewOmitted"`
	PreviewBasis   string              `json:"previewBasis,omitempty"` // saved-history when exclusivity is unknown
}

// ComparisonCounts are exact even when previews are shortened or omitted.
// Other includes private/opaque records without exposing their contents.
type ComparisonCounts struct {
	Nodes             int `json:"nodes"`
	Messages          int `json:"messages"`
	UserMessages      int `json:"userMessages"`
	AssistantMessages int `json:"assistantMessages"`
	Tools             int `json:"tools"`
	Other             int `json:"other"`
}

// ComparisonPreview is an allowlist: ordinary user/assistant text, tool names,
// and timestamps only. No native payload, reasoning, tool input/output or paths
// are exported. Role is "user" or "assistant", independent of the IR actor name.
type ComparisonPreview struct {
	Kind      ir.Kind    `json:"kind"`
	Role      string     `json:"role,omitempty"`
	Text      string     `json:"text,omitempty"`
	Tool      string     `json:"tool,omitempty"`
	Time      *time.Time `json:"time,omitempty"`
	Truncated bool       `json:"truncated"`
}

const (
	comparisonPreviewLimit = 40
	comparisonTextLimit    = 600
	comparisonToolLimit    = 120
	comparisonTotalLimit   = 12000
)

// BuildComparison consumes the full segments already read by planning. Call it
// immediately after targetState, before missingNodes or forkLine changes the
// planning view. It performs no I/O, writes, stops, or changes to Plan/Input/IR.
// Pass targetState's error through even if it returned a partial target segment.
// Without a uniquely selected Copy the caller should leave its comparison nil.
func BuildComparison(p *Plan, in Input, c Copy, sourceSegment, targetSegment ir.Segment, st lineage.State, targetErr error) *Comparison {
	out := &Comparison{
		Classification: "unavailable",
		Reason:         "Conversation comparison evidence is unavailable; independent work is not established.",
		Source:         comparisonSide(in.Source, in.Session),
		Destination:    comparisonSide(in.Target, c.Summary),
	}
	if p == nil || p.manifest == nil || p.sourceState.ID == "" {
		out.Source.Reason = "Source causal coverage is unavailable."
		out.Destination.Reason = "Shared causal coverage cannot be verified."
		return out
	}
	m := p.manifest
	if !comparisonReplica(m, p.sourceState, in.Source, in.Session.Key) {
		out.Source.Reason = "Source identity does not match its causal receipt."
		return out
	}
	sourceCovered := m.Covered(p.sourceState.Heads)
	source, ok := comparisonNodes(sourceSegment.Nodes, p.sourceState, sourceCovered)
	if !ok {
		out.Source.Reason = "Source records cannot be matched to verified causal coverage."
		return out
	}
	out.Source.Status = "verified"
	if targetErr != nil {
		out.Destination.Reason = comparisonUnavailableReason(targetErr)
		out.Source.Reason = "Exclusive counts require verified destination evidence."
		if errors.Is(targetErr, agent.ErrDiverged) && len(targetSegment.Nodes) > 0 {
			out.Reason = "Saved messages can be inspected, but their relationship to the earlier receipt could not be verified. An older move may have omitted existing history."
			comparisonSavedPreview(&out.Source, sourceSegment.Nodes)
			comparisonSavedPreview(&out.Destination, targetSegment.Nodes)
		}
		return out
	}
	if st.ID == "" || !comparisonReplica(m, st, in.Target, c.Summary.Key) {
		out.Destination.Reason = "Destination identity or causal receipt is unavailable."
		return out
	}
	if m.Replica(st.Replica).Line != m.Replica(p.sourceState.Replica).Line {
		out.Destination.Reason = "The sessions do not have verified coverage on the same lineage branch."
		return out
	}
	targetCovered := m.Covered(st.Heads)
	target, ok := comparisonNodes(targetSegment.Nodes, st, targetCovered)
	if !ok {
		out.Destination.Reason = "Destination records cannot be matched to verified causal coverage."
		return out
	}
	// Older native backups can have been observed as newly authored records
	// instead of retaining the source's projections. Matching native anchors and
	// content with disjoint logical origins is missing inheritance evidence, not
	// proof of independent work. Do not invent coverage from identical text/IDs.
	if comparisonUnprovenNativeCopy(in.Session.Key, c.Summary.Key, source, target, sourceCovered, targetCovered) {
		out.Destination.Reason = "Native copy inheritance is unavailable; shared records have unverified causal origins."
		return out
	}
	for id := range sourceCovered {
		if targetCovered[id] {
			out.SharedRevisions++
		} else {
			out.Source.Revisions++
		}
	}
	for id := range targetCovered {
		if !sourceCovered[id] {
			out.Destination.Revisions++
		}
	}
	out.Source.Status, out.Destination.Status = "verified", "verified"
	out.Source.ExclusiveKnown, out.Destination.ExclusiveKnown = true, true
	comparisonSummarize(&out.Source, missingNodes(source, targetCovered))
	comparisonSummarize(&out.Destination, missingNodes(target, sourceCovered))
	out.Verified = true
	switch {
	case out.Source.Revisions > 0 && out.Destination.Revisions > 0:
		out.Classification = "diverged"
		out.Reason = "Each conversation contains work not represented in the other. Append would omit destination work."
	case out.Source.Revisions > 0:
		out.Classification = "source-only"
		out.Reason = "The source contains conversation work not represented in the destination."
	case out.Destination.Revisions > 0:
		out.Classification = "destination-only"
		out.Reason = "The destination contains conversation work not represented in the source. Append would omit destination work."
	default:
		out.Classification = "same"
		out.Reason = "Both sessions cover the same verified conversation revisions."
	}
	return out
}

// A reader can successfully expose saved text while an old receipt cannot verify
// its ancestry (for example after a reader fix reveals previously omitted history).
// Show bounded recent saved messages for inspection, never as exclusive revisions.
func comparisonSavedPreview(side *ComparisonSide, nodes []ir.Node) {
	var recent []ir.Node
	eligible := 0
	for i := len(nodes) - 1; i >= 0; i-- {
		n := nodes[i]
		if n.Generated || n.Reasoning != nil || n.Kind == ir.KindReasoning {
			continue
		}
		if !(n.Kind == ir.KindMessage && (n.Actor == ir.User || n.Actor == ir.Agent) || n.Kind == ir.KindToolCall && n.Tool != nil && n.Tool.Kind != ir.ToolThink) {
			continue
		}
		eligible++
		if len(recent) < comparisonPreviewLimit {
			recent = append(recent, n)
		}
	}
	slices.Reverse(recent)
	comparisonSummarize(side, recent)
	side.Counts = ComparisonCounts{}
	side.PreviewBasis = "saved-history"
	side.PreviewOmitted += eligible - len(recent)
	side.Truncated = side.Truncated || side.PreviewOmitted > 0
}

func comparisonSide(side Side, s agent.Summary) ComparisonSide {
	id := ComparisonIdentity{Agent: s.Key.Agent, Profile: s.Key.Profile, Key: s.Key, Title: s.Title}
	if side.Module != nil {
		id.AgentName = side.Module.Spec().Name
	}
	if side.Machine != nil {
		id.Machine, id.MachineID = side.Machine.Name, side.Machine.Facts.Endpoint
	}
	if side.Install.Profile != nil {
		id.ProfileName = side.Install.Profile.Name
	}
	return ComparisonSide{Identity: id, Status: "unavailable", Preview: []ComparisonPreview{}}
}

func comparisonReplica(m *lineage.Manifest, st lineage.State, side Side, key agent.SessionKey) bool {
	if side.Machine == nil || side.Module == nil || side.Machine.Facts.Endpoint == "" || key.Agent != side.Module.Spec().ID || key.Profile != side.Install.ProfileID() {
		return false
	}
	r := m.Replica(st.Replica)
	current, ok := m.LatestState(st.Replica)
	return ok && current.ID == st.ID && r.Key == key && r.Endpoint == side.Machine.Facts.Endpoint && r.Binding == side.Install.BindingID()
}

// comparisonNodes also handles Reader IR without Coverage (including native
// copies): only exact receipt anchor/hash matches restore its logical origins.
// Never use IR content-chain IDs or timestamps as a substitute for coverage.
func comparisonNodes(nodes []ir.Node, st lineage.State, covered map[ir.NodeID]bool) ([]ir.Node, bool) {
	ps := make(map[string]ir.Projection, len(st.Projection))
	for _, p := range st.Projection {
		ps[p.Anchor] = p
	}
	out := slices.Clone(nodes)
	for i := range out {
		n := &out[i]
		if len(n.Coverage) == 0 {
			anchor := fmt.Sprintf("node:%d", i)
			if n.Native != nil && n.Native.Anchor != "" {
				anchor = n.Native.Anchor
			}
			p, ok := ps[anchor]
			if !ok || p.Hash != ir.ContentHash(*n) {
				return nil, false
			}
			n.Coverage, n.Fragments, n.Generated = p.Coverage, p.Fragments, p.Generated
		}
		if len(n.Coverage) == 0 && !n.Generated {
			return nil, false
		}
		for _, id := range n.Coverage {
			if !covered[id] {
				return nil, false
			}
		}
		for _, f := range n.Fragments {
			if len(f.Coverage) == 0 || !lineage.Subset(comparisonCoverage(f.Coverage), comparisonCoverage(n.Coverage)) {
				return nil, false
			}
		}
	}
	return out, true
}

func comparisonCoverage(ids []ir.NodeID) map[ir.NodeID]bool {
	out := make(map[ir.NodeID]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

func comparisonUnprovenNativeCopy(a, b agent.SessionKey, source, target []ir.Node, sourceCovered, targetCovered map[ir.NodeID]bool) bool {
	if a.Agent != b.Agent || a.Session != b.Session {
		return false
	}
	by := map[string]ir.Node{}
	for _, n := range source {
		if n.Native != nil && n.Native.Anchor != "" && !n.Generated {
			by[n.Native.Anchor] = n
		}
	}
	for _, n := range target {
		if n.Native == nil || n.Generated || len(n.Coverage) == 0 {
			continue
		}
		s, ok := by[n.Native.Anchor]
		if ok && !lineage.Subset(comparisonCoverage(n.Coverage), sourceCovered) && !lineage.Subset(comparisonCoverage(s.Coverage), targetCovered) && ir.ContentHash(s) == ir.ContentHash(n) {
			return true
		}
	}
	return false
}

func comparisonUnavailableReason(err error) string {
	// Module errors can embed paths, payloads or private text. Export stable
	// categories, not raw error strings, in this safe preview DTO.
	switch {
	case errors.Is(err, agent.ErrUnsupported):
		return "The destination module does not support reading conversation evidence."
	case errors.Is(err, agent.ErrDiverged):
		return "Destination history no longer matches its causal receipt; exclusive work cannot be verified."
	default:
		return "Destination conversation or causal evidence could not be verified."
	}
}

func comparisonSummarize(side *ComparisonSide, nodes []ir.Node) {
	remaining := comparisonTotalLimit
	for _, n := range nodes {
		if n.Generated {
			continue
		}
		side.Counts.Nodes++
		p := ComparisonPreview{Kind: n.Kind}
		limit := comparisonTextLimit
		switch {
		case n.Reasoning != nil || n.Kind == ir.KindReasoning:
			side.Counts.Other++
			continue
		case n.Kind == ir.KindMessage && (n.Actor == ir.User || n.Actor == ir.Agent):
			side.Counts.Messages++
			if n.Actor == ir.User {
				side.Counts.UserMessages++
				p.Role = "user"
			} else {
				side.Counts.AssistantMessages++
				p.Role = "assistant"
			}
		case n.Kind == ir.KindToolCall && n.Tool != nil && n.Tool.Kind != ir.ToolThink:
			side.Counts.Tools++
			limit = comparisonToolLimit
		default:
			side.Counts.Other++
			continue
		}
		if len(side.Preview) >= comparisonPreviewLimit || remaining == 0 {
			side.PreviewOmitted++
			side.Truncated = true
			continue
		}
		text := n.Text
		if p.Kind == ir.KindToolCall {
			text = n.Tool.Name
		}
		text, p.Truncated = comparisonText(text, min(limit, remaining))
		remaining -= len([]rune(text))
		if p.Kind == ir.KindToolCall {
			p.Tool = text
		} else {
			p.Text = text
		}
		if !n.Time.IsZero() {
			t := n.Time
			p.Time = &t
		}
		side.Truncated = side.Truncated || p.Truncated
		side.Preview = append(side.Preview, p)
	}
}

// Iterate only to the cap, even for enormous messages; do not allocate a rune
// slice proportional to a session. Controls and bidi formatting are excluded.
func comparisonText(s string, limit int) (string, bool) {
	var out strings.Builder
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' || unicode.Is(unicode.Cf, r) {
			continue
		}
		if n == limit {
			return out.String(), true
		}
		out.WriteRune(r)
		n++
	}
	return out.String(), false
}
