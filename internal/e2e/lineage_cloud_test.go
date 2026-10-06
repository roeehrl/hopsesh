package e2e

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
)

func TestLineageCloudForkAndIdempotentHandoff(t *testing.T) {
	w := newHandoffWorld(t)
	a := w.app()
	ctx := context.Background()
	opt := a.HandoffDefaults("claude-cloud")
	opt.Fork = true
	opt.OperationID = "stable-cloud-fork"
	inv, p := w.planHandoff(a, opt)
	defer inv.Close()
	original := findEntry(t, inv).Session.Path
	before, _ := os.ReadFile(original)
	first, err := a.Apply(ctx, p, move.Input{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	log, _ := os.ReadFile(os.Getenv("FAKE_AGENT_LOG"))
	again, err := a.Apply(ctx, p, move.Input{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	afterLog, _ := os.ReadFile(os.Getenv("FAKE_AGENT_LOG"))
	if first.Journal != again.Journal || first.Handoff.Session != again.Handoff.Session || string(log) != string(afterLog) {
		t.Fatal("retry sent another cloud operation")
	}
	after, _ := os.ReadFile(original)
	if string(before) != string(after) {
		t.Fatal("fork marked or changed its parent")
	}
	m, err := lineage.Read(host.LocalFS(), original)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Branches) != 2 {
		t.Fatalf("expected independent cloud fork, got %d branches", len(m.Branches))
	}
	hop := m.OrderedHops()[0]
	if !hop.Fork || m.Replica(hop.To).Line == m.Branch || m.Journey().Transfers != 0 {
		t.Fatalf("fork advanced parent's route: %+v", hop)
	}
	child := m.ForBranch(m.Replica(hop.To).Line)
	if !child.Journey().Fork || child.Journey().Transfers != 0 {
		t.Fatal("fork origin counters incorrect")
	}
}

func TestLineageCloudUncertainSendNeverRetriesVendor(t *testing.T) {
	w := newHandoffWorld(t)
	a := w.app()
	ctx := context.Background()
	opt := a.HandoffDefaults("claude-cloud")
	opt.OperationID = "uncertain-cloud-send"
	inv, p := w.planHandoff(a, opt)
	defer inv.Close()
	t.Setenv("FAKE_CLOUD_FAIL", "repo-mismatch")
	if _, err := a.Apply(ctx, p, move.Input{}, nil); err == nil {
		t.Fatal("expected driver refusal")
	}
	before, _ := os.ReadFile(os.Getenv("FAKE_AGENT_LOG"))
	t.Setenv("FAKE_CLOUD_FAIL", "")
	if _, err := a.Apply(ctx, p, move.Input{}, nil); err == nil || !strings.Contains(err.Error(), "pending") {
		t.Fatalf("uncertain send must require reconciliation: %v", err)
	}
	after, _ := os.ReadFile(os.Getenv("FAKE_AGENT_LOG"))
	if string(before) != string(after) {
		t.Fatal("retry invoked vendor twice")
	}
}
