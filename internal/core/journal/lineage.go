package journal

import (
	"fmt"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

type lineageUndo struct {
	machine, path string
	graph         *lineage.Manifest
}

func (j *Journal) captureLineage(r Reach) []lineageUndo {
	var out []lineageUndo
	seen := map[string]bool{}
	for _, e := range j.Entries {
		if !strings.HasSuffix(e.Path, lineage.Suffix) || seen[e.Machine+"\x00"+e.Path] {
			continue
		}
		seen[e.Machine+"\x00"+e.Path] = true
		fsys, err := r.fs(e.Machine)
		if err != nil {
			continue
		}
		m, err := lineage.Read(fsys, strings.TrimSuffix(e.Path, lineage.Suffix))
		if err != nil || m == nil {
			continue
		}
		out = append(out, lineageUndo{e.Machine, e.Path, m})
	}
	return out
}
func (j *Journal) compensateLineage(r Reach, captured []lineageUndo) error {
	operation := j.TransferID
	if operation == "" {
		operation = j.ID
	}
	for _, saved := range captured {
		m := saved.graph
		var hop *lineage.Hop
		for i := range m.Hops {
			if m.Hops[i].ID == operation {
				hop = &m.Hops[i]
				break
			}
		}
		if hop == nil {
			continue
		}
		fsys, err := r.fs(saved.machine)
		if err != nil {
			return err
		}
		native := strings.TrimSuffix(saved.path, lineage.Suffix)
		if _, err = fsys.Stat(native); err != nil {
			continue
		}
		if restored, e := lineage.Read(fsys, native); e == nil && restored != nil {
			if err = m.Merge(restored); err != nil {
				return err
			}
		}
		for _, id := range []lineage.ReplicaID{hop.From, hop.To} {
			st, ok := m.LatestState(id)
			if !ok {
				continue
			}
			var checkpoint lineage.State
			if id == hop.From {
				checkpoint = m.State(hop.Source)
			} else {
				target := m.State(hop.Target)
				if len(target.Parents) == 1 {
					checkpoint = m.State(target.Parents[0])
				}
			}
			if checkpoint.ID != "" && st.ID != checkpoint.ID {
				m.ReplaceProjection(id, ir.Cursor{Head: checkpoint.Head, Offset: checkpoint.Offset}, checkpoint.Projection, checkpoint.Heads, checkpoint.Loss)
			}
		}
		if err = m.UndoOperationAt(operation, j.UndoTime); err != nil {
			return err
		}
		if err = m.Validate(); err != nil {
			return fmt.Errorf("undo lineage: %w", err)
		}
		if err = fsys.WriteFile(saved.path, m.Encode(), 0o600); err != nil {
			return err
		}
	}
	return nil
}
