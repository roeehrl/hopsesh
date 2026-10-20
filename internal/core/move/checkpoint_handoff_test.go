package move

import (
	"slices"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func checkpointHandoffFixture(t *testing.T, fork bool) (*lineage.Manifest, lineage.Replica, CheckpointHandoff) {
	t.Helper()
	m := lineage.New("local-handoff-family")
	local := m.Upsert(lineage.Replica{Endpoint: "machine-A", Location: "A", Key: agent.SessionKey{Agent: "claude", Session: "local-original"}})
	seg := checkpointSegment("original request", "original result")
	st, err := m.Observe(local, &seg)
	if err != nil {
		t.Fatal(err)
	}
	cloud := m.Upsert(lineage.Replica{Endpoint: "cloud:claude-cloud:account", Location: "claude-cloud", Key: agent.SessionKey{Agent: "claude", Session: "outer-vendor-task"}})
	m.Deliver(cloud, ir.Cursor{}, nil, st.Heads, []string{"handoff briefing; native reasoning stays at source"})
	if err := m.AppendHop(lineage.Hop{ID: "original-handoff", Time: time.Now(), From: local, To: cloud, Kind: lineage.HopHandoff}); err != nil {
		t.Fatal(err)
	}
	if fork {
		m.Branch = m.Fork("task-independent-fork", st.Heads)
	}
	source := lineage.Replica{Endpoint: "cloud:claude-hosted:task-A", Binding: "task-A", Line: m.Branch, Location: "Claude cloud", Key: agent.SessionKey{Agent: "claude", Profile: "task-A", Session: "native-incarnation-one"}}
	origin := CheckpointHandoff{Task: "task-A", Operation: "original-handoff", CloudReplica: cloud, Brief: "Exact saved handoff briefing", Fork: fork, Time: time.Now()}
	return m, source, origin
}

func TestCheckpointHandoffIdentityIsCausalButNeverAnotherTransfer(t *testing.T) {
	for _, fork := range []bool{false, true} {
		t.Run(map[bool]string{false: "original", true: "independent-fork"}[fork], func(t *testing.T) {
			m, source, origin := checkpointHandoffFixture(t, fork)
			seg := checkpointSegment(origin.Brief, "cloud-only work")
			if err := seedCheckpointHandoff(m, source, &seg, origin); err != nil {
				t.Fatal(err)
			}
			id := m.Upsert(source)
			st, err := m.Observe(id, &seg)
			if err != nil {
				t.Fatal(err)
			}
			if !seg.Nodes[0].Generated || len(m.Revisions) != 3 || len(st.Loss) == 0 {
				t.Fatal("briefing became new work or lost the conversion warning")
			}
			vendor, _ := m.LatestState(origin.CloudReplica)
			if len(vendor.Projection) != 0 {
				t.Fatal("outer vendor task acquired a fabricated native transcript")
			}
			association := m.Hops[len(m.Hops)-1]
			if len(m.ActiveHops()) != 1 || len(m.ReturnReplicas(id)) != 0 {
				t.Fatal("identity association exposed a fabricated movement or return destination")
			}
			if association.Kind != lineage.HopIdentity || !slices.Contains(association.Parents, origin.Operation) {
				t.Fatal("association lost handoff ancestry")
			}
			if err := seedCheckpointHandoff(m, source, &seg, origin); err != nil || len(m.Hops) != 2 {
				t.Fatal("review retry duplicated identity event", err)
			}
			to := m.Upsert(lineage.Replica{Endpoint: "machine-A", Location: "A", Key: agent.SessionKey{Agent: "claude", Session: "returned-copy"}})
			m.Deliver(to, ir.Cursor{}, nil, st.Heads, st.Loss)
			if err := m.AppendHop(lineage.Hop{ID: "checkpoint-return", Time: time.Now(), From: id, To: to, Kind: lineage.HopContinue}); err != nil {
				t.Fatal(err)
			}
			if err := m.Validate(); err != nil {
				t.Fatal(err)
			}
			journey := m.Journey()
			if !fork && (journey.Transfers != 2 || journey.RoundTrips != 1) {
				t.Fatal("identity event counted as another trip", journey)
			}
			if fork && (journey.Transfers != 1 || journey.RoundTrips != 0 || !journey.Fork) {
				t.Fatal("fork inherited parent's travel counts", journey)
			}
			// A rebuilt native ID inherits the task's exact prefix without another
			// identity event and without importing the parent's unrelated branches.
			source.Key.Session = "native-incarnation-two"
			next := checkpointSegment(origin.Brief, "cloud-only work", "after rebuild")
			if err := seedCheckpointHandoff(m, source, &next, origin); err != nil {
				t.Fatal(err)
			}
			if err := seedCheckpointPrefix(m, source, &next); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Observe(m.Upsert(source), &next); err != nil {
				t.Fatal(err)
			}
			if len(m.Hops) != 3 || len(m.Revisions) != 4 {
				t.Fatal("rebuild duplicated ancestry or content")
			}
		})
	}
}

func TestCheckpointHandoffRefusesGuessedIdentityAndChangedBrief(t *testing.T) {
	for _, mode := range []string{"edited briefing", "similar native ID", "wrong task", "wrong agent", "wrong receipt", "undone", "fork mismatch"} {
		t.Run(mode, func(t *testing.T) {
			m, source, origin := checkpointHandoffFixture(t, false)
			seg := checkpointSegment(origin.Brief, "cloud work")
			switch mode {
			case "edited briefing":
				seg.Nodes[0].Text += " changed"
			case "similar native ID":
				source.Key.Session = m.Replica(origin.CloudReplica).Key.Session
				seg.Nodes[0].Text = "unrelated task"
			case "wrong task":
				origin.Task = "other-task"
			case "wrong agent":
				source.Key.Agent = "codex"
			case "wrong receipt":
				origin.Operation = "another-handoff"
			case "undone":
				if err := m.UndoOperation(origin.Operation); err != nil {
					t.Fatal(err)
				}
			case "fork mismatch":
				origin.Fork = true
			}
			if err := seedCheckpointHandoff(m, source, &seg, origin); err == nil {
				t.Fatal("unverified handoff ancestry accepted")
			}
		})
	}
}

func TestCheckpointHandoffContinuesOnlyTaskOwnedRewriteBranches(t *testing.T) {
	for _, fork := range []bool{false, true} {
		for _, mode := range []string{"same task", "local copy", "other task", "other profile", "sibling"} {
			if mode == "sibling" && !fork {
				continue // Every valid branch descends from the family root.
			}
			t.Run(map[bool]string{false: "original", true: "fork"}[fork]+"/"+mode, func(t *testing.T) {
				m, source, origin := checkpointHandoffFixture(t, fork)
				seg := checkpointSegment(origin.Brief, "cloud work")
				if err := seedCheckpointHandoff(m, source, &seg, origin); err != nil {
					t.Fatal(err)
				}
				state, err := m.Observe(m.Upsert(source), &seg)
				if err != nil {
					t.Fatal(err)
				}
				for depth := range 2 {
					if mode == "sibling" && depth == 0 {
						// A valid sibling of the linked task cannot acquire its
						// association, even with the same task-shaped origin.
						for _, branch := range m.Branches {
							if branch.ID == source.Line {
								m.Branch = branch.Parent
								break
							}
						}
					}
					line := m.Fork("reviewed-rewrite-"+string(rune('0'+depth)), state.Heads)
					m.Branch, source.Line = line, line
					branchOwner := source
					switch mode {
					case "local copy":
						branchOwner.Endpoint = "local-machine"
					case "other task":
						branchOwner.Binding = "other-task"
					case "other profile":
						branchOwner.Key.Profile = "other-profile"
					}
					m.Upsert(branchOwner)
					if err := m.Validate(); err != nil {
						t.Fatal("invalid branch fixture", err)
					}
					before := len(m.Hops)
					err := seedCheckpointHandoff(m, source, &seg, origin)
					if mode == "same task" && (err != nil || len(m.Hops) != before) {
						t.Fatal("reviewed task rewrite lost its historical association", err)
					}
					if mode != "same task" && err == nil {
						t.Fatal("unrelated branch adopted task ancestry")
					}
				}
			})
		}
	}
}
