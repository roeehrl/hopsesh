package app

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
	_ "modernc.org/sqlite"
)

// An idle app rescans the same sessions every minute. The catalog must not rewrite its
// snapshot for that, and must never store the lineage revision graph (it reached tens of
// megabytes per session and was rewritten several times per scan).
func TestIdleRescanWritesNothingAndOmitsRevisionGraph(t *testing.T) {
	a := catalogApp(t)
	key := agent.SessionKey{Agent: "claude", Session: "busy"}
	graph := lineage.New("family")
	graph.Replicas = []lineage.Replica{{ID: "r1", Key: key, Endpoint: "e", Location: "one", Line: graph.Branch}}
	for i := range 2000 {
		graph.Revisions = append(graph.Revisions, lineage.Revision{ID: ir.NodeID(fmt.Sprintf("node-%d", i)), Replica: "r1", Anchor: "a", Hash: strings.Repeat("f", 64)})
	}
	graph.States = []lineage.State{{ID: "s1", Replica: "r1", Projection: make([]ir.Projection, 500)}, {ID: "s2", Replica: "r1", Loss: []string{"images"}}}
	graph.Hops = []lineage.Hop{{ID: "h1", Line: graph.Branch, From: "r1", To: "r1", Target: "s2", Kind: lineage.HopMove}}
	scan := func(at time.Time) *Inventory {
		return &Inventory{Machines: []*Machine{{Name: "one", Status: StatusOK, CheckedAt: at}}, Entries: []Entry{{Machine: "one", ObservedAt: at, Session: agent.Summary{Key: key, Title: "Busy"}, Live: agent.LiveInfo{State: agent.Live}, Lineage: graph}}}
	}
	db, err := sql.Open("sqlite", filepath.Join(config.StateDir(), "catalog", "sessions-v1.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	row := func() (started int64, size int) {
		t.Helper()
		if err := db.QueryRow("SELECT started, length(body) FROM snapshots WHERE scope=?", a.CatalogScope()).Scan(&started, &size); err != nil {
			t.Fatal(err)
		}
		return
	}
	now := time.Now()
	a.saveCatalog(scan(now), now)
	first, size := row()
	if size > 8<<10 {
		t.Fatalf("snapshot is %d bytes: the revision graph was stored", size)
	}
	saved := a.CachedInventory()
	l := saved.Entries[0].Lineage
	if l == nil || len(l.Revisions) != 0 || len(l.Hops) != 1 || l.State("s2").Loss[0] != "images" || len(l.State("s1").Projection) != 0 {
		t.Fatalf("saved lineage is not the browsing outline: %+v", l)
	}
	for i := 1; i <= 5; i++ {
		later := now.Add(time.Duration(i) * time.Minute)
		a.saveCatalog(scan(later), later)
	}
	if again, _ := row(); again != first {
		t.Fatal("an unchanged rescan rewrote the catalog snapshot")
	}
	changed := scan(now.Add(10 * time.Minute))
	changed.Entries[0].Session.Title = "Renamed"
	a.saveCatalog(changed, now.Add(10*time.Minute))
	if again, _ := row(); again == first {
		t.Fatal("a changed session was not saved")
	}
	if got := a.CachedInventory().Entries[0].Session.Title; got != "Renamed" {
		t.Fatalf("saved title %q", got)
	}
}

func TestPlanRefusesSavedRows(t *testing.T) {
	a := catalogApp(t)
	_, _, err := a.Plan(t.Context(), &Inventory{}, Entry{Cached: true, Machine: "one"}, "", a.DefaultOptions())
	if err == nil || !strings.Contains(err.Error(), "refresh one") {
		t.Fatalf("plan from a saved row: %v", err)
	}
}
