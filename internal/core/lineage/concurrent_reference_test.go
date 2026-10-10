package lineage_test

import (
	"fmt"
	"maps"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Each simulated observer records which numbered actions it had received when
// it issued a move. These immutable sets are the oracle's causal clocks: they
// never read a production parent edge, graph traversal or ordering result.
type concurrentAction struct {
	branch, from, to int
	seen             map[int]bool
	hop              lineage.Hop
}

type concurrentObserver struct {
	graph   *lineage.Manifest
	seen    map[int]bool
	undone  map[int]bool
	current [2]int
}

func TestConcurrentJourneysAgainstIndependentObserverClocks(t *testing.T) {
	for seed := int64(0); seed < 8; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			base := lineage.New(fmt.Sprintf("concurrent-reference-%d", seed))
			branches := [2]string{base.Branch, base.Fork("initial-fork", nil)}
			var replicas [2][6]lineage.ReplicaID
			for branch := range branches {
				for place := range replicas[branch] {
					replicas[branch][place] = base.Upsert(lineage.Replica{
						Line: branches[branch], Endpoint: fmt.Sprintf("machine-%d", place%3), Location: "same display name",
						Binding: fmt.Sprintf("binding-%d", place/3),
						Key:     agent.SessionKey{Agent: []agent.ID{"claude", "codex"}[place%2], Profile: fmt.Sprint(place / 3), Session: agent.SessionID(fmt.Sprintf("%d-%d", branch, place))},
					})
				}
			}
			if err := base.AppendHop(lineage.Hop{ID: "initial-fork", From: replicas[0][0], To: replicas[1][0], Line: branches[1], Fork: true, Kind: lineage.HopContinue}); err != nil {
				t.Fatal(err)
			}
			var observers [3]concurrentObserver
			for i := range observers {
				observers[i] = concurrentObserver{graph: base.Clone(), seen: map[int]bool{}, undone: map[int]bool{}}
			}
			var actions []concurrentAction
			for step := 0; step < 96; step++ {
				who := rng.Intn(len(observers))
				observer := &observers[who]
				choice := rng.Intn(10)
				t.Logf("step=%d observer=%d action=%d", step, who, choice)
				switch {
				case choice < 5:
					branch := rng.Intn(len(branches))
					from := observer.current[branch]
					to := (from + 1 + rng.Intn(5)) % 6
					hop := lineage.Hop{ID: fmt.Sprintf("op-%03d", len(actions)), From: replicas[branch][from], To: replicas[branch][to], Line: branches[branch], Kind: lineage.HopContinue, Notify: rng.Intn(4) != 0, Time: time.Unix(int64(rng.Intn(200)-100), 0)}
					if err := observer.graph.AppendHop(hop); err != nil {
						t.Fatal(err)
					}
					actions = append(actions, concurrentAction{branch: branch, from: from, to: to, seen: maps.Clone(observer.seen), hop: hop})
					observer.seen[len(actions)-1] = true
					observer.current[branch] = to
				case choice < 8:
					other := &observers[(who+1+rng.Intn(2))%3]
					if err := observer.graph.Merge(other.graph); err != nil {
						t.Fatal(err)
					}
					maps.Copy(observer.seen, other.seen)
					maps.Copy(observer.undone, other.undone)
				default:
					// Choose by action number, never production serialization order.
					var known []int
					for i := range actions {
						if observer.seen[i] {
							known = append(known, i)
						}
					}
					if len(known) > 0 {
						i := known[rng.Intn(len(known))]
						if choice == 8 {
							if err := observer.graph.UndoOperation(actions[i].hop.ID); err != nil {
								t.Fatal(err)
							}
							observer.undone[i] = true
						} else if err := observer.graph.AppendHop(actions[i].hop); err != nil {
							t.Fatal("identical replay", err)
						}
					}
				}
				shuffled := observer.graph.Clone()
				rng.Shuffle(len(shuffled.Hops), func(i, j int) { shuffled.Hops[i], shuffled.Hops[j] = shuffled.Hops[j], shuffled.Hops[i] })
				rng.Shuffle(len(shuffled.Replicas), func(i, j int) {
					shuffled.Replicas[i], shuffled.Replicas[j] = shuffled.Replicas[j], shuffled.Replicas[i]
				})
				rng.Shuffle(len(shuffled.Compensations), func(i, j int) {
					shuffled.Compensations[i], shuffled.Compensations[j] = shuffled.Compensations[j], shuffled.Compensations[i]
				})
				var err error
				observer.graph, err = lineage.Parse(shuffled.Encode())
				if err != nil {
					t.Fatal("persisted concurrent graph", err)
				}
				for i := range observers {
					checkObserverClock(t, step, &observers[i], actions, branches, replicas)
				}
			}
			// Deliver all delayed messages in both orders, then replay them. Every
			// observer must converge without choosing a concurrent route by time.
			left, right := observers[0].graph.Clone(), observers[2].graph.Clone()
			for _, graph := range []*lineage.Manifest{observers[1].graph, observers[2].graph, observers[1].graph} {
				if err := left.Merge(graph); err != nil {
					t.Fatal(err)
				}
			}
			for _, graph := range []*lineage.Manifest{observers[1].graph, observers[0].graph, observers[0].graph} {
				if err := right.Merge(graph); err != nil {
					t.Fatal(err)
				}
			}
			if !reflect.DeepEqual(left, right) {
				t.Fatal("delivery order or duplicate delivery changed the merged graph")
			}
			for i := 1; i < len(observers); i++ {
				maps.Copy(observers[0].seen, observers[i].seen)
				maps.Copy(observers[0].undone, observers[i].undone)
			}
			observers[0].graph = left
			checkObserverClock(t, 96, &observers[0], actions, branches, replicas)
		})
	}
}

