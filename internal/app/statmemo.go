package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Refreshing an unchanged machine must cost almost nothing: the memos here key what a
// scan derives from a file (a parsed sidecar, a verified native fork) by the file's size
// and modification time, and return the earlier result without opening it. Manifests
// they return are shared between scans and are never modified in place (every writer
// clones first).

// contentReads counts reads of native transcripts and sidecars made by scans; a rescan
// of unchanged files must not add to it (tests hold it to that).
var contentReads atomic.Int64

// transcriptReads counts the native transcripts movement observation read.
var transcriptReads atomic.Int64

// scans counts discovery scans started in this process.
var scans atomic.Int64

// Counters is the work scans did in this process, for resource budgets in tests and
// the idle benchmark: they count work instead of timing it, so they do not flake.
type Counters struct {
	Scans           int64 `json:"scans"`
	ContentReads    int64 `json:"contentReads"`    // transcripts and sidecars opened
	TranscriptReads int64 `json:"transcriptReads"` // native transcripts read for movement
}

func ResourceCounters() Counters {
	return Counters{Scans: scans.Load(), ContentReads: contentReads.Load(), TranscriptReads: transcriptReads.Load()}
}

// statKey names one file's observed version.
type statKey struct {
	Size     int64
	Modified time.Time
	Missing  bool
}

func statOf(fsys agent.FS, path string) (statKey, error) {
	info, err := fsys.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return statKey{Missing: true}, nil
	}
	if err != nil {
		return statKey{}, err
	}
	return statKey{Size: info.Size(), Modified: info.ModTime()}, nil
}

// boundedMemo is a small process-wide cache, cleared when full.
type boundedMemo[V any] struct {
	mu    sync.Mutex
	limit int
	items map[string]V
}

func (m *boundedMemo[V]) get(key string) (V, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.items[key]
	return v, ok
}

func (m *boundedMemo[V]) put(key string, v V) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.items == nil || len(m.items) >= m.limit {
		m.items = map[string]V{}
	}
	m.items[key] = v
}

type sidecarRead struct {
	stat     statKey
	manifest *lineage.Manifest
	problem  string
}

var sidecars = &boundedMemo[sidecarRead]{limit: 4096}

// readSidecar is lineage.Read, without rereading a sidecar that has not changed.
func readSidecar(machine string, fsys agent.FS, main string, limits ...ir.Limits) (*lineage.Manifest, string) {
	path := lineage.PathFor(main)
	st, err := statOf(fsys, path)
	if err != nil {
		return nil, err.Error()
	}
	if st.Missing {
		return nil, ""
	}
	l := ir.DefaultLimits()
	if len(limits) > 0 {
		l = limits[0].Normalize()
	}
	key := machine + "\x00" + path + fmt.Sprint(l.LineageBytes)
	if c, ok := sidecars.get(key); ok && c.stat == st {
		return c.manifest, c.problem
	}
	contentReads.Add(1)
	m, err := lineage.Read(fsys, main, l)
	problem := ""
	if err != nil {
		problem = err.Error()
	}
	// Keep it only if the file did not change while it was read.
	if after, err := statOf(fsys, path); err == nil && after == st {
		sidecars.put(key, sidecarRead{st, m, problem})
	}
	return m, problem
}

