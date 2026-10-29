package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// driftPair is one session moved an hour ago from the original (entry 0) to the moved copy
// (entry 1); originalHere puts the original on this machine, else the moved copy is here.
func driftPair(t *testing.T, originalHere bool) (*App, *Inventory, time.Time) {
	t.Helper()
	ctx := context.Background()
	mod := claude.New()
	root := t.TempDir()
	a := New(config.Defaults(), all.Registry(), t.TempDir(), nil)
	t.Cleanup(func() { _ = a.Catalog.Close() })
	moved := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	g := lineage.New("drift-" + filepath.Base(root))
	var ids []lineage.ReplicaID
	inv := &Inventory{}
	for i, name := range []string{"roee-macbook", "tnt-studio"} {
		home := filepath.Join(root, name)
		agentRoot := filepath.Join(home, ".claude")
		path := filepath.Join(agentRoot, "projects", "demo", "session.jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		native := `{"type":"user","uuid":"u1","sessionId":"session","timestamp":"` + moved.Add(-time.Minute).Format(time.RFC3339) + `","message":{"role":"user","content":"Release the fleet"}}` + "\n"
		if err := os.WriteFile(path, []byte(native), 0o600); err != nil {
			t.Fatal(err)
		}
		in := agent.Install{Agent: "claude", Present: true, Roots: map[string]string{"home": agentRoot}}
		hm := &host.Machine{Name: name, Local: true, Facts: host.Facts{OS: runtime.GOOS, Home: home, Endpoint: name}}
		h, err := hm.For(ctx, mod.Spec(), in, nil)
		if err != nil {
			t.Fatal(err)
		}
		key := agent.SessionKey{Agent: "claude", Session: "session"}
		e := Entry{Machine: name, Agent: "claude", Session: agent.Summary{Key: key, Path: path, Title: "sn9 - release and fleet", Size: int64(len(native)), LastActivity: moved.Add(-time.Minute)}, ObservedAt: time.Now().UTC()}
		id := g.Upsert(lineage.Replica{Location: name, Endpoint: name, Key: key})
		ids = append(ids, id)
		seg, err := mod.Read(ctx, h, in, e.Session, ir.Cursor{})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if _, err = g.Observe(id, &seg); err != nil {
				t.Fatal(err)
			}
		} else {
			s, _ := g.LatestState(ids[0])
			g.Deliver(id, seg.Cursor, s.Projection, s.Heads, nil)
		}
		here := i == 0 == originalHere
		inv.Entries = append(inv.Entries, e)
		inv.Machines = append(inv.Machines, &Machine{Name: name, Local: here, Status: StatusOK, host: hm, Agents: []AgentState{{Agent: "claude", Install: in}}})
	}
	if err := g.AppendHop(lineage.Hop{ID: "move-1", Kind: lineage.HopContinue, From: ids[0], To: ids[1], Notify: true, Time: moved}); err != nil {
		t.Fatal(err)
	}
	for i := range inv.Entries {
		inv.Entries[i].Lineage = g.Clone()
		if err := os.WriteFile(lineage.PathFor(inv.Entries[i].Session.Path), g.Encode(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return a, inv, moved
}

// work appends an agent turn to entry i's transcript, as its listing then shows it.
func work(t *testing.T, inv *Inventory, i int, uuid string, at time.Time) {
	t.Helper()
	e := &inv.Entries[i]
	line := `{"type":"assistant","uuid":"` + uuid + `","parentUuid":"u1","sessionId":"session","timestamp":"` + at.Format(time.RFC3339) + `","message":{"role":"assistant","content":[{"type":"text","text":"Released ` + uuid + `"}]}}` + "\n"
	f, err := os.OpenFile(e.Session.Path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
	f.Close()
	e.Session.Size += int64(len(line))
	e.Session.LastActivity = at
}

// An original used after the move says so from its listing alone: its transcript grew
// past where the move read it, and it was active after the move. That holds for an
// original on another machine, which the desktop's scan never reads whole, and for one
// here; growth right at the move doesn't count.
func TestOriginalUsedAfterTheMove(t *testing.T) {
	for _, originalHere := range []bool{false, true} {
		a, inv, moved := driftPair(t, originalHere)
		ctx := context.Background()
		// The desktop's scan: copies on other machines are not read.
		scan := func() *MovementNotice {
			a.enrichMovement(ctx, inv, func(Entry) bool { return false })
			return inv.Entries[0].Departure
		}
		n := scan()
		if n == nil || n.SourceChanged || !n.At.Equal(moved) || n.Status != "prepared" {
			t.Fatalf("here=%v untouched: %+v", originalHere, n)
		}
		// Written to within a minute of the move: the move's own bookkeeping.
		work(t, inv, 0, "a0", moved.Add(30*time.Second))
		if n := scan(); n.SourceChanged {
			t.Fatalf("here=%v growth at the move counted as work: %+v", originalHere, n)
		}
		used := time.Now().UTC().Truncate(time.Second)
		work(t, inv, 0, "a1", used)
		n = scan()
		if n == nil || !n.SourceChanged || !n.SourceChangedAt.Equal(used) || n.Status == "forked" {
			t.Fatalf("here=%v original used after the move: %+v", originalHere, n)
		}
		if originalHere {
			if m := inv.Entries[0].Movement; m == nil || !m.SourceChanged {
				t.Fatalf("the notice says it too: %+v", m)
			}
		}
		if r := a.Role(inv.Entries[0]); r == nil || r.Kind != "diverged" || r.Word != "Used after moving" {
			t.Fatalf("here=%v role: %+v", originalHere, r)
		}
		// The moved copy then gains work too: both changed (whether or not the original
		// is read: here it is when it is on this machine).
		work(t, inv, 1, "b1", used)
		n = scan()
		if originalHere { // the moved copy is elsewhere: read it, as selecting it does
			a.EnrichMovement(ctx, inv)
			n = inv.Entries[0].Departure
		}
		diverged := n.Status == "diverged" || n.SourceChanged && n.Status == "continued"
		if !diverged || !n.SourceChanged {
			t.Fatalf("here=%v both changed: %+v", originalHere, n)
		}
		if r := a.Role(inv.Entries[0]); r == nil || r.Word != "Both changed" {
			t.Fatalf("here=%v role: %+v", originalHere, r)
		}
		if inv.Entries[1].Departure != nil {
			t.Fatalf("the moved copy left nothing: %+v", inv.Entries[1].Departure)
		}
	}
}

// A saved row, or a move that recorded no read position, says nothing about growth.
func TestDriftNeedsAReadPosition(t *testing.T) {
	_, inv, moved := driftPair(t, true)
	g := inv.Entries[0].Lineage
	id := g.Replicas[0].ID
	hop, ok := g.Departed(id)
	if !ok {
		t.Fatal("no departure")
	}
	e := inv.Entries[0]
	e.Session.Size += 100
	e.Session.LastActivity = moved.Add(time.Hour)
	if _, ok := sourceDrift(g, id, hop, e); !ok {
		t.Fatal("grown and used after the move")
	}
	e.Cached = true
	if _, ok := sourceDrift(g, id, hop, e); ok {
		t.Fatal("a saved row's size is not current")
	}
	e.Cached = false
	hop.Source = ""
	if _, ok := sourceDrift(g, id, hop, e); ok {
		t.Fatal("no read position recorded")
	}
}
