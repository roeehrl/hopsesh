// Package lineage stores portable causal history beside native agent transcripts.
// Native files stay vendor-owned; receipts describe their exact logical coverage.
package lineage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

const Suffix = ".hopsesh.json"
const Format = "lineage/4"
const maxSize = 16 << 20

type ReplicaID string
type StateID string

type Replica struct {
	Binding      string           `json:"binding,omitempty"`
	ID           ReplicaID        `json:"id"`
	Key          agent.SessionKey `json:"key"`
	Endpoint     string           `json:"endpoint"`
	Location     string           `json:"location"`
	Line         string           `json:"line"`
	AgentVersion string           `json:"agentVersion,omitempty"`
	// Native checkpoints are derived from immutable states, never merge authorities.
	Head   ir.NodeID `json:"-"`
	Offset int64     `json:"-"`
	Time   time.Time `json:"time"`
	URL    string    `json:"url,omitempty"`
	Branch string    `json:"branch,omitempty"` // code branch, independent of conversation Line
}

type Branch struct {
	ID        string      `json:"id"`
	Parent    string      `json:"parent,omitempty"`
	ForkHeads []ir.NodeID `json:"forkHeads,omitempty"`
	Origin    ReplicaID   `json:"origin,omitempty"`
}

type Revision struct {
	ID      ir.NodeID   `json:"id"`
	Parents []ir.NodeID `json:"parents,omitempty"`
	Replica ReplicaID   `json:"replica"`
	Anchor  string      `json:"anchor"`
	Hash    string      `json:"hash"`
}

type State struct {
	ID         StateID         `json:"id"`
	Replica    ReplicaID       `json:"replica"`
	Parents    []StateID       `json:"parents,omitempty"`
	Head       ir.NodeID       `json:"head,omitempty"`
	Offset     int64           `json:"offset"`
	Heads      []ir.NodeID     `json:"heads,omitempty"`
	Projection []ir.Projection `json:"projection,omitempty"`
	Loss       []string        `json:"loss,omitempty"`
}

const (
	HopMove     = "move"
	HopContinue = "continue"
	HopHandoff  = "handoff"
	HopFetch    = "fetch"
)

// Rollover records the unchanged replica retained when its active context was full.
type Rollover struct {
	Replica ReplicaID `json:"replica"`
	Cursor  ir.Cursor `json:"cursor"`
}

type Hop struct {
	Rollover *Rollover `json:"rollover,omitempty"`
	ID       string    `json:"id"`
	Parents  []string  `json:"parents,omitempty"`
	Line     string    `json:"line"`
	Time     time.Time `json:"time"`
	From     ReplicaID `json:"from"`
	To       ReplicaID `json:"to"`
	Source   StateID   `json:"source,omitempty"`
	Target   StateID   `json:"target,omitempty"`
	Kind     string    `json:"kind"`
	Fork     bool      `json:"fork,omitempty"`
	Backup   bool      `json:"backup,omitempty"`
	Fidelity string    `json:"fidelity,omitempty"`
	Written  *Range    `json:"written,omitempty"`
	Code     *CodeHop  `json:"code,omitempty"`
}

type CodeHop struct {
	Way        agent.CodeWay `json:"way"`
	Remote     string        `json:"remote,omitempty"`
	Branch     string        `json:"branch,omitempty"`
	Base       string        `json:"base,omitempty"`
	Snapshot   string        `json:"snapshot,omitempty"`
	Withheld   []string      `json:"withheld,omitempty"`
	Redactions int           `json:"redactions,omitempty"`
}

type Range struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

type Compensation struct {
	ID        string `json:"id"`
	Operation string `json:"operation"`
}

type Manifest struct {
	Format        string         `json:"hopsesh"`
	Family        string         `json:"family"`
	Branch        string         `json:"branch"` // branch of the native session beside this manifest
	Branches      []Branch       `json:"branches"`
	Replicas      []Replica      `json:"replicas"`
	Revisions     []Revision     `json:"revisions"`
	States        []State        `json:"states"`
	Hops          []Hop          `json:"hops"`
	Compensations []Compensation `json:"compensations,omitempty"`
}

