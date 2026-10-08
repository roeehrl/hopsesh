package move

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// CheckpointHandoff identifies an immutable owner-reviewed provenance capsule.
// The surrounding Input.Lineage is the verified saved receipt, never a peer's
// claimed ancestry. Native task ID changes do not create new association hops.
type CheckpointHandoff struct {
	Task         string            `json:"task"`
	Operation    string            `json:"operation"`
	CloudReplica lineage.ReplicaID `json:"cloudReplica"`
	Brief        string            `json:"brief"`
	Fork         bool              `json:"fork"`
	Time         time.Time         `json:"time"`
}

func seedCheckpointHandoff(m *lineage.Manifest, source lineage.Replica, seg *ir.Segment, origin CheckpointHandoff) error {
	if origin.Task == "" || source.Binding != origin.Task || !strings.HasPrefix(source.Endpoint, "cloud:") || origin.Time.IsZero() {
		return errors.New("invalid checkpoint handoff binding")
	}
	var handoff *lineage.Hop
	for i := range m.Hops {
		if m.Hops[i].ID == origin.Operation {
			handoff = &m.Hops[i]
			break
		}
	}
	if handoff == nil || handoff.Kind != lineage.HopHandoff || handoff.To != origin.CloudReplica {
		return errors.New("checkpoint has no matching saved handoff")
	}
	for _, undo := range m.Compensations {
		if undo.Operation == handoff.ID {
			return errors.New("checkpoint handoff was undone")
		}
	}
	cloud := m.Replica(handoff.To)
	if cloud.Key.Agent != source.Key.Agent {
		return errors.New("checkpoint handoff agent changed")
	}
	operation := "cloud-task-binding/" + origin.Task
	for _, h := range m.Hops {
		if h.ID == operation {
			target := m.Replica(h.To)
			if h.Kind != lineage.HopIdentity || h.From != cloud.ID || target.Endpoint != source.Endpoint || target.Binding != source.Binding || target.Key.Agent != source.Key.Agent || h.Fork != origin.Fork || origin.Fork != (cloud.Line != target.Line) || !checkpointTaskDescendant(m, source.Line, target) {
				return errors.New("checkpoint task is associated with a different handoff")
			}
			return nil
		}
	}
	if origin.Fork != (cloud.Line != source.Line) {
		return errors.New("checkpoint handoff fork boundary changed")
	}
	// Verify representation against a disposable clone. The vendor's outer task
	// replica must not acquire a made-up native transcript identity or projection.
	proof := m.Clone()
	if err := seedCloudPrefix(proof, cloud.ID, seg, origin.Brief); err != nil {
		return err
	}
	verified, _ := proof.LatestState(cloud.ID)
	brief := len(seg.Nodes) > 0 && origin.Brief != "" && seg.Nodes[0].Actor == ir.User && seg.Nodes[0].Kind == ir.KindMessage && strings.TrimSpace(seg.Nodes[0].Text) == strings.TrimSpace(origin.Brief)
	prior := m.State(handoff.Source)
	complete := len(prior.Projection) > 0 && len(seg.Nodes) >= len(prior.Projection)
	if complete {
		for i, p := range prior.Projection {
			n := seg.Nodes[i]
			if n.Native == nil || n.Native.Anchor != p.Anchor || ir.ContentHash(n) != p.Hash {
				complete = false
				break
			}
		}
	}
	if !brief && !complete {
		return errors.New("cloud checkpoint does not contain the saved handoff briefing or its complete verified native prefix; keep this task independent")
	}
	if len(verified.Projection) == 0 {
		return errors.New("saved handoff has no verifiable checkpoint representation")
	}
	old := m.State(handoff.Target)
	if !slices.Equal(old.Heads, verified.Heads) {
		return errors.New("cloud handoff receipt advanced beyond the reviewed checkpoint")
	}
	id := m.Upsert(source)
	m.Deliver(id, ir.Cursor{}, verified.Projection, old.Heads, old.Loss)
	if err := m.AppendHop(lineage.Hop{ID: operation, Time: origin.Time, From: cloud.ID, To: id, Source: handoff.Target, Kind: lineage.HopIdentity, Fork: origin.Fork, Fidelity: "verified saved handoff task association"}); err != nil {
		return fmt.Errorf("associate cloud identity: %w", err)
	}
	return m.Validate()
}

// A reviewed native-history rewrite can create descendants of the initially
// linked task branch. Only branches originating from that same cloud task may
// continue it; local checkpoint forks and other tasks cannot select its ledger.
func checkpointTaskDescendant(m *lineage.Manifest, line string, task lineage.Replica) bool {
	for range len(m.Branches) + 1 {
		if line == task.Line {
			return true
		}
		parent := ""
		for _, branch := range m.Branches {
			if branch.ID != line {
				continue
			}
			origin := m.Replica(branch.Origin)
			if origin.Endpoint != task.Endpoint || origin.Binding != task.Binding || origin.Key.Agent != task.Key.Agent || origin.Key.Profile != task.Key.Profile {
				return false
			}
			parent = branch.Parent
			break
		}
		if parent == "" {
			return false
		}
		line = parent
	}
	return false
}
