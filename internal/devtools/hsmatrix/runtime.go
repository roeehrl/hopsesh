package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/roeehrl/hopsesh/internal/testkit/runtimecases"
)

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
	timeout := flags.Duration("timeout", 15*time.Minute, "bounded native test deadline")
	if err := flags.Parse(args); err != nil || len(flags.Args()) != 0 || *timeout <= 0 || *timeout > time.Hour {
		fmt.Fprintln(os.Stderr, "invalid runtime matrix arguments or timeout")
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
	passed := runErr == nil && closeErr == nil && reportErr == nil
	result := struct {
		Coverage        runtimecases.Coverage `json:"coverage"`
		SelectionPassed bool                  `json:"selectionPassed"`
		ModelQualified  bool                  `json:"modelQualified"`
		Execution       string                `json:"execution"`
		Results         []runtimecases.Result `json:"results"`
	}{coverage, passed, passed && coverage.CompleteSelection, execution, results}
	body, err = json.MarshalIndent(result, "", "  ")
	if err == nil {
		err = os.WriteFile(filepath.Join(output, "results.json"), body, 0600)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("\nModel %s: %d/%d generated rows, %d/%d interactions at t=%d; selection passed=%t; model qualified=%t.\n", coverage.Model, coverage.SelectedRows, coverage.GeneratedRows, coverage.SelectedInteractions, coverage.ValidInteractions, coverage.Strength, passed, result.ModelQualified)
	if !passed {
		fmt.Fprintf(os.Stderr, "native run=%v; log=%v; verified report=%v\n", runErr, closeErr, reportErr)
		return 1
	}
	return 0
}
