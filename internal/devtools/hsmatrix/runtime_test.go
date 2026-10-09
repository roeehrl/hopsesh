package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/roeehrl/hopsesh/internal/testkit/runtimecases"
)

func TestRuntimeCommandRejectsInvalidSelectionBeforeExecution(t *testing.T) {
	for _, args := range [][]string{{"-t", "0"}, {"-only", "99999"}, {"-shard", "999/999"}, {"-timeout", "0"}, {"-source", "does-not-exist"}, {"unexpected-positional-argument"}} {
		out := filepath.Join(t.TempDir(), "must-not-exist")
		flags := append([]string{"-out", out}, args...)
		if status := runtimeMain("runtime-run", flags); status != 2 {
			t.Fatal("invalid command did not fail before execution", args, status)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Fatal("invalid selection created execution files", err)
		}
	}
}

func TestRuntimeShardVerificationRequiresExactCompleteCurrentResults(t *testing.T) {
	var reports []runtimeReport
	for shard := 1; shard <= 2; shard++ {
		rows, coverage, err := runtimecases.Select(2, 1, "", fmt.Sprintf("%d/2", shard))
		if err != nil {
			t.Fatal(err)
		}
		r := runtimeReport{Coverage: coverage, SelectionPassed: true}
		for _, row := range rows {
			r.Results = append(r.Results, runtimecases.Result{Row: row, Status: "passed"})
		}
		reports = append(reports, r)
	}
	body, err := json.Marshal(reports)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func([]runtimeReport) []runtimeReport
	}{
		{"complete", func(r []runtimeReport) []runtimeReport { return r }},
		{"missing shard", func(r []runtimeReport) []runtimeReport { return r[:1] }},
		{"duplicate shard", func(r []runtimeReport) []runtimeReport { return append(r, r[0]) }},
		{"failed row", func(r []runtimeReport) []runtimeReport { r[0].Results[0].Status = "failed"; return r }},
		{"skipped row", func(r []runtimeReport) []runtimeReport { r[0].Results[0].Status = "skipped"; return r }},
		{"substituted factors", func(r []runtimeReport) []runtimeReport { r[0].Results[0].Row.Provider = "unimplemented"; return r }},
		{"stale domain", func(r []runtimeReport) []runtimeReport { r[0].Coverage.Domains[2] = []string{"local"}; return r }},
		{"wrong strength", func(r []runtimeReport) []runtimeReport { r[0].Coverage.Strength = 3; return r }},
		{"false qualification", func(r []runtimeReport) []runtimeReport { r[0].ModelQualified = true; return r }},
		{"empty result", func(r []runtimeReport) []runtimeReport { r[0].Results = nil; return r }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var copied []runtimeReport
			if err := json.Unmarshal(body, &copied); err != nil {
				t.Fatal(err)
			}
			var paths []string
			for i, report := range tc.change(copied) {
				data, err := json.Marshal(report)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), fmt.Sprintf("report-%d.json", i))
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				paths = append(paths, path)
			}
			if err := verifyRuntimeReports(2, 1, paths); (err == nil) != (tc.name == "complete") {
				t.Fatal("incorrect combined coverage verdict", err)
			}
		})
	}
}
