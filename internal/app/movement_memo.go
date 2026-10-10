package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/lineage"
)

// movementMemo keeps each lineage family's observation between scans. Observing reads
// and hashes every copy's whole native transcript (hundreds of megabytes for a long
// Codex session); when no copy, install or receipt changed, the result is the same, so
// an idle refresh reuses it instead of reading again.
type movementMemo struct {
	mu       sync.Mutex
	families map[string]familyObservation
}

// movementMemoFamilies bounds how many families' graphs the memo keeps.
const movementMemoFamilies = 32

type familyObservation struct {
	fingerprint string
	graph       *lineage.Manifest
	observed    map[lineage.ReplicaID]rememberedObservation
	ids         map[string]lineage.ReplicaID // entry identity -> replica
}

type rememberedObservation struct {
	movementObservation
	identity string // the entry it describes, rebound to the current scan's row
}

func (m *movementMemo) get(family, fingerprint string) (familyObservation, bool) {
	if m == nil {
		return familyObservation{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.families[family]
	return f, ok && f.fingerprint == fingerprint
}

func (m *movementMemo) put(family, fingerprint string, g *lineage.Manifest, inv *Inventory, observed map[lineage.ReplicaID]movementObservation, ids map[int]lineage.ReplicaID) {
	if m == nil {
		return
	}
	f := familyObservation{fingerprint: fingerprint, graph: g, observed: map[lineage.ReplicaID]rememberedObservation{}, ids: map[string]lineage.ReplicaID{}}
	for id, o := range observed {
		r := rememberedObservation{movementObservation: o}
		if o.entry != nil {
			r.identity = EntryIdentity(o.entry.Machine, o.entry.Session.Key.String())
			r.entry = nil
		}
		f.observed[id] = r
	}
	for i, id := range ids {
		e := inv.Entries[i]
		f.ids[EntryIdentity(e.Machine, e.Session.Key.String())] = id
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.families == nil || len(m.families) >= movementMemoFamilies {
		m.families = map[string]familyObservation{}
	}
	m.families[family] = f
}

// recall fills observed and ids for this scan's rows and returns the observed graph.
func (f familyObservation) recall(inv *Inventory, observed map[lineage.ReplicaID]movementObservation, ids map[int]lineage.ReplicaID) *lineage.Manifest {
	rows := map[string]int{}
	for i, e := range inv.Entries {
		if !e.Cached {
			rows[EntryIdentity(e.Machine, e.Session.Key.String())] = i
		}
	}
	for id, r := range f.observed {
		o := r.movementObservation
		if i, ok := rows[r.identity]; ok && r.identity != "" {
			o.entry = &inv.Entries[i]
			if !o.checked.IsZero() {
				o.checked = o.entry.ObservedAt
			}
		}
		observed[id] = o
	}
	for identity, id := range f.ids {
		if i, ok := rows[identity]; ok {
			ids[i] = id
		}
	}
	return f.graph
}

// movementFingerprint names everything a family's observation depends on: each copy's
// native file (size and last activity), its machine, install and presentation, and the
// receipts each copy's sidecar holds.
func (a *App) movementFingerprint(ctx context.Context, inv *Inventory, family string) string {
	type copyInput struct {
		Identity, Endpoint, Status, Binding, Version, Path, Title, Live, State, Lineage string
		Size, FileSize                                                                  int64
		Activity, Modified                                                              time.Time
		Installed                                                                       bool
	}
	var copies []copyInput
	for _, e := range inv.Entries {
		if e.Lineage == nil || e.Lineage.Family != family || e.LineageError != "" || e.Cached {
			continue
		}
		c := copyInput{Lineage: manifestPrint(e.Lineage), Identity: EntryIdentity(e.Machine, e.Session.Key.String()), Path: e.Session.Path, Size: e.Session.Size, Activity: e.Session.LastActivity, Title: e.Session.Title, Live: e.Live.Name, State: string(e.Live.State)}
		if m := inv.Machine(e.Machine); m != nil {
			c.Status = string(m.Status)
			if m.host != nil {
				c.Endpoint = m.host.Facts.Endpoint
			}
			in, installed := m.InstallProfile(e.Agent, e.Session.Key.Profile)
			c.Binding, c.Version, c.Installed = in.BindingID(), in.Version, installed
			// The native file itself, not only the listing's view of it.
			if mod, ok := a.Module(e.Agent); ok && m.host != nil && installed && e.Session.Path != "" {
				if h, err := m.host.For(ctx, mod.Spec(), in, nil); err == nil {
					if info, err := h.FS().Stat(e.Session.Path); err == nil {
						c.FileSize, c.Modified = info.Size(), info.ModTime()
					}
				}
			}
		}
		copies = append(copies, c)
	}
	slices.SortFunc(copies, func(x, y copyInput) int { return strings.Compare(x.Identity, y.Identity) })
	b, _ := json.Marshal(copies)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
