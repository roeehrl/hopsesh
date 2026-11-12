package app

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Native history and lineage come from the protected-return fixture. Only live
// detection is intercepted; no process control is needed to simulate a close.
type planLiveModule struct {
	*returnTestModule
	calls   [][]agent.SessionID
	err     error
	unknown bool
	missing bool
	onLive  func()
}

func (m *planLiveModule) Live(ctx context.Context, h agent.Host, in agent.Install, ids []agent.SessionID) (map[agent.SessionID]agent.LiveInfo, error) {
	m.calls = append(m.calls, slices.Clone(ids))
	if m.onLive != nil {
		m.onLive()
	}
	if m.err != nil || m.missing {
		return nil, m.err
	}
	out, err := m.returnTestModule.Live(ctx, h, in, ids)
	if m.unknown {
		out[ids[0]] = agent.LiveInfo{State: agent.Unknown}
	}
	return out, err
}

func planLiveFixture(t *testing.T) (*protectedReturnFixture, *planLiveModule, *Inventory, Entry) {
	t.Helper()
	f := newProtectedReturnFixture(t)
	m := &planLiveModule{returnTestModule: f.m}
	reg, err := registry.New(m)
	if err != nil {
		t.Fatal(err)
	}
	f.a.Reg = reg
	source := Entry{Machine: "here", Agent: "claude", Session: f.in.Session, Lineage: f.in.Lineage, Live: agent.LiveInfo{State: agent.Ended, Status: "source observation"},
		Returns: []ReturnCandidate{{Machine: "here", Key: f.original.Key.String(), Local: true}}}
	original := Entry{Machine: "here", Agent: "claude", Session: f.original, Lineage: f.in.Copies[0].Lineage,
		Live:      agent.LiveInfo{State: agent.Live, PID: 12345, Status: "old live observation"},
		Departure: &MovementNotice{Operation: "outbound", Status: "prepared"}}
	unrelated := original
	unrelated.Session.Key.Session = "unrelated"
	remote := original
	remote.Machine = "another-machine"
	siblingProfile := original
	siblingProfile.Session.Key.Profile = "another-profile"
	inv := &Inventory{Machines: []*Machine{{Name: "here", Local: true, host: f.in.Target.Machine,
		Agents: []AgentState{{Agent: "claude", Install: f.m.install}}}},
		Entries: []Entry{source, original, unrelated, remote, siblingProfile}}
	return f, m, inv, source
}

func TestPlanRechecksSelectedDestinationAfterCloseWithoutRescan(t *testing.T) {
	for _, selector := range []string{"key", "session id"} {
		t.Run(selector, func(t *testing.T) {
			f, m, inv, source := planLiveFixture(t)
			target := f.original.Key.String()
			if selector == "session id" {
				target = string(f.original.Key.Session)
			}
			opt := move.Options{TargetSession: target, TargetDir: source.Session.CWD, Notify: true}
			// First review sees the original open. Closing it changes the module's
			// current evidence, but leaves this complete browsing inventory intact.
			p, in, err := f.a.Plan(context.Background(), inv, source, "claude", opt)
			if err != nil || !move.CanEndDestination(p, in) {
				t.Fatalf("open original lost its end/check gate: %+v %v", p, err)
			}
			f.m.live[f.original.Key.Session] = false
			p, in, err = f.a.Plan(context.Background(), inv, source, "claude", opt)
			if err != nil || len(p.Blockers) != 0 || p.Continue == nil || p.Continue.Relation != move.RelationAppend || p.Placement.Key != f.original.Key {
				t.Fatalf("closed original blocked by old browsing evidence: %+v %v", p, err)
			}
			if move.CanEndDestination(p, in) || p.Options.OpenBlockedOriginal != "" {
				t.Fatal("closed original still offers teardown")
			}
			for _, c := range in.Copies {
				if c.Summary.Key == f.original.Key && c.Live.State != agent.Ended {
					t.Fatal("plan input retained stale destination evidence")
				}
			}
			if len(m.calls) != 2 {
				t.Fatalf("unexpected probes: %v", m.calls)
			}
			for _, ids := range m.calls {
				if !slices.Equal(ids, []agent.SessionID{f.original.Key.Session}) {
					t.Fatalf("probed source, sibling profile or unrelated session: %v", ids)
				}
			}
			if inv.Entries[1].Live.State != agent.Live || inv.Entries[1].Live.PID != 12345 || !reflect.DeepEqual(in.Live, source.Live) {
				t.Fatal("planning mutated published inventory or source evidence")
			}
			if len(f.m.stopped) != 0 || len(f.m.writes) != 0 || !f.m.live["unrelated"] || !bytes.Equal(readReturnFile(t, f.original.Path), f.prefix) {
				t.Fatal("planning stopped a client or wrote native history")
			}
		})
	}
}

func TestPlanDetectsDestinationRestartFromEndedBrowsingEvidence(t *testing.T) {
	f, m, inv, source := planLiveFixture(t)
	inv.Entries[1].Live = agent.LiveInfo{State: agent.Ended}
	// No protected-original override: the fresh writer must block the append.
	source.Returns = nil
	p, in, err := f.a.Plan(context.Background(), inv, source, "claude", move.Options{TargetSession: f.original.Key.String(), TargetDir: source.Session.CWD})
	if err != nil || !slices.Contains(p.Blockers, "the destination copy is open; quit it first") || !move.CanEndDestination(p, in) || len(m.calls) != 1 {
		t.Fatalf("restarted destination not detected: %+v %v", p, err)
	}
	if inv.Entries[1].Live.State != agent.Ended || len(f.m.stopped) != 0 {
		t.Fatal("planning mutated browsing state or stopped the writer")
	}
}

func TestPlanRejectsUnverifiableDestinationAndPropagatesCancellation(t *testing.T) {
	for _, failure := range []string{"probe error", "missing result", "unknown", "cancel before probe", "cancel during probe"} {
		t.Run(failure, func(t *testing.T) {
			f, m, inv, source := planLiveFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch failure {
			case "probe error":
				m.err = errors.New("fixture probe failed")
			case "missing result":
				m.missing = true
			case "unknown":
				m.unknown = true
			case "cancel before probe":
				cancel()
			case "cancel during probe":
				m.onLive = cancel
			}
			p, _, err := f.a.Plan(ctx, inv, source, "claude", move.Options{TargetSession: f.original.Key.String(), TargetDir: source.Session.CWD})
			if err == nil || p != nil {
				t.Fatalf("accepted unverified destination: %+v %v", p, err)
			}
			if m.err != nil && !errors.Is(err, m.err) || strings.HasPrefix(failure, "cancel") && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost probe/cancellation error: %v", err)
			}
			if failure == "cancel before probe" && len(m.calls) != 0 {
				t.Fatal("started probe after cancellation")
			}
			if inv.Entries[1].Live.State != agent.Live || len(f.m.stopped) != 0 || len(f.m.writes) != 0 || !bytes.Equal(readReturnFile(t, f.original.Path), f.prefix) {
				t.Fatal("failed read-only planning mutated the original")
			}
		})
	}
}
