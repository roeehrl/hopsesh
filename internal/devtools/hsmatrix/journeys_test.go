package main

import (
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestJourneyRouteCoverage(t *testing.T) {
	seen := map[string]bool{}
	edges := map[string]bool{}
	for _, c := range journeyCases() {
		if seen[c.Name] {
			t.Fatal("duplicate case", c.Name)
		}
		seen[c.Name] = true
		for i := 1; i < len(c.Route); i++ {
			edges[c.Route[i-1:i+1]] = true
		}
	}
	if len(seen) != 24 {
		t.Fatalf("missing route/agent/fork combinations: %d", len(seen))
	}
	for _, edge := range []string{"AB", "AC", "BA", "BC", "CA", "CB"} {
		if !edges[edge] {
			t.Fatal("missing directed edge", edge)
		}
	}
}

func TestExpectedJourneyCounts(t *testing.T) {
	for _, c := range []struct {
		route                      string
		fork                       bool
		transfers, returns, rounds int
	}{
		{"ABCA", false, 3, 1, 1},
		{"ABCBCAB", false, 6, 4, 1},
		{"ABCACBA", false, 6, 4, 2},
		{"ABCA", true, 2, 0, 0},
		{"ABCBCAB", true, 5, 3, 2},
		{"ABCACBA", true, 5, 3, 1},
	} {
		a, b, d := expectedJourney(c.route, c.fork)
		if a != c.transfers || b != c.returns || d != c.rounds {
			t.Fatalf("%+v: %d/%d/%d", c, a, b, d)
		}
	}
}

// Construct receipts separately from the CLI runner. These are tests of the
// qualification oracle, not claims of native multi-machine execution.
func journeyGraph(t *testing.T, c journeyCase) *lineage.Manifest {
	t.Helper()
	g := lineage.New("fixture-family")
	var previous lineage.ReplicaID
	for i := 0; i < len(c.Route); i++ {
		p := int(c.Route[i] - 'A')
		branch := g.Branch
		if i == 1 && c.Fork {
			branch = g.Fork("fork", nil)
		}
		next := g.Upsert(lineage.Replica{Line: branch, Endpoint: string(c.Route[i]), Location: string(c.Route[i]), Key: agent.SessionKey{Agent: agent.ID(c.Agents[p]), Session: agent.SessionID(fmt.Sprintf("copy-%d", i))}})
		if i != 0 {
			if err := g.AppendHop(lineage.Hop{ID: fmt.Sprintf("hop-%d", i), From: previous, To: next, Fork: i == 1 && c.Fork, Kind: lineage.HopMove, Notify: true}); err != nil {
				t.Fatal(err)
			}
		}
		if i == 1 && c.Fork {
			g = g.ForBranch(branch)
		}
		previous = next
	}
	return g
}

func TestJourneyOracleRejectsIncompleteDuplicatedAndCollapsedRoutes(t *testing.T) {
	for _, c := range journeyCases() {
		t.Run(c.Name, func(t *testing.T) {
			g := journeyGraph(t, c)
			if err := checkJourney(g, c.Route, c.Fork); err != nil {
				t.Fatal(err)
			}
			slices.Reverse(g.Hops)
			if err := checkJourney(g, c.Route, c.Fork); err != nil {
				t.Fatal("storage order changed result", err)
			}
			for _, mutation := range []struct {
				name   string
				change func(*lineage.Manifest)
			}{
				{"missing hop", func(m *lineage.Manifest) { m.Hops = m.Hops[1:] }},
				{"duplicate hop", func(m *lineage.Manifest) { h := m.Hops[0]; h.ID = "duplicate-effect"; m.Hops = append(m.Hops, h) }},
				{"collapsed machines", func(m *lineage.Manifest) {
					for i := range m.Replicas {
						m.Replicas[i].Endpoint = "one-machine"
					}
				}},
			} {
				bad := g.Clone()
				mutation.change(bad)
				if err := checkJourney(bad, c.Route, c.Fork); err == nil {
					t.Fatal("accepted", mutation.name)
				}
			}
		})
	}
}

func TestMatrixCommandRejectsOtherExecutablesAndEmptyRequests(t *testing.T) {
	for _, args := range [][]string{nil, {"sh", "-c", "echo unsafe"}, {"settings", "set", "relay.enabled", "true"}} {
		if _, err := matrixCommand(commandReq{Args: args}); err == nil {
			t.Fatal("accepted unrelated command", args)
		}
	}
}

func TestJourneySourceRefusesMissingStaleAndDirtyBinaries(t *testing.T) {
	valid := map[string]string{"helperRevision": "current", "appRevision": "current", "helperModified": "false", "appModified": "false"}
	if err := verifyJourneySource(valid, "current"); err != nil {
		t.Fatal(err)
	}
	for key := range valid {
		for _, value := range []string{"", "old", "true"} {
			bad := maps.Clone(valid)
			bad[key] = value
			if err := verifyJourneySource(bad, "current"); err == nil {
				t.Fatalf("accepted %s=%q", key, value)
			}
		}
	}
	if err := verifyJourneySource(map[string]string{}, ""); err == nil {
		t.Fatal("accepted missing source identity")
	}
}
