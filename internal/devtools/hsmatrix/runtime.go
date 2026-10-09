package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/testkit/runtimecases"
)

type runtimeReport struct {
	Source          runtimeSource         `json:"source"`
	SourceUnchanged bool                  `json:"sourceUnchanged"`
	Coverage        runtimecases.Coverage `json:"coverage"`
	SelectionPassed bool                  `json:"selectionPassed"`
	ModelQualified  bool                  `json:"modelQualified"`
	Execution       string                `json:"execution"`
	Results         []runtimecases.Result `json:"results"`
}

// Dirty runs remain useful development evidence, but cannot qualify a release.
// Both the runner and aggregator bind evidence to their actual Git checkout.
type runtimeSource struct {
	Revision string `json:"revision"`
	Dirty    bool   `json:"dirty"`
}

func readRuntimeSource(root string) (runtimeSource, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	git := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = root
		return cmd.Output()
	}
	head, err := git("rev-parse", "--verify", "HEAD")
	if err != nil {
		return runtimeSource{}, fmt.Errorf("read runtime source revision: %w", err)
	}
	status, err := git("status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return runtimeSource{}, fmt.Errorf("read runtime source changes: %w", err)
	}
	return runtimeSource{Revision: strings.TrimSpace(string(head)), Dirty: len(status) != 0}, nil
}

