package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
	source := runtimeSource{Revision: "0123456789012345678901234567890123456789"}
	var reports []runtimeReport
	for shard := 1; shard <= 2; shard++ {
		rows, coverage, err := runtimecases.Select(2, 1, "", fmt.Sprintf("%d/2", shard))
		if err != nil {
			t.Fatal(err)
		}
		r := runtimeReport{Source: source, SourceUnchanged: true, Coverage: coverage, SelectionPassed: true}
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
		{"missing revision", func(r []runtimeReport) []runtimeReport { r[0].Source.Revision = ""; return r }},
		{"old revision", func(r []runtimeReport) []runtimeReport {
			r[0].Source.Revision = "1123456789012345678901234567890123456789"
			return r
		}},
		{"dirty source", func(r []runtimeReport) []runtimeReport { r[0].Source.Dirty = true; return r }},
		{"source changed during execution", func(r []runtimeReport) []runtimeReport { r[0].SourceUnchanged = false; return r }},
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
			if err := verifyRuntimeReports(2, 1, paths, source); (err == nil) != (tc.name == "complete") {
				t.Fatal("incorrect combined coverage verdict", err)
			}
			if tc.name == "complete" {
				for _, rejected := range []runtimeSource{{}, {Revision: source.Revision, Dirty: true}, {Revision: "1123456789012345678901234567890123456789"}} {
					if err := verifyRuntimeReports(2, 1, paths, rejected); err == nil {
						t.Fatal("reports qualified a missing, modified or different verifier checkout", rejected)
					}
				}
			}
		})
	}
}

func TestRuntimeShardVerificationRejectsUnboundSource(t *testing.T) {
	rows, coverage, err := runtimecases.Select(2, 1, "", "")
	if err != nil {
		t.Fatal(err)
	}
	report := runtimeReport{Coverage: coverage, SelectionPassed: true, ModelQualified: true}
	for _, row := range rows {
		report.Results = append(report.Results, runtimecases.Result{Row: row, Status: "passed"})
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "old-results.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyRuntimeReports(2, 1, []string{path}, runtimeSource{Revision: "0123456789012345678901234567890123456789"}); err == nil {
		t.Fatal("a report without source revision evidence qualified the current source")
	}
}

func TestRuntimeSourceTracksActualCheckout(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=" + filepath.Join(dir, "empty-hooks")}, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Runtime fixture", "GIT_AUTHOR_EMAIL=runtime@example.invalid", "GIT_COMMITTER_NAME=Runtime fixture", "GIT_COMMITTER_EMAIL=runtime@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %v %s", err, out)
		}
	}
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	read := func() runtimeSource {
		t.Helper()
		s, err := readRuntimeSource(dir)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if _, err := readRuntimeSource(dir); err == nil {
		t.Fatal("non-Git source was accepted")
	}
	git("init", "-q")
	write("source.go", "initial")
	write(".gitignore", "generated/\n")
	git("add", ".")
	git("-c", "commit.gpgsign=false", "commit", "-qm", "initial")
	initial := read()
	if initial.Revision == "" || initial.Dirty {
		t.Fatal("clean revision not recorded", initial)
	}
	write("source.go", "modified")
	if s := read(); s.Revision != initial.Revision || !s.Dirty {
		t.Fatal("uncommitted edit not detected", s)
	}
	git("add", "source.go")
	if !read().Dirty {
		t.Fatal("staged edit not detected")
	}
	git("-c", "commit.gpgsign=false", "commit", "-qm", "change")
	if s := read(); s.Dirty || s.Revision == initial.Revision {
		t.Fatal("new revision not detected", s)
	}
	write("extra.go", "untracked")
	if !read().Dirty {
		t.Fatal("untracked source not detected")
	}
	if err := os.Remove(filepath.Join(dir, "extra.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "generated"), 0700); err != nil {
		t.Fatal(err)
	}
	write("generated/output", "artifact")
	if read().Dirty {
		t.Fatal("ignored build output dirtied source")
	}
}
