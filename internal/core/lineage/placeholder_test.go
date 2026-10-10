package lineage

import (
	"bytes"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"testing"
)

func TestFirstReplicaCompletesOnlyEmptyBranchPlaceholder(t *testing.T) {
	empty := New("family")
	complete := empty.Clone()
	id := complete.Upsert(Replica{Endpoint: "A", Location: "A", Key: agent.SessionKey{Agent: "claude", Session: "original"}})
	for _, reverse := range []bool{false, true} {
		left, right := empty.Clone(), complete.Clone()
		if reverse {
			left, right = right, left
		}
		if err := left.Merge(right); err != nil {
			t.Fatal(err)
		}
		if left.branch(left.Branch).Origin != id {
			t.Fatal("origin lost")
		}
		if err := left.Merge(right); err != nil {
			t.Fatal("receipt recovery not idempotent", err)
		}
	}
}
func TestPopulatedOriginsAndForkBoundariesRemainImmutable(t *testing.T) {
	m := New("family")
	a := m.Upsert(Replica{Endpoint: "A", Location: "A", Key: agent.SessionKey{Agent: "claude", Session: "original"}})
	b := m.Upsert(Replica{Endpoint: "B", Location: "B", Key: agent.SessionKey{Agent: "codex", Session: "copy"}})
	if m.branch(m.Branch).Origin != a {
		t.Fatal("test setup")
	}
	for _, kind := range []string{"other-origin", "existing-replicas-without-origin", "changed-parent"} {
		other := m.Clone()
		switch kind {
		case "other-origin":
			other.branch(other.Branch).Origin = b
		case "existing-replicas-without-origin":
			other.branch(other.Branch).Origin = ""
		case "changed-parent":
			other.Branches = append(other.Branches, Branch{ID: "parent"})
			other.branch(other.Branch).Parent = "parent"
			other.branch(other.Branch).Origin = ""
		}
		before := m.Encode()
		if err := m.Merge(other); err == nil {
			t.Fatal("accepted conflict", kind)
		}
		if !bytes.Equal(before, m.Encode()) {
			t.Fatal("failed merge changed original")
		}
	}
}