func hash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func New(family string) *Manifest {
	root := hash([]string{family, "root"})
	return &Manifest{Format: Format, Family: family, Branch: root, Branches: []Branch{{ID: root}}}
}
func NewNative(endpoint string, key agent.SessionKey) *Manifest {
	return New(hash([]string{"native-family", endpoint, key.String()}))
}
func PathFor(main string) string { return main + Suffix }
func Read(fsys agent.FS, main string) (*Manifest, error) {
	b, err := fsys.ReadFile(PathFor(main), maxSize)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(b)
}
func Parse(b []byte) (*Manifest, error) {
	if len(b) > maxSize {
		return nil, fmt.Errorf("lineage exceeds %d bytes", maxSize)
	}
	var m Manifest
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("lineage has trailing JSON")
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}
func (m *Manifest) Clone() *Manifest {
	if m == nil {
		return nil
	}
	b, _ := json.Marshal(m)
	var out Manifest
	_ = json.Unmarshal(b, &out)
	return &out
}
func (m *Manifest) Encode() []byte {
	b, _ := json.MarshalIndent(m, "", "  ")
	return append(b, '\n')
}
func (m *Manifest) ForBranch(branch string) *Manifest {
	out := m.Clone()
	out.Branch = branch
	return out
}
func (m *Manifest) branch(id string) *Branch {
	for i := range m.Branches {
		if m.Branches[i].ID == id {
			return &m.Branches[i]
		}
	}
	return nil
}
func (m *Manifest) Fork(id string, heads []ir.NodeID) string {
	line := hash([]string{m.Family, "fork", id})
	if m.branch(line) == nil {
		m.Branches = append(m.Branches, Branch{ID: line, Parent: m.Branch, ForkHeads: slices.Clone(heads)})
	}
	return line
}
func (m *Manifest) Replica(id ReplicaID) Replica {
	if m != nil {
		for _, r := range m.Replicas {
			if r.ID == id {
				if s, ok := m.LatestState(id); ok {
					r.Head, r.Offset = s.Head, s.Offset
				}
				return r
			}
		}
	}
	return Replica{}
}
func (m *Manifest) HasReplica(id ReplicaID) bool { return m.Replica(id).ID != "" }
func (m *Manifest) Upsert(r Replica) ReplicaID {
	if r.Line == "" {
		r.Line = m.Branch
	}
	if r.Endpoint == "" {
		r.Endpoint = r.Location
	}
	r.ID = ReplicaID(hash([]string{m.Family, r.Line, r.Endpoint, r.Key.String(), r.Binding}))
	found := false
	for _, x := range m.Replicas {
		if x.ID == r.ID {
			found = true
			break
		}
	}
	if !found {
		m.Replicas = append(m.Replicas, r)
		if b := m.branch(r.Line); b != nil && b.Origin == "" {
			b.Origin = r.ID
		}
	}
	return r.ID
}
func (m *Manifest) Find(key agent.SessionKey, location string) (Replica, ReplicaID, bool) {
	if m == nil {
		return Replica{}, "", false
	}
	var found Replica
	for _, r := range m.Replicas {
		if r.Key == key && r.Location == location && r.Line == m.Branch {
			if found.ID != "" && found.ID != r.ID {
				return Replica{}, "", false
			}
			found = m.Replica(r.ID)
		}
	}
	return found, found.ID, found.ID != ""
}
func (m *Manifest) FindEndpoint(key agent.SessionKey, endpoint string) (Replica, ReplicaID, bool) {
	var found Replica
	for _, r := range m.Replicas {
		if r.Key == key && r.Endpoint == endpoint && r.Line == m.Branch {
			if found.ID != "" && found.ID != r.ID {
				return Replica{}, "", false
			}
			found = m.Replica(r.ID)
		}
	}
	return found, found.ID, found.ID != ""
}
func (m *Manifest) FindOnBranch(key agent.SessionKey, location, line string) (Replica, ReplicaID, bool) {
	if m == nil {
		return Replica{}, "", false
	}
	return m.ForBranch(line).Find(key, location)
}
func (m *Manifest) stateTips(id ReplicaID) []StateID {
	have := map[StateID]bool{}
	for _, s := range m.States {
		if s.Replica == id {
			have[s.ID] = true
		}
	}
	for _, s := range m.States {
		if s.Replica == id {
			for _, p := range s.Parents {
				delete(have, p)
			}
		}
	}
	var out []StateID
	for id := range have {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}
func (m *Manifest) LatestState(id ReplicaID) (State, bool) {
	tips := m.stateTips(id)
	if len(tips) != 1 {
		return State{}, false
	}
	return m.State(tips[0]), true
}
func (m *Manifest) State(id StateID) State {
	for _, s := range m.States {
		if s.ID == id {
			return s
		}
	}
	return State{}
}
func (m *Manifest) addState(s State) StateID {
	s.Parents = m.stateTips(s.Replica)
	s.Heads = unique(s.Heads)
	sort.Slice(s.Projection, func(i, j int) bool { return s.Projection[i].Anchor < s.Projection[j].Anchor })
	s.ID = StateID(hash(s))
	for _, old := range m.States {
		if old.ID == s.ID {
			return s.ID
		}
	}
	m.States = append(m.States, s)
	return s.ID
}
func unique[T ~string](xs []T) []T {
	out := slices.Clone(xs)
	slices.Sort(out)
	return slices.Compact(out)
}
func (m *Manifest) Covered(heads []ir.NodeID) map[ir.NodeID]bool {
	out := map[ir.NodeID]bool{}
	todo := slices.Clone(heads)
	by := map[ir.NodeID]Revision{}
	for _, r := range m.Revisions {
		by[r.ID] = r
	}
	for len(todo) > 0 {
		id := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		if out[id] {
			continue
		}
		out[id] = true
		todo = append(todo, by[id].Parents...)
	}
	return out
}
func (m *Manifest) Heads(ids []ir.NodeID) []ir.NodeID {
	ids = unique(ids)
	keep := map[ir.NodeID]bool{}
	for _, id := range ids {
		keep[id] = true
	}
	for _, id := range ids {
		for a := range m.Covered([]ir.NodeID{id}) {
			if a != id {
				delete(keep, a)
			}
		}
	}
	var out []ir.NodeID
	for id := range keep {
		out = append(out, id)
	}
	return unique(out)
}
func Subset(a, b map[ir.NodeID]bool) bool {
	for id := range a {
		if !b[id] {
			return false
		}
	}
	return true
}

// Observe validates previously delivered native anchors, then registers only newly
// authored records. Imported nodes retain their logical origins through every hop.
func (m *Manifest) Observe(replica ReplicaID, seg *ir.Segment) (State, error) {
	old, known := m.LatestState(replica)
	if len(m.stateTips(replica)) > 1 {
		return State{}, fmt.Errorf("%w: concurrent receipts for replica", agent.ErrDiverged)
	}
	by := map[string]ir.Projection{}
	for _, p := range old.Projection {
		by[p.Anchor] = p
	}
	seen := map[string]bool{}
	for i, n := range seg.Nodes {
		anchor := fmt.Sprintf("node:%d", i)
		if n.Native != nil && n.Native.Anchor != "" {
			anchor = n.Native.Anchor
		}
		if seen[anchor] {
			return State{}, fmt.Errorf("%w: duplicate native anchor", agent.ErrDiverged)
		}
		seen[anchor] = true
		if p, ok := by[anchor]; ok && p.Hash != ir.ContentHash(n) {
			return State{}, fmt.Errorf("%w: native history at %s changed; cannot safely append", agent.ErrDiverged, anchor)
		}
	}
	for anchor := range by {
		if !seen[anchor] {
			return State{}, fmt.Errorf("%w: history was compacted or rewound; create a separate fork", agent.ErrDiverged)
		}
	}
	lastKnown := -1
	for i, n := range seg.Nodes {
		anchor := fmt.Sprintf("node:%d", i)
		if n.Native != nil && n.Native.Anchor != "" {
			anchor = n.Native.Anchor
		}
		if _, ok := by[anchor]; ok {
			lastKnown = i
		}
	}
	for i := 0; i < lastKnown; i++ {
		n := seg.Nodes[i]
		anchor := fmt.Sprintf("node:%d", i)
		if n.Native != nil && n.Native.Anchor != "" {
			anchor = n.Native.Anchor
		}
		if _, ok := by[anchor]; !ok {
			return State{}, fmt.Errorf("%w: a record was inserted into prior history", agent.ErrDiverged)
		}
	}
	seen = map[string]bool{}
	var proj []ir.Projection
	heads := slices.Clone(old.Heads)
	covered := m.Covered(heads)
	revisions := map[ir.NodeID]bool{}
	for _, r := range m.Revisions {
		revisions[r.ID] = true
	}
	for i := range seg.Nodes {
		n := &seg.Nodes[i]
		anchor := fmt.Sprintf("node:%d", i)
		if n.Native != nil && n.Native.Anchor != "" {
			anchor = n.Native.Anchor
		}
		digest := ir.ContentHash(*n)
		p, ok := by[anchor]
		if ok {
			if p.Hash != digest {
				return State{}, fmt.Errorf("%w: native history at %s changed; cannot safely append", agent.ErrDiverged, anchor)
			}
			seen[anchor] = true
			n.Coverage = slices.Clone(p.Coverage)
			n.Generated = p.Generated
			n.Fragments = slices.Clone(p.Fragments)
		} else {
			rev := Revision{Replica: replica, Anchor: anchor, Hash: digest, Parents: slices.Clone(heads)}
			id := ir.NodeID(hash(rev))
			n.Coverage = []ir.NodeID{id}
			n.Generated = false
			if !revisions[id] {
				rev.ID = id
				m.Revisions = append(m.Revisions, rev)
				revisions[id] = true
			}
			heads = []ir.NodeID{id}
			covered[id] = true
			p = ir.Projection{Anchor: anchor, Hash: digest, Coverage: slices.Clone(n.Coverage), Fidelity: "native"}
		}
		missing := false
		for _, id := range n.Coverage {
			if !covered[id] {
				missing = true
			}
		}
		if missing {
			heads = m.Heads(append(heads, n.Coverage...))
			covered = m.Covered(heads)
		}
		proj = append(proj, p)
	}
	if known {
		for anchor := range by {
			if !seen[anchor] {
				return State{}, fmt.Errorf("%w: history was compacted or rewound at %s; cannot safely append", agent.ErrDiverged, anchor)
			}
		}
	}
	sort.Slice(proj, func(i, j int) bool { return proj[i].Anchor < proj[j].Anchor })
	if known && old.Head == seg.Cursor.Head && old.Offset == seg.Cursor.Offset && hash(old.Projection) == hash(proj) {
		return old, nil
	}
	s := State{Replica: replica, Head: seg.Cursor.Head, Offset: seg.Cursor.Offset, Heads: heads, Projection: proj, Loss: slices.Clone(old.Loss)}
	id := m.addState(s)
	return m.State(id), nil
}

// Deliver records the destination's own receipt. It acknowledges only this replica,
// keeping all provenance and losses; another destination's receipt is unaffected.
func (m *Manifest) Deliver(replica ReplicaID, cursor ir.Cursor, projection []ir.Projection, heads []ir.NodeID, loss []string) State {
	old, _ := m.LatestState(replica)
	by := map[string]ir.Projection{}
	for _, p := range old.Projection {
		by[p.Anchor] = p
	}
	for _, p := range projection {
		by[p.Anchor] = p
	}
	var ps []ir.Projection
	for _, p := range by {
		ps = append(ps, p)
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].Anchor < ps[j].Anchor })
	id := m.addState(State{Replica: replica, Head: cursor.Head, Offset: cursor.Offset, Heads: m.Heads(append(slices.Clone(old.Heads), heads...)), Projection: ps, Loss: unique(append(slices.Clone(old.Loss), loss...))})
	return m.State(id)
}

