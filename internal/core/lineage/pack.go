package lineage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/roeehrl/hopsesh/sdk/ir"
)

// FileFormat is how a lineage file holds a Format manifest: its records with each value
// written once. A revision is [replica, anchor, hash] or [replica, anchor, hash, parents]:
// the replica's and the parents' places in their lists, the parents only when they are not
// just the revision before, and no id (it is the hash of the rest, worked out again on
// read). A native projection, the anchor and hash of its one revision, is that revision's
// place. Everything else is as in Format. In memory, and between machines, a manifest is
// always Format; a manifest that cannot be packed is written as Format.
//
// Most of a long session's lineage was these repeats: its ids are 64 hex digits, each
// revision's written three times and its anchor and hash twice.
const FileFormat = "lineage/6"

type packedManifest struct {
	Format        string         `json:"hopsesh"`
	Family        string         `json:"family"`
	Branch        string         `json:"branch"`
	Branches      []Branch       `json:"branches"`
	Replicas      []Replica      `json:"replicas"`
	Revisions     []packedRev    `json:"revisions"`
	States        []packedState  `json:"states"`
	Hops          []Hop          `json:"hops"`
	Compensations []Compensation `json:"compensations,omitempty"`
}

type packedRev struct {
	replica      int
	anchor, hash string
	parents      []int
	previous     bool // parents are just the revision before
}

type packedState struct {
	ID         StateID           `json:"id"`
	Replica    ReplicaID         `json:"replica"`
	Parents    []StateID         `json:"parents,omitempty"`
	Head       ir.NodeID         `json:"head,omitempty"`
	Offset     int64             `json:"offset"`
	Heads      []ir.NodeID       `json:"heads,omitempty"`
	Projection []json.RawMessage `json:"projection,omitempty"`
	Loss       []string          `json:"loss,omitempty"`
}

type packedProjection struct {
	Fragments []packedFragment `json:"fragments,omitempty"`
	Anchor    string           `json:"anchor"`
	Hash      string           `json:"hash"`
	Coverage  []int            `json:"coverage,omitempty"`
	Generated bool             `json:"generated,omitempty"`
	Fidelity  string           `json:"fidelity,omitempty"`
}

type packedFragment struct {
	Text     string `json:"text"`
	Coverage []int  `json:"coverage"`
}

func (r packedRev) MarshalJSON() ([]byte, error) {
	b := strconv.AppendInt([]byte{'['}, int64(r.replica), 10)
	for _, s := range []string{r.anchor, r.hash} {
		q, err := json.Marshal(s)
		if err != nil {
			return nil, err
		}
		b = append(append(b, ','), q...)
	}
	if !r.previous {
		b = append(b, ",["...)
		for i, p := range r.parents {
			if i > 0 {
				b = append(b, ',')
			}
			b = strconv.AppendInt(b, int64(p), 10)
		}
		b = append(b, ']')
	}
	return append(b, ']'), nil
}

func (r *packedRev) UnmarshalJSON(b []byte) error {
	var a []json.RawMessage
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	if len(a) != 3 && len(a) != 4 {
		return fmt.Errorf("invalid packed revision")
	}
	if err := json.Unmarshal(a[0], &r.replica); err != nil {
		return err
	}
	if err := json.Unmarshal(a[1], &r.anchor); err != nil {
		return err
	}
	if err := json.Unmarshal(a[2], &r.hash); err != nil {
		return err
	}
	r.previous = len(a) == 3
	if !r.previous {
		return json.Unmarshal(a[3], &r.parents)
	}
	return nil
}

