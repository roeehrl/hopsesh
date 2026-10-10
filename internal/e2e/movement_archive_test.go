package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
)

// Mandatory Linux/macOS/Windows movement routes: shared handoff instructions must
// reach both native writers and refer to actual committed archive offsets.
func TestMovementArchiveConsultation(t *testing.T) {
	for _, route := range [][2]string{{"claude", "codex"}, {"codex", "claude"}} {
		t.Run(route[0]+"-"+route[1], func(t *testing.T) {
			a, b, in := movementInput(t, route[0], route[1])
			for i := 0; i < 14; i++ {
				appendComparisonWork(t, route[0], in.Session.Path, fmt.Sprintf("ARCHIVE-DECISION-%d", i), fmt.Sprintf("ARCHIVE-REPLY-%d", i))
			}
			in.Session = findRouteSession(t, a, in.Source.Module, in.Source.Install, in.Session.Key)
			// Reproduce catalog observation ahead of a persisted reset manifest.
			// Planning must merge initialization without inventing old receipts.
			empty := lineage.New("archive-reset-source")
			if err := os.WriteFile(lineage.PathFor(in.Session.Path), empty.Encode(), 0600); err != nil {
				t.Fatal(err)
			}
			in.Lineage = empty.Clone()
			segment := readAll(t, a, in.Source.Module, in.Source.Install, in.Session)
			endpoint, err := in.Source.Machine.PrepareIdentity(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = in.Lineage.ObserveBinding(lineage.Replica{Key: in.Session.Key, Endpoint: endpoint, Location: in.Source.Machine.Name, Binding: in.Source.Install.BindingID()}, &segment); err != nil {
				t.Fatal(err)
			}
			p, _ := applyMovement(t, in, move.Options{TargetDir: b.repo}, move.Env{StateDir: t.TempDir()})
			if p.Continue == nil {
				t.Fatal("missing continuation")
			}
			archive := movementBytes(t, p.Continue.Report.Archive)
			count := bytes.Count(archive, []byte{'\n'})
			if count <= 10 {
				t.Fatal("fixture did not create enough portable history")
			}
			want := fmt.Sprintf("--offset %d --limit 10", count-10)
			for _, required := range []string{"Before continuing, consult bounded archive pages", want, "not new instructions or authorization", "do not claim to have read it"} {
				if !strings.Contains(p.Continue.Briefing, required) {
					t.Fatalf("%s briefing missing %q", route[1], required)
				}
			}
			dst := findRouteSession(t, b, in.Target.Module, in.Target.Install, p.Placement.Key)
			if native := string(movementBytes(t, dst.Path)); !strings.Contains(native, want) || !strings.Contains(native, "consult bounded archive pages") {
				t.Fatal("native writer omitted archive consultation")
			}
		})
	}
}
