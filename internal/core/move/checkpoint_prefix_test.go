package move

import (
	"errors"
	"slices"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func checkpointSegment(texts ...string) ir.Segment {
	var seg ir.Segment
	for i, text := range texts {
		seg.Nodes = append(seg.Nodes, ir.Node{Kind: ir.KindMessage, Actor: ir.User, Text: text, Native: &ir.Native{Anchor: string(rune('a' + i))}})
	}
	ir.Chain(seg.Nodes, "")
	seg.Cursor = ir.Cursor{Head: seg.Nodes[len(seg.Nodes)-1].ID, Offset: int64(len(texts) * 10)}
	return seg
}

func TestCloudCheckpointChangedNativeIDInheritsOnlyExactTaskPrefix(t *testing.T) {
	for _, mode := range []string{"verified", "other-endpoint", "other-binding", "other-agent", "other-line", "changed-anchor", "edited-prefix", "inserted-record"} {
		t.Run(mode, func(t *testing.T) {
			m := lineage.New("checkpoint-family")
			source := lineage.Replica{Endpoint: "cloud:claude-hosted:task", Binding: "task", Key: agent.SessionKey{Agent: "claude", Profile: "task", Session: "old-native-id"}, Line: m.Branch, Location: "claude-hosted"}
			id := m.Upsert(source)
			old := checkpointSegment("first", "second")
			if _, err := m.Observe(id, &old); err != nil {
				t.Fatal(err)
			}
			source.Key.Session = "new-native-id"
			next := checkpointSegment("first", "second", "new cloud work")
			switch mode {
			case "other-endpoint":
				source.Endpoint = "cloud:claude-hosted:other"
			case "other-binding":
				source.Binding = "other"
			case "other-agent":
				source.Key.Agent = "codex"
			case "other-line":
				source.Line = m.Fork("independent", nil)
			case "changed-anchor":
				next.Nodes[0].Native.Anchor = "unrelated"
			case "edited-prefix":
				next.Nodes[0].Text = "edited"
			case "inserted-record":
				next = checkpointSegment("inserted", "first", "second")
			}
			err := seedCheckpointPrefix(m, source, &next)
			if mode == "changed-anchor" || mode == "edited-prefix" || mode == "inserted-record" {
				if !errors.Is(err, agent.ErrDiverged) {
					t.Fatal("rebuilt history rewrite was not refused", err)
				}
				if err = m.Validate(); err != nil {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			newID := m.Upsert(source)
			if _, err := m.Observe(newID, &next); err != nil {
				t.Fatal(err)
			}
			inherited := slices.Equal(next.Nodes[0].Coverage, old.Nodes[0].Coverage)
			if inherited != (mode == "verified") {
				t.Fatal("checkpoint inherited unverifiable history", mode)
			}
			if mode == "verified" && (len(m.Revisions) != 3 || !slices.Equal(next.Nodes[1].Coverage, old.Nodes[1].Coverage) || m.Replica(newID).Location != "claude-hosted") {
				t.Fatal("verified prefix lost coverage or source metadata")
			}
			if err := m.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCloudCheckpointExistingNativeHistoryStillRefusesRewrite(t *testing.T) {
	m := lineage.New("checkpoint-family")
	r := lineage.Replica{Endpoint: "cloud:task", Binding: "task", Key: agent.SessionKey{Agent: "claude", Profile: "task", Session: "native"}, Line: m.Branch}
	id := m.Upsert(r)
	old := checkpointSegment("first", "second")
	if _, err := m.Observe(id, &old); err != nil {
		t.Fatal(err)
	}
	for _, next := range []ir.Segment{checkpointSegment("edited", "second"), checkpointSegment("first")} {
		if err := seedCheckpointPrefix(m, r, &next); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Observe(id, &next); !errors.Is(err, agent.ErrDiverged) {
			t.Fatal("task identity bypassed rewrite/rewind refusal", err)
		}
	}
}
