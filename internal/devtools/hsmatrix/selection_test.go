package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMatrixSelectionRejectsFalseGreenInputs(t *testing.T) {
	for _, tc := range []struct {
		name, only, shard string
		strength          int
	}{
		{name: "zero strength", strength: 0},
		{name: "excess strength", strength: 9},
		{name: "unknown operation", strength: 2, only: "rountrip"},
		{name: "mixed typo", strength: 2, only: "skill,rountrip"},
		{name: "unknown number", strength: 2, only: "999999"},
		{name: "empty selector", strength: 2, only: "skill,"},
		{name: "shard trailing text", strength: 2, shard: "1/4garbage"},
		{name: "extra slash", strength: 2, shard: "1/4/2"},
		{name: "zero shard", strength: 2, shard: "0/4"},
		{name: "empty shard", strength: 2, shard: "99999/99999"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, _, err := selectedRows(tc.strength, 1, false, tc.only, tc.shard)
			if err == nil || len(rows) != 0 {
				t.Fatal("invalid selection could report successful execution", rows, err)
			}
		})
	}
}

func TestMatrixSelectionReportsOnlyActualCoverage(t *testing.T) {
	rows, coverage, err := selectedRows(2, 1, false, "", "")
	if err != nil || !coverage.Complete || coverage.GeneratedRows != len(rows) || coverage.SelectedRows != len(rows) || coverage.ValidInteractions == 0 || coverage.SelectedInteractions != coverage.ValidInteractions {
		t.Fatal("full model coverage not reported", coverage, err)
	}
	if !reflect.DeepEqual(coverage.Factors, []string{"op", "agents", "content", "repo", "naming", "location"}) {
		t.Fatal("report must name the factors it actually covers", coverage)
	}
	filtered, partial, err := selectedRows(2, 1, false, "skill", "")
	if err != nil || partial.Complete || partial.SelectedRows != len(filtered) || partial.GeneratedRows != len(rows) || partial.SelectedInteractions >= partial.ValidInteractions {
		t.Fatal("partial selection misrepresented as full coverage", partial, err)
	}
	seen := map[int]bool{}
	for _, shard := range []string{"1/2", "2/2"} {
		part, meta, err := selectedRows(2, 1, false, "", shard)
		if err != nil || meta.SelectedRows != len(part) || meta.Shard != shard {
			t.Fatal("invalid shard metadata", meta, err)
		}
		for _, row := range part {
			if seen[row.N] {
				t.Fatal("shards repeat a row", row.N)
			}
			seen[row.N] = true
		}
	}
	if len(seen) != len(rows) {
		t.Fatal("shards lost generated rows")
	}
}

func TestEmptyMatrixRunFailsBeforeTouchingMachines(t *testing.T) {
	out := filepath.Join(t.TempDir(), "must-not-be-created")
	before := os.Getenv("HOPSESH_CONFIG_DIR")
	if code := runMain([]string{"-only", "not-a-real-operation", "-out", out, "-there", "must-not-connect.invalid"}); code != 2 {
		t.Fatal("invalid matrix did not fail with usage error", code)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("invalid selection created fixture or result files", err)
	}
	if os.Getenv("HOPSESH_CONFIG_DIR") != before {
		t.Fatal("invalid selection changed the execution namespace")
	}
	_, _, err := selectedRows(2, 1, false, "skill", "999/999")
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatal("empty shard must explicitly report no scenarios", err)
	}
}

func TestUnsupportedCloudFixturesNeverCountAsPassed(t *testing.T) {
	var results []result
	for _, op := range []string{"fetch", "handoff", "cloud-roundtrip", "cloud-hop"} {
		row := Row{Op: op}
		err := scenarioSupport(row, "windows")
		res := rowOutcome(err)
		if err == nil || res.OK || res.Status != "unsupported" || res.Reason == "" {
			t.Fatal("unexecuted Windows cloud fixture reported as passed", res)
		}
		results = append(results, res)
		if err = scenarioSupport(row, "linux"); err != nil {
			t.Fatal("supported native fixture was excluded", err)
		}
	}
	if err := scenarioSupport(Row{Op: "repeat-roundtrip"}, "windows"); err != nil {
		t.Fatal("Windows native movement is supported", err)
	}
	results = append(results, rowOutcome(nil), rowOutcome(errors.New("real failure")))
	passed, failed, unsupported := resultCounts(results)
	if passed != 1 || failed != 1 || unsupported != 4 {
		t.Fatal("unsupported rows entered the pass count", passed, failed, unsupported)
	}
	output := grid("qualification", results)
	if !strings.Contains(output, "1 passed, 1 failed, 4 unsupported") || strings.Count(output, "✅") != 1 || !strings.Contains(output, "Unsupported:") {
		t.Fatal("summary conceals unexecuted rows", output)
	}
}
