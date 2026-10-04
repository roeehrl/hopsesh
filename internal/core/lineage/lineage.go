// Package lineage records where a session has been. Every session hopsesh moves or
// converts gets a manifest beside its main file (in the agent's own folder), and every
// move carries it along, so any hopsesh on any machine can tell how the copies of a
// session relate, without relying on the state of one install.
package lineage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Suffix names a manifest: the session's main file name plus Suffix.
const Suffix = ".hopsesh.json"

// format is the manifest format; other formats are not read (hopsesh keeps no code for
// older ones).
const format = "lineage/2"

// maxSize bounds a manifest read.
const maxSize = 4 << 20

// Replica is one copy of the session, as it was when a hop recorded it.
type Replica struct {
	Key agent.SessionKey `json:"key"`
	// Location is a machine's name, or a cloud's ("codex-cloud"). A cloud replica has no
	// file to sit beside: it is recorded in the manifests of the copies it hopped from or to.
	Location     string    `json:"location"`
	AgentVersion string    `json:"agentVersion,omitempty"`
	Head         ir.NodeID `json:"head,omitempty"`   // the last conversation node then ("" for a cloud copy hopsesh cannot read)
	Offset       int64     `json:"offset,omitempty"` // the main file's size then
	Time         time.Time `json:"time"`
	// URL is a cloud copy's page. Manifests stay in the agents' folders, never in a
	// repository.
	URL    string `json:"url,omitempty"`
	Branch string `json:"branch,omitempty"` // a cloud copy's branch
}

// Kinds of hops.
const (
	HopMove     = "move"     // the same agent, another location
	HopContinue = "continue" // another agent (a conversion)
	HopHandoff  = "handoff"  // up to a cloud: a briefing and the code
	HopFetch    = "fetch"    // down from a cloud
)

// Hop is one move, conversion, handoff or fetch between two replicas.
type Hop struct {
	Time time.Time `json:"time"`
	From int       `json:"from"` // index into Replicas
	To   int       `json:"to"`
	Kind string    `json:"kind"`
	Fork bool      `json:"fork,omitempty"` // both copies continue on purpose
	// Fidelity is what the hop carried: history or note between agents on machines; a
	// cloud's fidelity (brief, native, code, text) for a handoff or a fetch.
	Fidelity string `json:"fidelity,omitempty"`
	// Written is the byte range hopsesh wrote into the To replica's file (conversions):
	// those nodes are never read back as the other agent's work.
	Written *Range `json:"written,omitempty"`
	// Code is how the code went with a handoff or a fetch.
	Code *CodeHop `json:"code,omitempty"`
}

// CodeHop is the code side of a handoff or a fetch.
type CodeHop struct {
	Way      agent.CodeWay `json:"way"`
	Remote   string        `json:"remote,omitempty"` // the repository's identity (host/owner/repo)
	Branch   string        `json:"branch,omitempty"` // the handoff branch, or the cloud's branch
	Base     string        `json:"base,omitempty"`   // the commit it built on
	Snapshot string        `json:"snapshot,omitempty"`
	// Withheld are files that stayed on the machine (credential-like, or not chosen).
	Withheld []string `json:"withheld,omitempty"`
	// Redactions is how many secrets the scanner masked in the briefing.
	Redactions int `json:"redactions,omitempty"`
}

// Range is a byte range of a file.
type Range struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

// Manifest is a session's lineage.
type Manifest struct {
	Format   string    `json:"hopsesh"`
	Logical  string    `json:"logical"` // one id for every copy of the session
	Replicas []Replica `json:"replicas"`
	Hops     []Hop     `json:"hops"`
}

// New starts a manifest with a fresh logical id.
func New(logical string) *Manifest { return &Manifest{Format: format, Logical: logical} }

// PathFor is the manifest path for a session's main file.
func PathFor(main string) string { return main + Suffix }

// Read loads the manifest beside a session's main file (nil when there is none).
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

// Parse decodes a manifest.
func Parse(b []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m.Format != format {
		return nil, fmt.Errorf("lineage format %q is not %q", m.Format, format)
	}
	return &m, nil
}

// Encode serialises a manifest.
func (m *Manifest) Encode() []byte {
	b, _ := json.MarshalIndent(m, "", "  ")
	return append(b, '\n')
}

// Upsert records a replica's current state, replacing an older record of the same copy
// (same key and location). It returns the replica's index.
func (m *Manifest) Upsert(r Replica) int {
	for i, x := range m.Replicas {
		if x.Key == r.Key && x.Location == r.Location {
			m.Replicas[i] = r
			return i
		}
	}
	m.Replicas = append(m.Replicas, r)
	return len(m.Replicas) - 1
}

// Find returns a replica by copy.
func (m *Manifest) Find(key agent.SessionKey, location string) (Replica, int, bool) {
	if m == nil {
		return Replica{}, -1, false
	}
	for i, x := range m.Replicas {
		if x.Key == key && x.Location == location {
			return x, i, true
		}
	}
	return Replica{}, -1, false
}

// Merge adds another manifest of the same logical session (records of the same copy keep
// the newer state).
func (m *Manifest) Merge(o *Manifest) {
	if o == nil || o.Logical != m.Logical {
		return
	}
	index := map[int]int{}
	for i, r := range o.Replicas {
		if x, j, ok := m.Find(r.Key, r.Location); ok {
			if r.Time.After(x.Time) {
				m.Replicas[j] = r
			}
			index[i] = j
			continue
		}
		m.Replicas = append(m.Replicas, r)
		index[i] = len(m.Replicas) - 1
	}
	have := map[string]bool{}
	for _, h := range m.Hops {
		have[hopKey(h, m)] = true
	}
	for _, h := range o.Hops {
		h.From, h.To = index[h.From], index[h.To]
		if !have[hopKey(h, m)] {
			m.Hops = append(m.Hops, h)
		}
	}
}

func hopKey(h Hop, m *Manifest) string {
	return h.Time.UTC().Format(time.RFC3339Nano) + m.Replicas[h.From].Key.String() + m.Replicas[h.To].Key.String()
}

// LastHopTo returns the newest hop into a replica.
func (m *Manifest) LastHopTo(i int) (Hop, bool) {
	var best Hop
	found := false
	for _, h := range m.Hops {
		if h.To == i && (!found || h.Time.After(best.Time)) {
			best, found = h, true
		}
	}
	return best, found
}
