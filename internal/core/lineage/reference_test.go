package lineage_test

import (
	"fmt"
	"math/rand"
	"slices"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// This oracle owns a chronological action log, independent of the manifest's
// parents, graph traversal, hashes and journey implementation. Forks begin empty
// routes; undo removes only the chosen branch's latest movement. The model covers
// serial branch histories, not concurrent edits or native filesystem effects.
type routePosition struct{ machine, agent, profile int }
type routeEvent struct {
	id       string
	from, to routePosition
	hop      lineage.Hop
}
type routeBranch struct {
	id, parent      string
	origin, current routePosition
	replicas        map[routePosition]lineage.ReplicaID
	active          []routeEvent
}

func TestSeededForkedJourneysAgainstIndependentActionLog(t *testing.T) {
	for seed := int64(0); seed < 8; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			m := lineage.New(fmt.Sprintf("reference-family-%d", seed))
			branches := []*routeBranch{{id: m.Branch, replicas: map[routePosition]lineage.ReplicaID{}}}
			upsert := func(branch *routeBranch, pos routePosition) lineage.ReplicaID {
				if id := branch.replicas[pos]; id != "" {
					return id
				}
				agentID := []agent.ID{"claude", "codex"}[pos.agent]
				id := m.Upsert(lineage.Replica{Line: branch.id, Endpoint: fmt.Sprintf("machine-%d", pos.machine), Location: fmt.Sprintf("Machine %d", pos.machine), Binding: fmt.Sprintf("binding-%d", pos.profile), Key: agent.SessionKey{Agent: agentID, Profile: fmt.Sprintf("profile-%d", pos.profile), Session: agent.SessionID(fmt.Sprintf("native-%s-%d-%d-%d", branch.id, pos.machine, pos.agent, pos.profile))}})
				branch.replicas[pos] = id
				return id
			}
			upsert(branches[0], branches[0].origin)
			for step := 0; step < 64; step++ {
				b := branches[rng.Intn(len(branches))]
				m.Branch = b.id
				op := fmt.Sprintf("seed-%d-step-%d", seed, step)
				position := routePosition{rng.Intn(3), rng.Intn(2), rng.Intn(2)}
				choice := rng.Intn(10)
				t.Logf("step %d branch %s choice %d from %+v to %+v", step, b.id[:8], choice, b.current, position)
				switch {
				case choice < 2 && len(branches) < 6:
					child := &routeBranch{id: m.Fork(op, nil), parent: b.id, origin: position, current: position, replicas: map[routePosition]lineage.ReplicaID{}}
					to := upsert(child, position)
					if err := m.AppendHop(lineage.Hop{ID: op, From: upsert(b, b.current), To: to, Line: child.id, Fork: true, Kind: lineage.HopContinue, Notify: true, Time: time.Unix(int64(rng.Intn(100)), 0)}); err != nil {
						t.Fatal(step, err)
					}
					branches = append(branches, child)
				case choice == 2 && len(b.active) > 0:
					last := b.active[len(b.active)-1]
					if err := m.UndoOperation(last.id); err != nil {
						t.Fatal(step, err)
					}
					if err := m.UndoOperation(last.id); err != nil { // repeated compensation is idempotent
						t.Fatal(step, err)
					}
					b.active = b.active[:len(b.active)-1]
					b.current = last.from
				case choice == 3 && len(b.active) > 0:
					if err := m.AppendHop(b.active[rng.Intn(len(b.active))].hop); err != nil {
						t.Fatal("replayed identical receipt", step, err)
					}
				default:
					if position == b.current {
						position.machine = (position.machine + 1) % 3
					}
					h := lineage.Hop{ID: op, From: upsert(b, b.current), To: upsert(b, position), Line: b.id, Kind: lineage.HopContinue, Notify: true, Time: time.Unix(int64(rng.Intn(100)), 0)}
					if err := m.AppendHop(h); err != nil {
						t.Fatal(step, err)
					}
					b.active = append(b.active, routeEvent{id: op, from: b.current, to: position, hop: h})
					b.current = position
				}
				// Persisted arrays may arrive in any order, with unrelated fork work
				// interleaved. No expected value is computed from these arrays.
				shuffled := m.Clone()
				rng.Shuffle(len(shuffled.Hops), func(i, j int) { shuffled.Hops[i], shuffled.Hops[j] = shuffled.Hops[j], shuffled.Hops[i] })
				rng.Shuffle(len(shuffled.Replicas), func(i, j int) {
					shuffled.Replicas[i], shuffled.Replicas[j] = shuffled.Replicas[j], shuffled.Replicas[i]
				})
				rng.Shuffle(len(shuffled.Branches), func(i, j int) {
					shuffled.Branches[i], shuffled.Branches[j] = shuffled.Branches[j], shuffled.Branches[i]
				})
				parsed, err := lineage.Parse(shuffled.Encode())
				if err != nil {
					t.Fatal("persisted graph", step, err)
				}
				for _, branch := range branches {
					checkReferenceRoute(t, step, parsed.ForBranch(branch.id), branch)
				}
			}
		})
	}
}

func checkReferenceRoute(t *testing.T, step int, m *lineage.Manifest, b *routeBranch) {
	t.Helper()
	visits := map[routePosition]int{b.origin: 1}
	machineVisits := map[int]int{b.origin.machine: 1}
	var returns, roundTrips, machineTransfers, machineReturns, machineRoundTrips int
	var ids []string
	seenReplicas := map[lineage.ReplicaID]bool{}
	for _, e := range b.active {
		ids = append(ids, e.id)
		seenReplicas[b.replicas[e.from]], seenReplicas[b.replicas[e.to]] = true, true
		visits[e.to]++
		if visits[e.to] > 1 {
			returns++
		}
		if e.to == b.origin {
			roundTrips++
		}
		if e.from.machine != e.to.machine {
			machineTransfers++
			machineVisits[e.to.machine]++
			if machineVisits[e.to.machine] > 1 {
				machineReturns++
			}
			if e.to.machine == b.origin.machine {
				machineRoundTrips++
			}
		}
	}
	j := m.Journey()
	if j.Transfers != len(b.active) || j.Returns != returns || j.RoundTrips != roundTrips || j.MachineTransfers != machineTransfers || j.MachineReturns != machineReturns || j.MachineRoundTrips != machineRoundTrips || j.Fork != (b.parent != "") || j.ParentBranch != b.parent {
		t.Fatalf("step %d branch %s: journey %+v differs from action log transfers=%d returns=%d roundTrips=%d machine=%d/%d/%d", step, b.id, j, len(b.active), returns, roundTrips, machineTransfers, machineReturns, machineRoundTrips)
	}
	var actualIDs []string
	for _, h := range m.ActiveHops() {
		if h.Line == b.id && !h.Fork {
			actualIDs = append(actualIDs, h.ID)
		}
	}
	if !slices.Equal(ids, actualIDs) {
		t.Fatalf("step %d branch %s: active operations differ from action log", step, b.id)
	}
	current := b.replicas[b.current]
	delete(seenReplicas, current)
	actualReturns := m.ReturnReplicas(current)
	if len(actualReturns) != len(seenReplicas) {
		t.Logf("hops=%+v active=%+v current=%s", m.OrderedHops(), b.active, current)
		t.Fatalf("step %d branch %s: return candidates=%d want=%d", step, b.id, len(actualReturns), len(seenReplicas))
	}
	for _, candidate := range actualReturns {
		if !seenReplicas[candidate.ID] || candidate.Line != b.id {
			t.Fatal("return candidate escaped branch action history", step, candidate.ID)
		}
		delete(seenReplicas, candidate.ID)
	}
}