// pack is m as FileFormat, or false when a reference has no place to point at (a manifest
// Validate would refuse; it is written as it is).
func (m *Manifest) pack() (*packedManifest, bool) {
	reps := make(map[ReplicaID]int, len(m.Replicas))
	for i, r := range m.Replicas {
		reps[r.ID] = i
	}
	revs := make(map[ir.NodeID]int, len(m.Revisions))
	for i, r := range m.Revisions {
		if _, dup := revs[r.ID]; dup {
			return nil, false
		}
		revs[r.ID] = i
	}
	places := func(ids []ir.NodeID) ([]int, bool) {
		if ids == nil {
			return nil, true
		}
		out := make([]int, len(ids))
		for i, id := range ids {
			p, ok := revs[id]
			if !ok {
				return nil, false
			}
			out[i] = p
		}
		return out, true
	}
	out := &packedManifest{Format: FileFormat, Family: m.Family, Branch: m.Branch, Branches: m.Branches, Replicas: m.Replicas, Hops: m.Hops, Compensations: m.Compensations}
	out.Revisions = makeLike[packedRev](m.Revisions)
	for i, r := range m.Revisions {
		rep, ok := reps[r.Replica]
		parents, ok2 := places(r.Parents)
		if !ok || !ok2 {
			return nil, false
		}
		out.Revisions[i] = packedRev{replica: rep, anchor: r.Anchor, hash: r.Hash, parents: parents,
			previous: (i == 0 && len(parents) == 0) || (len(parents) == 1 && parents[0] == i-1)}
	}
	out.States = makeLike[packedState](m.States)
	for i, s := range m.States {
		ps := packedState{ID: s.ID, Replica: s.Replica, Parents: s.Parents, Head: s.Head, Offset: s.Offset, Heads: s.Heads, Loss: s.Loss}
		for _, p := range s.Projection {
			var b []byte
			if r, ok := revs[firstOf(p.Coverage)]; ok && len(p.Coverage) == 1 && len(p.Fragments) == 0 && !p.Generated && p.Fidelity == "native" && p.Anchor == m.Revisions[r].Anchor && p.Hash == m.Revisions[r].Hash {
				b = strconv.AppendInt(nil, int64(r), 10)
			} else {
				pp := packedProjection{Anchor: p.Anchor, Hash: p.Hash, Generated: p.Generated, Fidelity: p.Fidelity}
				if pp.Coverage, ok = places(p.Coverage); !ok {
					return nil, false
				}
				for _, f := range p.Fragments {
					c, ok := places(f.Coverage)
					if !ok {
						return nil, false
					}
					pp.Fragments = append(pp.Fragments, packedFragment{Text: f.Text, Coverage: c})
				}
				var err error
				if b, err = json.Marshal(pp); err != nil {
					return nil, false
				}
			}
			ps.Projection = append(ps.Projection, b)
		}
		out.States[i] = ps
	}
	return out, true
}

// makeLike is a slice as long as like, nil when like is (an empty manifest's lists are null).
func makeLike[T, U any](like []U) []T {
	if like == nil {
		return nil
	}
	return make([]T, len(like))
}

