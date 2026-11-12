package app

import (
	"context"
	"encoding/json"
	"fmt"
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

// movedPair is one session moved between two machines, both read in this test.
func movedPair(t *testing.T) (*App, *Inventory) { return movedPairSized(t, 0) }

// movedPairSized adds extra turns to the transcript, for a large lineage graph.
func movedPairSized(t testing.TB, turns int) (*App, *Inventory) {
	t.Helper()
	ctx := context.Background()
	mod := claude.New()
	root := t.TempDir()
	a := New(config.Defaults(), all.Registry(), t.TempDir(), nil)
	t.Cleanup(func() { _ = a.Catalog.Close() })
	g := lineage.New("idle-" + filepath.Base(root))
	var entries []Entry
	var machines []*Machine
	var ids []lineage.ReplicaID
	for i, name := range []string{"alice-desktop", "bob-laptop"} {
		home := filepath.Join(root, name)
		agentRoot := filepath.Join(home, ".claude")
		path := filepath.Join(agentRoot, "projects", "demo", "session.jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		native := `{"type":"user","uuid":"u1","sessionId":"session","message":{"role":"user","content":"Fix tests"}}` + "\n"
		for n := range turns {
			native += fmt.Sprintf(`{"type":"assistant","uuid":"t%d","parentUuid":"%s","sessionId":"session","message":{"role":"assistant","content":[{"type":"text","text":"step %d of the work, with enough words to look like a real turn"}]}}`+"\n", n, parent(n), n)
		}
		if err := os.WriteFile(path, []byte(native), 0600); err != nil {
			t.Fatal(err)
		}
		in := agent.Install{Agent: "claude", Present: true, Roots: map[string]string{"home": agentRoot}}
		hm := &host.Machine{Name: name, Local: true, Facts: host.Facts{OS: runtime.GOOS, Home: home, Endpoint: name}}
		h, err := hm.For(ctx, mod.Spec(), in, nil)
		if err != nil {
			t.Fatal(err)
		}
		key := agent.SessionKey{Agent: "claude", Session: "session"}
		e := Entry{Machine: name, Agent: "claude", Session: agent.Summary{Key: key, Path: path, Title: "Session on " + name}, ObservedAt: time.Now().UTC()}
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
		entries = append(entries, e)
		machines = append(machines, &Machine{Name: name, Local: i == 0, Status: StatusOK, host: hm, Agents: []AgentState{{Agent: "claude", Install: in}}})
	}
	if err := g.AppendHop(lineage.Hop{ID: "native-move", Kind: lineage.HopMove, From: ids[0], To: ids[1], Notify: true}); err != nil {
		t.Fatal(err)
	}
	for i := range entries {
		entries[i].Lineage = g.Clone()
		if err := os.WriteFile(lineage.PathFor(entries[i].Session.Path), g.Encode(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return a, &Inventory{Entries: entries, Machines: machines}
}

func parent(n int) string {
	if n == 0 {
		return "u1"
	}
	return fmt.Sprintf("t%d", n-1)
}

func movementView(inv *Inventory) string {
	type view struct {
		Returns   []ReturnCandidate
		Departure *MovementNotice
		Arrival   *Arrival
	}
	var out []view
	for _, e := range inv.Entries {
		d := e.Departure
		if d != nil {
			c := *d
			c.CheckedAt, c.Text = time.Time{}, ""
			d = &c
		}
		out = append(out, view{e.Returns, d, e.Arrival})
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// Refreshing unchanged sessions must not open them: no transcript or sidecar is read
// again and the result is the same. When one copy grows, only that copy is read.
func TestUnchangedRefreshReadsNoContent(t *testing.T) {
	ctx := context.Background()
	a, inv := movedPair(t)
	a.EnrichMovement(ctx, inv)
	first := movementView(inv)
	before := contentReads.Load()
	for range 3 {
		a.EnrichMovement(ctx, inv)
	}
	if n := contentReads.Load() - before; n != 0 {
		t.Fatalf("unchanged refreshes read content %d times", n)
	}
	if got := movementView(inv); got != first {
		t.Fatalf("a recalled refresh changed the result:\n%s\n%s", first, got)
	}
	f, err := os.OpenFile(inv.Entries[1].Session.Path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"type":"assistant","uuid":"a1","parentUuid":"u1","sessionId":"session","message":{"role":"assistant","content":[{"type":"text","text":"Tests fixed"}]}}` + "\n")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	before = transcriptReads.Load()
	a.EnrichMovement(ctx, inv)
	if n := transcriptReads.Load() - before; n != 1 {
		t.Fatalf("one grown copy caused %d reads, want 1", n)
	}
	if got := inv.Entries[1].Returns; len(got) != 1 || got[0].Status != "available" {
		t.Fatalf("new work not seen after a recalled refresh: %+v", got)
	}
}

// A full rescan of an unchanged machine writes nothing to the catalog and reads no
// session content: the catalog database and its journal are left exactly as they were.
func TestIdleRescanTouchesNothing(t *testing.T) {
	a := catalogApp(t)
	ctx := context.Background()
	opts := ScanOptions{Hosts: []string{LocalName()}, SkipGit: true, Progress: func(ScanUpdate) {}}
	a.Scan(ctx, opts).Close()
	a.Scan(ctx, opts).Close() // settle: first scans save summaries and the snapshot
	dir := filepath.Join(config.StateDir(), "catalog")
	stat := func() map[string]string {
		out := map[string]string{}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if info, err := e.Info(); err == nil && !e.IsDir() {
				out[e.Name()] = info.ModTime().String() + "/" + string(rune(info.Size()))
			}
		}
		return out
	}
	files, reads := stat(), contentReads.Load()
	for range 3 {
		a.Scan(ctx, opts).Close()
	}
	if n := contentReads.Load() - reads; n != 0 {
		t.Fatalf("idle rescans read session content %d times", n)
	}
	after := stat()
	for name, v := range files {
		if after[name] != v && name != "sessions-v1.sqlite-shm" {
			t.Fatalf("idle rescans wrote %s", name)
		}
	}
}

// An unchanged refresh of a large moved session allocates almost nothing: it must not
// parse, clone or rehash the graph (thousands of revisions) or the transcript again.
func TestUnchangedRefreshAllocationBudget(t *testing.T) {
	ctx := context.Background()
	a, inv := movedPairSized(t, 5000)
	a.EnrichMovement(ctx, inv)
	if n := len(inv.Entries[0].Lineage.Revisions); n < 5000 {
		t.Fatalf("graph has %d revisions, want a large one", n)
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	refreshes := uint64(5)
	if os.Getenv("HS_REFRESHES") != "" {
		refreshes = 25
	}
	for range refreshes {
		a.EnrichMovement(ctx, inv)
	}
	runtime.ReadMemStats(&after)
	perRefresh := (after.TotalAlloc - before.TotalAlloc) / refreshes
	// Rereading this session costs several megabytes; recalling it, tens of kilobytes.
	const budget = 512 << 10
	if perRefresh > budget {
		t.Fatalf("an unchanged refresh allocated %d KiB, budget %d KiB", perRefresh>>10, budget>>10)
	}
	t.Logf("unchanged refresh: %d KiB allocated", perRefresh>>10)
}