// runtime-run executes selected factor rows in the native integration harness.
// It requires a source checkout with the relay's npm dependencies installed.
// Tests are run once; no retry can hide a failing matrix row.
func runtimeMain(command string, args []string) int {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	strength := flags.Int("t", 2, "2 = pairs; 3 = triples")
	seed := flags.Int64("seed", 1, "deterministic covering-array seed")
	only := flags.String("only", "", "comma-separated generated row IDs")
	shard := flags.String("shard", "", "k/n: split the generated rows across jobs")
	source := flags.String("source", ".", "Hopsesh source checkout with installed relay fixture dependencies")
	out := flags.String("out", "hsmatrix-runtime-out", "result and execution-log directory")
	reports := flags.String("reports", "", "runtime-verify: comma-separated report files from all shards")
	timeout := flags.Duration("timeout", 15*time.Minute, "bounded native test deadline")
	if err := flags.Parse(args); err != nil || len(flags.Args()) != 0 || *timeout <= 0 || *timeout > time.Hour {
		fmt.Fprintln(os.Stderr, "invalid runtime matrix arguments or timeout")
		return 2
	}
	if command == "runtime-verify" {
		if *reports == "" || *only != "" || *shard != "" {
			fmt.Fprintln(os.Stderr, "runtime-verify needs report files and the complete model selection")
			return 2
		}
		expected, err := readRuntimeSource(*source)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := verifyRuntimeReports(*strength, *seed, strings.Split(*reports, ","), expected); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("All runtime covering rows executed exactly once and passed at clean source revision %s.\n", expected.Revision)
		return 0
	}
	if *reports != "" {
		fmt.Fprintln(os.Stderr, "-reports is only valid with runtime-verify")
		return 2
	}
	rows, coverage, err := runtimecases.Select(*strength, *seed, *only, *shard)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if command == "runtime-rows" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(struct {
			Coverage runtimecases.Coverage `json:"coverage"`
			Rows     []runtimecases.Row    `json:"rows"`
		}{coverage, rows}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	root, err := filepath.Abs(*source)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if _, err := os.Stat(filepath.Join(root, "internal", "e2e", "runtime_matrix_test.go")); err != nil {
		fmt.Fprintln(os.Stderr, "-source must contain the native runtime matrix harness:", err)
		return 2
	}
	sourceBefore, err := readRuntimeSource(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	output, err := filepath.Abs(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// A fresh directory prevents an old successful report from qualifying a
	// later process that fails or exits without executing its requested rows.
	execution, err := os.MkdirTemp(output, "execution-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	input, report := filepath.Join(execution, "rows.json"), filepath.Join(execution, "results.json")
	body, err := json.Marshal(rows)
	if err == nil {
		err = os.WriteFile(input, body, 0600)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	log, err := os.OpenFile(filepath.Join(execution, "native.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	cmd := exec.Command("go", "test", "-race", "-count=1", "-v", "-run", "^TestRuntimeNativeMatrix$", "-timeout", timeout.String(), "./internal/e2e")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "HOPSESH_RUNTIME_MATRIX=1", "HOPSESH_RELAY_REQUIRE_NODE=1", "HOPSESH_RELAY_PLATFORM=1", "HOPSESH_RUNTIME_MATRIX_INPUT="+input, "HOPSESH_RUNTIME_MATRIX_REPORT="+report)
	cmd.Stdout, cmd.Stderr = io.MultiWriter(os.Stdout, log), io.MultiWriter(os.Stderr, log)
	runErr := cmd.Run()
	closeErr := log.Close()
	var results []runtimecases.Result
	body, reportErr := os.ReadFile(report)
	if reportErr == nil {
		reportErr = json.Unmarshal(body, &results)
	}
	if reportErr == nil {
		reportErr = runtimecases.VerifyResults(rows, results)
	}
	sourceAfter, sourceErr := readRuntimeSource(root)
	unchanged := sourceErr == nil && sourceBefore == sourceAfter
	passed := runErr == nil && closeErr == nil && reportErr == nil && unchanged
	result := runtimeReport{
		Source: sourceBefore, SourceUnchanged: unchanged, Coverage: coverage,
		SelectionPassed: passed, ModelQualified: passed && coverage.CompleteSelection && !sourceBefore.Dirty,
		Execution: execution, Results: results,
	}
	body, err = json.MarshalIndent(result, "", "  ")
	if err == nil {
		err = os.WriteFile(filepath.Join(output, "results.json"), body, 0600)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("\nModel %s: %d/%d generated rows, %d/%d interactions at t=%d; selection passed=%t; model qualified=%t.\n", coverage.Model, coverage.SelectedRows, coverage.GeneratedRows, coverage.SelectedInteractions, coverage.ValidInteractions, coverage.Strength, passed, result.ModelQualified)
	fmt.Printf("Source revision %s; dirty=%t; unchanged=%t.\n", sourceBefore.Revision, sourceBefore.Dirty, unchanged)
	if !passed {
		fmt.Fprintf(os.Stderr, "native run=%v; log=%v; verified report=%v; source unchanged=%t; source error=%v\n", runErr, closeErr, reportErr, unchanged, sourceErr)
		return 1
	}
	return 0
}

func verifyRuntimeReports(strength int, seed int64, paths []string, expected runtimeSource) error {
	if expected.Revision == "" || expected.Dirty {
		return fmt.Errorf("runtime qualification requires a clean source checkout with a known revision")
	}
	all, _, err := runtimecases.Select(strength, seed, "", "")
	if err != nil {
		return err
	}
	seen := map[int]bool{}
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var report runtimeReport
		if err := json.Unmarshal(body, &report); err != nil {
			return err
		}
		if !report.SourceUnchanged || report.Source != expected {
			return fmt.Errorf("report %s is not bound to unchanged clean source revision %s", path, expected.Revision)
		}
		if !report.SelectionPassed || len(report.Results) == 0 {
			return fmt.Errorf("report %s has no passing executed selection", path)
		}
		var ids []string
		for _, result := range report.Results {
			if seen[result.Row.N] {
				return fmt.Errorf("duplicate runtime row %d across reports", result.Row.N)
			}
			seen[result.Row.N] = true
			ids = append(ids, fmt.Sprint(result.Row.N))
		}
		rows, coverage, err := runtimecases.Select(strength, seed, strings.Join(ids, ","), "")
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(report.Coverage, coverage) || report.ModelQualified != coverage.CompleteSelection {
			return fmt.Errorf("report %s does not describe the current requested model and selection", path)
		}
		if err := runtimecases.VerifyResults(rows, report.Results); err != nil {
			return fmt.Errorf("report %s: %w", path, err)
		}
	}
	if len(seen) != len(all) {
		return fmt.Errorf("incomplete runtime coverage: %d/%d generated rows", len(seen), len(all))
	}
	return nil
}