// manifestPrint identifies a manifest's content cheaply. Manifests only grow, and every
// record is content-addressed, so the counts and the newest records name a version.
func manifestPrint(m *lineage.Manifest) string {
	if m == nil {
		return ""
	}
	last := func(n int, id func(int) string) string {
		if n == 0 {
			return ""
		}
		return id(n - 1)
	}
	b, _ := json.Marshal([]any{m.Family, m.Branch, len(m.Branches), len(m.Replicas), len(m.Revisions), len(m.States), len(m.Hops), len(m.Compensations),
		last(len(m.Revisions), func(i int) string { return string(m.Revisions[i].ID) }),
		last(len(m.States), func(i int) string { return string(m.States[i].ID) }),
		last(len(m.Hops), func(i int) string { return m.Hops[i].ID }),
		last(len(m.Replicas), func(i int) string { return string(m.Replicas[i].ID) })})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

type forkMember struct {
	manifest *lineage.Manifest
	problem  string
}
type forkResult struct{ members map[string]forkMember }

var forks = &boundedMemo[forkResult]{limit: 64}

// forkInputs fingerprints everything native fork verification reads: each fork and its
// parent (listing, file version, sidecar) and the install. "" when there is nothing to
// verify; cacheable is false when a file cannot be checked (verify without the memo).
func forkInputs(ctx context.Context, hm *host.Machine, h agent.Host, in agent.Install, ss []agent.Summary, manifests []*lineage.Manifest, problems []string) (key string, cacheable bool) {
	by := map[agent.SessionID]int{}
	for i, s := range ss {
		by[s.Key.Session] = i
	}
	type side struct {
		Key, Path, Parent, Manifest, Problem, Version string
		Stat                                          statKey
		Listed                                        bool
	}
	var inputs []side
	add := func(i int) bool {
		st, err := statOf(h.FS(), ss[i].Path)
		if err != nil {
			return false
		}
		inputs = append(inputs, side{Key: ss[i].Key.String(), Path: ss[i].Path, Parent: string(ss[i].NativeParent), Manifest: manifestPrint(manifests[i]), Problem: problems[i], Version: ss[i].AgentVersion, Stat: st})
		return true
	}
	for i, s := range ss {
		if s.NativeParent == "" {
			continue
		}
		if !add(i) {
			return "", false
		}
		p, listed := by[s.NativeParent]
		inputs[len(inputs)-1].Listed = listed
		if listed && !add(p) {
			return "", false
		}
	}
	if len(inputs) == 0 {
		return "", true
	}
	b, _ := json.Marshal([]any{hm.Name, in.BindingID(), in.Version, ir.LimitsFrom(ctx), inputs})
	return fmt.Sprintf("%x", sha256.Sum256(b)), true
}

// errRecalled marks a copy whose observation was replayed from copyObservations.
var errRecalled = errors.New("observation recalled")

// copyObservation is one copy's observation against its family's committed receipts:
// what it found and the records observing added to the in-memory graph.
type copyObservation struct {
	state        lineage.State
	valid        bool
	agentAnchors map[string]bool
	added        graphRecords
}

var copyObservations = &boundedMemo[copyObservation]{limit: 256}

func (c copyObservation) replay(g *lineage.Manifest) {
	have := recordsOf(g)
	for _, b := range c.added.Branches {
		if !have.branch[b.ID] {
			g.Branches = append(g.Branches, b)
		}
	}
	for _, r := range c.added.Replicas {
		if !have.replica[string(r.ID)] {
			g.Replicas = append(g.Replicas, r)
		}
	}
	for _, r := range c.added.Revisions {
		if !have.revision[string(r.ID)] {
			g.Revisions = append(g.Revisions, r)
		}
	}
	for _, s := range c.added.States {
		if !have.state[string(s.ID)] {
			g.States = append(g.States, s)
		}
	}
}

// committedPrint names a family's committed receipts: every copy's sidecar.
func committedPrint(inv *Inventory, family string) string {
	var prints []string
	for _, e := range inv.Entries {
		if e.Lineage != nil && e.Lineage.Family == family && e.LineageError == "" && !e.Cached {
			prints = append(prints, EntryIdentity(e.Machine, e.Session.Key.String())+"\x00"+manifestPrint(e.Lineage))
		}
	}
	slices.Sort(prints)
	b, _ := json.Marshal(prints)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func copyObservationKey(committed string, e Entry, st statKey, in agent.Install, r lineage.Replica, id lineage.ReplicaID) string {
	b, _ := json.Marshal([]any{committed, e.Machine, e.Session.Key.String(), e.Session.Path, st, in.BindingID(), in.Version, r.Binding, id})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

// graphRecords is a graph's records by identity, to find what an observation added.
type graphRecords struct {
	branch, replica, revision, state map[string]bool
	Branches                         []lineage.Branch
	Replicas                         []lineage.Replica
	Revisions                        []lineage.Revision
	States                           []lineage.State
}

func recordsOf(g *lineage.Manifest) graphRecords {
	r := graphRecords{branch: map[string]bool{}, replica: map[string]bool{}, revision: map[string]bool{}, state: map[string]bool{}, Branches: g.Branches, Replicas: g.Replicas, Revisions: g.Revisions, States: g.States}
	for _, b := range g.Branches {
		r.branch[b.ID] = true
	}
	for _, x := range g.Replicas {
		r.replica[string(x.ID)] = true
	}
	for _, x := range g.Revisions {
		r.revision[string(x.ID)] = true
	}
	for _, x := range g.States {
		r.state[string(x.ID)] = true
	}
	return r
}

// since is what r holds that before did not.
func (r graphRecords) since(before graphRecords) graphRecords {
	var out graphRecords
	for _, b := range r.Branches {
		if !before.branch[b.ID] {
			out.Branches = append(out.Branches, b)
		}
	}
	for _, x := range r.Replicas {
		if !before.replica[string(x.ID)] {
			out.Replicas = append(out.Replicas, x)
		}
	}
	for _, x := range r.Revisions {
		if !before.revision[string(x.ID)] {
			out.Revisions = append(out.Revisions, x)
		}
	}
	for _, x := range r.States {
		if !before.state[string(x.ID)] {
			out.States = append(out.States, x)
		}
	}
	return out
}

type mergedGraph struct{ graph *lineage.Manifest } // nil: the receipts conflict

var committedGraphs = &boundedMemo[mergedGraph]{limit: 8}

// committedGraph merges a family's receipts once per version of them and returns a copy
// observation may extend (its record lists are its own; records are never edited).
func committedGraph(inv *Inventory, family string, rows []int) *lineage.Manifest {
	key := committedPrint(inv, family)
	c, ok := committedGraphs.get(key)
	if !ok {
		var g *lineage.Manifest
		for _, i := range rows {
			if g == nil {
				g = inv.Entries[i].Lineage.Clone()
			} else if g.Merge(inv.Entries[i].Lineage) != nil {
				g = nil
				break
			}
		}
		c = mergedGraph{g}
		committedGraphs.put(key, c)
	}
	if c.graph == nil {
		return nil
	}
	return recordCopy(c.graph, c.graph.Branch)
}

// recordCopy is g on branch with record lists of its own: lineage code adds and replaces
// whole records but never edits one in place, so sharing the records is safe, and much
// cheaper than Clone's JSON round trip on a graph of tens of thousands of revisions.
func recordCopy(g *lineage.Manifest, branch string) *lineage.Manifest {
	c := *g
	c.Branch = branch
	c.Branches, c.Replicas, c.Revisions, c.States, c.Hops, c.Compensations = slices.Clone(g.Branches), slices.Clone(g.Replicas), slices.Clone(g.Revisions), slices.Clone(g.States), slices.Clone(g.Hops), slices.Clone(g.Compensations)
	return &c
}

// movementRecords remembers, per session, the sidecar version and notice its movement
// record was last checked against (the notice without its check time, kept within the
// hour).
var movementRecords = &recordSeen{boundedMemo[string]{limit: 4096}}

type recordSeen struct{ boundedMemo[string] }

func (r *recordSeen) key(e Entry, st statKey) string {
	var n *MovementNotice
	if e.Movement != nil {
		c := *e.Movement
		if !c.CheckedAt.IsZero() {
			c.Text = strings.TrimSuffix(c.Text, " Last checked "+c.CheckedAt.UTC().Format(time.RFC3339)+".")
			c.CheckedAt = c.CheckedAt.Truncate(time.Hour)
		}
		n = &c
	}
	b, _ := json.Marshal([]any{e.Session.Key, st, n})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