// ReplaceProjection is a verified native replacement, not an append receipt.
func (m *Manifest) ReplaceProjection(replica ReplicaID, cursor ir.Cursor, projection []ir.Projection, heads []ir.NodeID, loss []string) State {
	id := m.addState(State{Replica: replica, Head: cursor.Head, Offset: cursor.Offset, Heads: m.Heads(heads), Projection: projection, Loss: loss})
	return m.State(id)
}

func (m *Manifest) Merge(o *Manifest) error {
	if o == nil {
		return nil
	}
	if o.Family != m.Family {
		return fmt.Errorf("unrelated lineage families")
	}
	if err := o.Validate(); err != nil {
		return err
	}
	next := m.Clone()
	union := func(dst, src any, key func(json.RawMessage) string) (json.RawMessage, error) {
		a, _ := json.Marshal(dst)
		b, _ := json.Marshal(src)
		var xs, ys []json.RawMessage
		_ = json.Unmarshal(a, &xs)
		_ = json.Unmarshal(b, &ys)
		by := map[string]json.RawMessage{}
		for _, x := range xs {
			by[key(x)] = x
		}
		for _, y := range ys {
			id := key(y)
			if x, ok := by[id]; ok && string(x) != string(y) {
				return nil, fmt.Errorf("conflicting lineage record %s", id)
			}
			by[id] = y
		}
		ids := make([]string, 0, len(by))
		for id := range by {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		var out []json.RawMessage
		for _, id := range ids {
			out = append(out, by[id])
		}
		return json.Marshal(out)
	}
	key := func(b json.RawMessage) string {
		var x struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(b, &x)
		return x.ID
	}
	for _, pair := range [][2]any{{&next.Branches, o.Branches}, {&next.Replicas, o.Replicas}, {&next.Revisions, o.Revisions}, {&next.States, o.States}, {&next.Hops, o.Hops}, {&next.Compensations, o.Compensations}} {
		b, err := union(pair[0], pair[1], key)
		if err != nil {
			return err
		}
		reflect.ValueOf(pair[0]).Elem().SetZero()
		if err = json.Unmarshal(b, pair[0]); err != nil {
			return err
		}
	}
	if err := next.Validate(); err != nil {
		return err
	}
	*m = *next
	return nil
}
func (m *Manifest) hopTips(line string) []string {
	have := map[string]bool{}
	for _, h := range m.Hops {
		if h.Line == line && !h.Backup {
			have[h.ID] = true
		}
	}
	for _, h := range m.Hops {
		if h.Backup {
			continue
		}
		for _, p := range h.Parents {
			delete(have, p)
		}
	}
	var out []string
	for id := range have {
		out = append(out, id)
	}
	return unique(out)
}
func (m *Manifest) AppendHop(h Hop) error {
	for _, old := range m.Hops {
		if old.ID == h.ID && len(h.Parents) == 0 {
			h.Parents = slices.Clone(old.Parents)
			break
		}
	}
	if h.Line == "" {
		h.Line = m.Replica(h.To).Line
	}
	if len(h.Parents) == 0 {
		h.Parents = m.hopTips(h.Line)
	}
	if h.Source == "" {
		if s, ok := m.LatestState(h.From); ok {
			h.Source = s.ID
		}
	}
	if h.Target == "" {
		if s, ok := m.LatestState(h.To); ok {
			h.Target = s.ID
		}
	}
	if h.ID == "" {
		h.ID = hash(h)
	}
	for _, old := range m.Hops {
		if old.ID == h.ID {
			if hash(old) != hash(h) {
				return fmt.Errorf("conflicting transfer operation %s", h.ID)
			}
			return nil
		}
	}
	m.Hops = append(m.Hops, h)
	return nil
}
func (m *Manifest) OrderedHops() []Hop {
	done := map[string]bool{}
	var out []Hop
	todo := slices.Clone(m.Hops)
	sort.Slice(todo, func(i, j int) bool { return todo[i].ID < todo[j].ID })
	for len(todo) > 0 {
		changed := false
		next := todo[:0]
		for _, h := range todo {
			ready := true
			for _, p := range h.Parents {
				if !done[p] {
					ready = false
				}
			}
			if ready {
				out = append(out, h)
				done[h.ID] = true
				changed = true
			} else {
				next = append(next, h)
			}
		}
		todo = next
		if !changed {
			break
		}
	}
	return out
}
func (m *Manifest) UndoOperation(id string) error {
	for _, c := range m.Compensations {
		if c.Operation == id {
			return nil
		}
	}
	for _, h := range m.Hops {
		if h.ID == id {
			m.Compensations = append(m.Compensations, Compensation{ID: hash([]string{m.Family, "undo", id}), Operation: id})
			return nil
		}
	}
	return fmt.Errorf("unknown transfer operation")
}
func (m *Manifest) LastHopTo(id ReplicaID) (Hop, bool) {
	var best Hop
	ok := false
	for _, h := range m.OrderedHops() {
		if h.To == id && !h.Backup {
			best, ok = h, true
		}
	}
	return best, ok
}

type Journey struct {
	MachineTransfers  int    `json:"machineTransfers"`
	MachineRoundTrips int    `json:"machineRoundTrips"`
	MachineReturns    int    `json:"machineReturns"`
	OriginProfile     string `json:"originProfile,omitempty"`
	ParentBranch      string `json:"parentBranch,omitempty"`
	Origin            string `json:"origin,omitempty"`
	Transfers         int    `json:"transfers"`
	RoundTrips        int    `json:"roundTrips"`
	Returns           int    `json:"returns"`
	Fork              bool   `json:"fork"`
	Branch            string `json:"branch"`
	Family            string `json:"family"`
}

func (m *Manifest) Journey() Journey {
	j := Journey{Branch: m.Branch, Family: m.Family}
	b := m.branch(m.Branch)
	if b == nil {
		return j
	}
	j.Fork = b.Parent != ""
	j.ParentBranch = b.Parent
	origin := m.Replica(b.Origin)
	j.Origin = origin.Location + "/" + string(origin.Key.Agent)
	j.OriginProfile = origin.Key.Profile
	seenMachines := map[string]bool{origin.Endpoint: true}
	place := func(r Replica) string {
		return r.Endpoint + "\x00" + string(r.Key.Agent) + "\x00" + r.Key.Profile + "\x00" + r.Binding
	}
	seen := map[string]bool{}
	if origin.ID != "" {
		seen[place(origin)] = true
	}
	undone := map[string]bool{}
	for _, c := range m.Compensations {
		undone[c.Operation] = true
	}
	for _, h := range m.OrderedHops() {
		if h.Line != m.Branch || h.Backup || undone[h.ID] {
			continue
		}
		from, to := m.Replica(h.From), m.Replica(h.To)
		if h.Fork && to.ID == b.Origin {
			continue
		}
		if place(from) == place(to) {
			continue
		}
		j.Transfers++
		if from.Endpoint != to.Endpoint && !strings.HasPrefix(from.Endpoint, "cloud:") && !strings.HasPrefix(to.Endpoint, "cloud:") {
			j.MachineTransfers++
			if seenMachines[to.Endpoint] {
				j.MachineReturns++
				if to.Endpoint == origin.Endpoint {
					j.MachineRoundTrips++
				}
			}
			seenMachines[to.Endpoint] = true
		}
		if seen[place(to)] {
			j.Returns++
			if place(to) == place(origin) {
				j.RoundTrips++
			}
		}
		seen[place(to)] = true
	}
	return j
}

func (m *Manifest) Validate() error {
	if len(m.Encode()) > maxSize {
		return fmt.Errorf("lineage exceeds %d bytes", maxSize)
	}
	if m.Format != Format {
		return fmt.Errorf("lineage format %q is not %q", m.Format, Format)
	}
	if len(m.Revisions)+len(m.States)+len(m.Hops)+len(m.Replicas)+len(m.Branches)+len(m.Compensations) > 100000 {
		return fmt.Errorf("lineage exceeds graph limits")
	}
	if m.Family == "" || m.Branch == "" {
		return fmt.Errorf("lineage requires family and branch")
	}
	reps := map[string]bool{}
	branches := map[string]bool{}
	revs := map[string]bool{}
	states := map[string]bool{}
	hops := map[string]bool{}
	add := func(set map[string]bool, id string) error {
		if id == "" || set[id] {
			return fmt.Errorf("empty or duplicate lineage id %q", id)
		}
		set[id] = true
		return nil
	}
	for _, b := range m.Branches {
		if err := add(branches, b.ID); err != nil {
			return err
		}
	}
	if !branches[m.Branch] {
		return fmt.Errorf("unknown current branch")
	}
	for _, r := range m.Replicas {
		if err := add(reps, string(r.ID)); err != nil {
			return err
		}
		if r.ID != ReplicaID(hash([]string{m.Family, r.Line, r.Endpoint, r.Key.String(), r.Binding})) {
			return fmt.Errorf("invalid replica identity")
		}
		if r.Endpoint == "" || r.Key.Agent == "" || r.Key.Session == "" || !branches[r.Line] {
			return fmt.Errorf("invalid replica %s", r.ID)
		}
	}
	for _, r := range m.Revisions {
		body := r
		body.ID = ""
		if r.ID != ir.NodeID(hash(body)) {
			return fmt.Errorf("invalid revision identity")
		}
		if err := add(revs, string(r.ID)); err != nil {
			return err
		}
		if !reps[string(r.Replica)] {
			return fmt.Errorf("revision references unknown replica")
		}
		if r.Anchor == "" || r.Hash == "" {
			return fmt.Errorf("authored revision lacks native provenance")
		}
	}
	for _, s := range m.States {
		body := s
		body.ID = ""
		if s.ID != StateID(hash(body)) {
			return fmt.Errorf("invalid receipt identity")
		}
		if err := add(states, string(s.ID)); err != nil {
			return err
		}
		if !reps[string(s.Replica)] || s.Offset < 0 {
			return fmt.Errorf("invalid receipt")
		}
	}
	for _, h := range m.Hops {
		switch h.Kind {
		case HopMove, HopContinue, HopHandoff, HopFetch:
		default:
			return fmt.Errorf("invalid hop kind %q", h.Kind)
		}
		if err := add(hops, h.ID); err != nil {
			return err
		}
		if !reps[string(h.From)] || !reps[string(h.To)] || !branches[h.Line] {
			return fmt.Errorf("invalid hop endpoints")
		}
		from, to := m.Replica(h.From), m.Replica(h.To)
		if h.Rollover != nil {
			old := m.Replica(h.Rollover.Replica)
			if old.ID == "" || old.Line != to.Line || old.Endpoint != to.Endpoint || old.Binding != to.Binding || old.Key.Agent != to.Key.Agent || old.Key.Profile != to.Key.Profile || old.ID == to.ID || h.Rollover.Cursor.Offset < 0 {
				return fmt.Errorf("invalid capacity rollover")
			}
		}
		if h.Line != to.Line || h.Fork != (from.Line != to.Line) {
			return fmt.Errorf("invalid operation branch boundary")
		}
		if h.Fork && m.branch(to.Line).Parent != from.Line {
			return fmt.Errorf("invalid fork operation parent")
		}
		if h.Written != nil && (h.Written.From < 0 || h.Written.To < h.Written.From) {
			return fmt.Errorf("invalid written range")
		}
	}
	graphs := []map[string][]string{{}, {}, {}, {}}
	for _, b := range m.Branches {
		if b.Parent != "" {
			if !branches[b.Parent] {
				return fmt.Errorf("missing fork parent")
			}
			graphs[0][b.ID] = []string{b.Parent}
		}
		if b.Origin != "" && (!reps[string(b.Origin)] || m.Replica(b.Origin).Line != b.ID) {
			return fmt.Errorf("missing branch origin")
		}
		for _, id := range b.ForkHeads {
			if !revs[string(id)] {
				return fmt.Errorf("missing fork revision")
			}
		}
	}
	for _, r := range m.Revisions {
		for _, p := range r.Parents {
			if !revs[string(p)] {
				return fmt.Errorf("missing causal revision")
			}
			graphs[1][string(r.ID)] = append(graphs[1][string(r.ID)], string(p))
		}
	}
	for _, s := range m.States {
		for _, p := range s.Parents {
			if !states[string(p)] || m.State(p).Replica != s.Replica {
				return fmt.Errorf("invalid receipt parent")
			}
			graphs[2][string(s.ID)] = append(graphs[2][string(s.ID)], string(p))
		}
		for _, id := range s.Heads {
			if !revs[string(id)] {
				return fmt.Errorf("missing receipt revision")
			}
		}
		covered := m.Covered(s.Heads)
		seen := map[string]bool{}
		for _, p := range s.Projection {
			if p.Anchor == "" || p.Hash == "" || seen[p.Anchor] {
				return fmt.Errorf("invalid native projection")
			}
			for _, f := range p.Fragments {
				for _, id := range f.Coverage {
					if !revs[string(id)] || !slices.Contains(p.Coverage, id) {
						return fmt.Errorf("invalid fragment coverage")
					}
				}
			}
			seen[p.Anchor] = true
			for _, id := range p.Coverage {
				if !revs[string(id)] || !covered[id] {
					return fmt.Errorf("missing projection revision")
				}
			}
		}
	}
	for _, h := range m.Hops {
		if h.Source != "" && (!states[string(h.Source)] || m.State(h.Source).Replica != h.From) {
			return fmt.Errorf("invalid source receipt")
		}
		if h.Target != "" && (!states[string(h.Target)] || m.State(h.Target).Replica != h.To) {
			return fmt.Errorf("invalid destination receipt")
		}
		for _, p := range h.Parents {
			if !hops[p] {
				return fmt.Errorf("missing operation parent")
			}
			graphs[3][h.ID] = append(graphs[3][h.ID], p)
		}
	}
	for i, g := range graphs {
		if cyclic(g) {
			return fmt.Errorf("lineage causal graph %d contains a cycle", i)
		}
	}
	compensations := map[string]bool{}
	for _, c := range m.Compensations {
		if c.ID != hash([]string{m.Family, "undo", c.Operation}) {
			return fmt.Errorf("invalid undo identity")
		}
		if err := add(compensations, c.ID); err != nil {
			return err
		}
		if !hops[c.Operation] {
			return fmt.Errorf("undo references unknown operation")
		}
	}
	return nil
}
func cyclic(g map[string][]string) bool {
	color := map[string]uint8{}
	var visit func(string) bool
	visit = func(id string) bool {
		if color[id] == 1 {
			return true
		}
		if color[id] == 2 {
			return false
		}
		color[id] = 1
		for _, p := range g[id] {
			if visit(p) {
				return true
			}
		}
		color[id] = 2
		return false
	}
	for id := range g {
		if visit(id) {
			return true
		}
	}
	return false
}