func checkObserverClock(t *testing.T, step int, observer *concurrentObserver, actions []concurrentAction, branches [2]string, replicas [2][6]lineage.ReplicaID) {
	t.Helper()
	for branch, line := range branches {
		var active []int
		places, machines := map[int]bool{0: true}, map[int]bool{0: true}
		returns, rounds, machineMoves, machineReturns, machineRounds := 0, 0, 0, 0, 0
		wantIDs := map[string]bool{}
		for i, action := range actions {
			if !observer.seen[i] || observer.undone[i] || action.branch != branch {
				continue
			}
			active = append(active, i)
			wantIDs[action.hop.ID] = true
			if places[action.to] {
				returns++
			}
			places[action.to] = true
			if action.to == 0 {
				rounds++
			}
			if action.from%3 != action.to%3 {
				machineMoves++
				if machines[action.to%3] {
					machineReturns++
				}
				machines[action.to%3] = true
				if action.to%3 == 0 {
					machineRounds++
				}
			}
		}
		graph := observer.graph.ForBranch(line)
		journey := graph.Journey()
		if journey.Transfers != len(active) || journey.Returns != returns || journey.RoundTrips != rounds || journey.MachineTransfers != machineMoves || journey.MachineReturns != machineReturns || journey.MachineRoundTrips != machineRounds || journey.Fork != (branch == 1) {
			t.Fatalf("step %d branch %d: journey %+v disagrees with observer actions: transfers=%d returns=%d roundTrips=%d machine=%d/%d/%d", step, branch, journey, len(active), returns, rounds, machineMoves, machineReturns, machineRounds)
		}
		actualIDs := map[string]bool{}
		for _, hop := range graph.ActiveHops() {
			if hop.Line == line && !hop.Fork {
				actualIDs[hop.ID] = true
			}
		}
		if !maps.Equal(wantIDs, actualIDs) {
			t.Fatal("active operations disagree with received/undone actions", step, branch)
		}
		for place, replica := range replicas[branch] {
			var arrivals []int
			for _, i := range active {
				if actions[i].to == place {
					arrivals = append(arrivals, i)
				}
			}
			frontier := clockFrontier(actions, arrivals)
			want := map[lineage.ReplicaID]bool{}
			if len(frontier) == 1 {
				// Fork birth was delivered to every observer initially and is
				// never undone by the generated movement compensations.
				if branch == 1 {
					want[replicas[branch][0]] = true
				}
				arrival := frontier[0]
				for _, i := range active {
					if i == arrival || actions[arrival].seen[i] {
						want[replicas[branch][actions[i].from]] = true
						want[replicas[branch][actions[i].to]] = true
					}
				}
				delete(want, replica)
			}
			actual := map[lineage.ReplicaID]bool{}
			for _, candidate := range graph.ReturnReplicas(replica) {
				if actual[candidate.ID] || candidate.Line != line {
					t.Fatal("duplicate or other-branch return candidate", step, branch, place)
				}
				actual[candidate.ID] = true
			}
			if !maps.Equal(want, actual) {
				t.Fatalf("step %d branch %d place %d: return candidates do not match arrival's observed actions; arrivals=%v frontier=%v want=%v got=%v", step, branch, place, arrivals, frontier, want, actual)
			}
			if len(clockFrontier(actions, active)) > 1 {
				if _, ok := graph.Departure(replica); ok {
					t.Fatal("concurrent movements silently chose a departure", step, branch, place)
				}
			}
		}
	}
}

func clockFrontier(actions []concurrentAction, candidates []int) []int {
	var out []int
	for _, candidate := range candidates {
		observed := false
		for _, later := range candidates {
			observed = observed || actions[later].seen[candidate]
		}
		if !observed {
			out = append(out, candidate)
		}
	}
	return out
}