func firstOf(ids []ir.NodeID) ir.NodeID {
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

// unpack is the Format manifest a FileFormat file holds.
func unpack(b []byte) (*Manifest, error) {
	var p packedManifest
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return nil, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("lineage has trailing JSON")
	}
	m := &Manifest{Format: Format, Family: p.Family, Branch: p.Branch, Branches: p.Branches, Replicas: p.Replicas, Hops: p.Hops, Compensations: p.Compensations}
	m.Revisions = makeLike[Revision](p.Revisions)
	for i, r := range p.Revisions {
		if r.replica < 0 || r.replica >= len(p.Replicas) {
			return nil, fmt.Errorf("revision references unknown replica")
		}
		parents := r.parents
		if r.previous && i > 0 {
			parents = []int{i - 1}
		}
		for _, q := range parents {
			if q < 0 || q >= len(p.Revisions) {
				return nil, fmt.Errorf("missing causal revision")
			}
		}
		p.Revisions[i].parents = parents
		m.Revisions[i] = Revision{Replica: p.Replicas[r.replica].ID, Anchor: r.anchor, Hash: r.hash}
	}
	if err := identify(m.Revisions, p.Revisions); err != nil {
		return nil, err
	}
	ids := func(places []int) ([]ir.NodeID, error) {
		if places == nil {
			return nil, nil
		}
		out := make([]ir.NodeID, len(places))
		for i, q := range places {
			if q < 0 || q >= len(m.Revisions) {
				return nil, fmt.Errorf("missing projection revision")
			}
			out[i] = m.Revisions[q].ID
		}
		return out, nil
	}
	m.States = makeLike[State](p.States)
	for i, s := range p.States {
		st := State{ID: s.ID, Replica: s.Replica, Parents: s.Parents, Head: s.Head, Offset: s.Offset, Heads: s.Heads, Loss: s.Loss}
		for _, raw := range s.Projection {
			var pr ir.Projection
			if q, err := strconv.Atoi(string(raw)); err == nil {
				if q < 0 || q >= len(m.Revisions) {
					return nil, fmt.Errorf("missing projection revision")
				}
				r := m.Revisions[q]
				pr = ir.Projection{Anchor: r.Anchor, Hash: r.Hash, Coverage: []ir.NodeID{r.ID}, Fidelity: "native"}
			} else {
				var pp packedProjection
				d := json.NewDecoder(bytes.NewReader(raw))
				d.DisallowUnknownFields()
				if err := d.Decode(&pp); err != nil {
					return nil, err
				}
				pr = ir.Projection{Anchor: pp.Anchor, Hash: pp.Hash, Generated: pp.Generated, Fidelity: pp.Fidelity}
				if pr.Coverage, err = ids(pp.Coverage); err != nil {
					return nil, err
				}
				for _, f := range pp.Fragments {
					c, err := ids(f.Coverage)
					if err != nil {
						return nil, err
					}
					pr.Fragments = append(pr.Fragments, ir.Fragment{Text: f.Text, Coverage: c})
				}
			}
			st.Projection = append(st.Projection, pr)
		}
		m.States[i] = st
	}
	return m, nil
}

// identify gives each revision its id, the hash of its body, once its parents have theirs
// (a parent may come later in the list). It walks without recursion: a chain is as long as
// the session.
func identify(revs []Revision, packed []packedRev) error {
	const (
		unseen = iota
		visiting
		done
	)
	mark := make([]uint8, len(revs))
	for start := range revs {
		if mark[start] == done {
			continue
		}
		stack := []int{start}
		mark[start] = visiting
		for len(stack) > 0 {
			i := stack[len(stack)-1]
			next := -1
			for _, q := range packed[i].parents {
				if mark[q] == visiting {
					return fmt.Errorf("lineage causal graph 1 contains a cycle")
				}
				if mark[q] == unseen {
					next = q
					break
				}
			}
			if next >= 0 {
				mark[next] = visiting
				stack = append(stack, next)
				continue
			}
			if len(packed[i].parents) > 0 {
				revs[i].Parents = make([]ir.NodeID, len(packed[i].parents))
				for k, q := range packed[i].parents {
					revs[i].Parents[k] = revs[q].ID
				}
			}
			revs[i].ID = ir.NodeID(hash(revs[i]))
			mark[i] = done
			stack = stack[:len(stack)-1]
		}
	}
	return nil
}

// fileFormat is the format a lineage file names, read from its first field when it is
// there (every file hopsesh writes) so that only one decode reads the whole file.
func fileFormat(b []byte) (string, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	if t, err := d.Token(); err == nil && t == json.Delim('{') {
		if k, err := d.Token(); err == nil && k == "hopsesh" {
			if v, err := d.Token(); err == nil {
				if s, ok := v.(string); ok {
					return s, nil
				}
			}
		}
	}
	var head struct {
		Format string `json:"hopsesh"`
	}
	err := json.Unmarshal(b, &head)
	return head.Format, err
}
