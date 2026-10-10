package lineage

// ActiveHops excludes undone operations and implementation-only native backups.
// OrderedHops supplies causal ordering; wall-clock timestamps are not authorities.
func (m *Manifest) ActiveHops() []Hop {
	if m == nil {
		return nil
	}
	undone := map[string]bool{}
	for _, c := range m.Compensations {
		undone[c.Operation] = true
	}
	var out []Hop
	for _, h := range m.OrderedHops() {
		if !h.Backup && !undone[h.ID] {
			out = append(out, h)
		}
	}
	return out
}

// ReturnReplicas lists destinations actually visited by this branch before the
// selected replica's latest arrival. A sibling's or parent's history is not a return.
func (m *Manifest) ReturnReplicas(current ReplicaID) []Replica {
	if m == nil || !m.HasReplica(current) {
		return nil
	}
	line := m.Replica(current).Line
	g := m.movementHistory()
	var arrivals []Hop
	for _, h := range g.active {
		if h.Line == line && h.To == current {
			arrivals = append(arrivals, h)
		}
	}
	arrival, ok := g.latest(arrivals)
	if !ok {
		return nil
	}
	// Follow only the arrival's ancestors, not parallel moves ordered beside it.
	ancestors := g.ancestors([]string{arrival.ID})
	seen := map[ReplicaID]bool{current: true}
	// Preserve rollover originals as historical candidates. Only the app can
	// establish whether their replacement exists and the original is not live.
	var out []Replica
	for i := len(g.active) - 1; i >= 0; i-- {
		h := g.active[i]
		if !ancestors[h.ID] || h.Line != line {
			continue
		}
		for _, id := range []ReplicaID{h.From, h.To} {
			r := m.Replica(id)
			if seen[id] || r.ID == "" || r.Line != line {
				continue
			}
			seen[id] = true
			out = append(out, r)
		}
	}
	return out
}

// Departed returns where a replica's branch went after it: the latest hop on its line,
// when the replica left that line and has not been returned to since. It is the
// committed movement record, whether or not the move asked for a notice; a fork leaves
// its parent current and is not a departure.
func (m *Manifest) Departed(current ReplicaID) (Hop, bool) {
	if m == nil || !m.HasReplica(current) {
		return Hop{}, false
	}
	line := m.Replica(current).Line
	g := m.movementHistory()
	var route []Hop
	left := false
	for _, h := range g.active {
		if h.Line != line || h.Fork {
			continue
		}
		route = append(route, h)
		if h.From == current {
			left = true
		}
	}
	if !left {
		return Hop{}, false
	}
	last, ok := g.latest(route)
	if !ok || last.To == current {
		return Hop{}, false
	}
	return last, true
}

// Departure returns the latest unambiguous route away from a replica. A return
// clears it. Fork notices describe a separate branch and never retire the parent.
func (m *Manifest) Departure(current ReplicaID) (Hop, bool) {
	if m == nil || !m.HasReplica(current) {
		return Hop{}, false
	}
	line := m.Replica(current).Line
	g := m.movementHistory()
	var route, candidates []Hop
	for _, h := range g.active {
		if h.Fork && h.From == current {
			candidates = append(candidates, h)
		}
		if h.Line == line {
			route = append(route, h)
		}
	}
	var departure Hop
	if len(route) > 0 {
		last, ok := g.latest(route)
		if !ok {
			return Hop{}, false
		}
		if last.To != current {
			var departures []Hop
			for _, h := range route {
				if h.From == current {
					departures = append(departures, h)
				}
			}
			if len(departures) > 0 {
				departure, ok = g.latest(departures)
				if !ok {
					return Hop{}, false
				}
				candidates = append(candidates, last)
			}
		}
	}
	// Select before applying Notify: an opted-out or concurrent departure must
	// never expose an older enabled notice. An inbound hop does not retire forks.
	last, ok := g.latest(candidates)
	if !ok || !last.Notify {
		return Hop{}, false
	}
	if last.Fork {
		return last, true
	}
	// Every active leg since the selected copy's latest departure must opt in.
	// A later visit resets that interval; old opt-outs are not permanent bans.
	afterDeparture := map[string]bool{departure.ID: true}
	for _, h := range g.ordered {
		for _, p := range h.Parents {
			if afterDeparture[p] {
				afterDeparture[h.ID] = true
			}
		}
	}
	for _, h := range route {
		if afterDeparture[h.ID] && !h.Notify {
			return Hop{}, false
		}
	}
	return last, true
}

// Retain filtered operations as causal links. Undoing a hop or hiding a native
// backup does not sever the ancestry of later committed operations.
type movementHistory struct {
	ordered []Hop
	active  []Hop
	byID    map[string]Hop
}

func (m *Manifest) movementHistory() movementHistory {
	g := movementHistory{ordered: m.OrderedHops(), byID: map[string]Hop{}}
	undone := map[string]bool{}
	for _, c := range m.Compensations {
		undone[c.Operation] = true
	}
	for _, h := range g.ordered {
		g.byID[h.ID] = h
		if !h.Backup && !undone[h.ID] {
			g.active = append(g.active, h)
		}
	}
	return g
}

func (g movementHistory) ancestors(ids []string) map[string]bool {
	seen := map[string]bool{}
	// Own the stack: callers may pass a hop's immutable Parents slice.
	stack := append([]string(nil), ids...)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if !seen[id] {
			seen[id] = true
			stack = append(stack, g.byID[id].Parents...)
		}
	}
	return seen
}

// latest requires one maximal candidate by ancestry, not serialization order,
// operation ID or time. A common descendant need not itself be a candidate.
func (g movementHistory) latest(candidates []Hop) (Hop, bool) {
	var parents []string
	for _, h := range candidates {
		parents = append(parents, h.Parents...)
	}
	ancestors := g.ancestors(parents)
	var last Hop
	for _, h := range candidates {
		if ancestors[h.ID] {
			continue
		}
		if last.ID != "" {
			return Hop{}, false
		}
		last = h
	}
	return last, last.ID != ""
}
