// Command hsmatrix runs hopsesh's scenario matrix between this machine ("here") and
// another one reached over ssh ("there"): moves, continuing in the other agent, pushes,
// round trips, conflicts, undo, skills, in every language and repository state the model
// names. The other machine needs hopsesh, this program (as its helper) and the stand-in
// agents (internal/devtools/fakeagent) on its PATH; CI jobs set that up for each pair of
// systems. Results go to a grid in the run summary and a log per row.
//
//	hsmatrix run -hopsesh bin/hopsesh -there hsremote@127.0.0.1 -alias hsm-box -label linux→linux -out out/
//	hsmatrix rows [-all]
//	hsmatrix agent seed|find|append|remove|head|base   (the helper; JSON in, JSON out)
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: hsmatrix run|rows|agent …")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "agent":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: hsmatrix agent seed|find|append|remove|head|base")
			os.Exit(2)
		}
		if err := helperMain(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, "hsmatrix:", err)
			os.Exit(1)
		}
	case "rows":
		fl := flag.NewFlagSet("rows", flag.ExitOnError)
		all := fl.Bool("all", false, "every combination, not pairwise")
		strength := fl.Int("t", 2, "coverage: 2 = pairs, 3 = triples")
		seedN := fl.Int64("seed", 1, "pairwise seed")
		_ = fl.Parse(os.Args[2:])
		rows, _, err := selectedRows(*strength, *seedN, *all, "", "")
		if err != nil {
			fmt.Fprintln(os.Stderr, "hsmatrix:", err)
			os.Exit(2)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rows)
	case "run":
		os.Exit(runMain(os.Args[2:]))
	default:
		fmt.Fprintln(os.Stderr, "unknown command", os.Args[1])
		os.Exit(2)
	}
}

func runMain(args []string) int {
	fl := flag.NewFlagSet("run", flag.ExitOnError)
	hs := fl.String("hopsesh", "hopsesh", "the hopsesh program here")
	there := fl.String("there", "", "ssh destination of the other machine (user@address)")
	alias := fl.String("alias", "", "an ssh config alias for the same machine (naming=alias rows); default: -there")
	helper := fl.String("helper", "hsmatrix", "this program's command on the other machine")
	label := fl.String("label", "", "what the run covers, for the summary (e.g. linux→windows)")
	out := fl.String("out", "hsmatrix-out", "folder for results and logs")
	all := fl.Bool("all", false, "every combination, not pairwise")
	strength := fl.Int("t", 2, "coverage: 2 = every pair of values (pull requests), 3 = every triple (releases)")
	seedN := fl.Int64("seed", 1, "pairwise seed")
	only := fl.String("only", "", "comma-separated row numbers or ops to run")
	shard := fl.String("shard", "", "k/n: run every n-th row starting at the k-th (1-based), to split a long run across jobs")
	_ = fl.Parse(args)
	rows, coverage, err := selectedRows(*strength, *seedN, *all, *only, *shard)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hsmatrix:", err)
		return 2
	}
	// Vendor-cloud fixtures run locally; unsupported rows must not connect to
	// another machine merely to report why they could not execute.
	needThere := false
	for _, row := range rows {
		needThere = needThere || scenarioSupport(row, runtime.GOOS) == nil && !cloudOp(row.Op) && row.Op != "skill"
	}
	if *there == "" && needThere {
		fmt.Fprintln(os.Stderr, "-there is required")
		return 2
	}
	if *alias == "" {
		*alias = *there
	}
	outDir, _ := filepath.Abs(*out)
	work := filepath.Join(outDir, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if p, err := exec.LookPath(*hs); err == nil {
		*hs, _ = filepath.Abs(p)
	}
	// hopsesh here, isolated: its own settings, state, agent folders and repositories.
	env := map[string]string{
		"HOPSESH_CONFIG_DIR": filepath.Join(work, "config"), "HOPSESH_STATE_DIR": filepath.Join(work, "state"),
		"CLAUDE_CONFIG_DIR": filepath.Join(work, "claude"), "CODEX_HOME": filepath.Join(work, "codex"),
		"HSMATRIX_BASE": filepath.Join(work, "repos"), "HOPSESH_MACHINE": "here", "HOPSESH_TAILSCALE": "off",
	}
	for k, v := range env {
		os.Setenv(k, v)
	}
	for _, d := range []string{env["HOPSESH_CONFIG_DIR"], env["CLAUDE_CONFIG_DIR"], filepath.Join(env["CODEX_HOME"], "sessions"), env["HSMATRIX_BASE"]} {
		_ = os.MkdirAll(d, 0o755)
	}
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	_ = os.WriteFile(filepath.Join(env["HOPSESH_CONFIG_DIR"], "config.toml"),
		[]byte("schema = 5\nrepos_dir = "+q(env["HSMATRIX_BASE"])+"\nlayout = \"flat\"\nupdate_check = \"off\"\n"), 0o644)

	r := &runner{hopsesh: *hs, there: remoteSide{dest: *there, helper: *helper}, out: outDir, log: &rowLog{},
		hosts: map[string]string{"address": "box", "alias": "boxa"}}
	setup := func() error {
		for name, dest := range map[string]string{"box": *there, "boxa": *alias} {
			if _, err := r.hs(true, "hosts", "add", name, dest); err != nil {
				return err
			}
			if _, err := r.hs(true, "trust", name, "--yes"); err != nil {
				return err
			}
		}
		// The other machine's hopsesh accepts pushes.
		cmd := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "-o", "LogLevel=ERROR", *there, "hopsesh receive on")
		if b, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("hopsesh receive on, there: %v: %s", err, b)
		}
		return nil
	}
	if needThere {
		if err := setup(); err != nil {
			_ = os.WriteFile(filepath.Join(outDir, "setup-FAIL.log"), []byte(r.log.b.String()+"\n"+err.Error()), 0o644)
			fmt.Fprintln(os.Stderr, "setup:", err)
			return 1
		}
	}

	var results []result
	for _, row := range rows {
		res := r.run(row)
		results = append(results, res)
		fmt.Printf("%s %5.1fs %s\n", res.Status, res.Seconds, row)
		if !res.OK {
			fmt.Println("     ", strings.ReplaceAll(firstLines(res.Error+res.Reason, 6), "\n", "\n      "))
		}
	}
	passed, failed, unsupported := resultCounts(results)
	b, _ := json.MarshalIndent(map[string]any{"label": *label, "coverage": coverage, "qualificationComplete": coverage.Complete && failed == 0 && unsupported == 0, "passed": passed, "failed": failed, "unsupported": unsupported, "results": results}, "", "  ")
	_ = os.WriteFile(filepath.Join(outDir, "results.json"), b, 0o644)
	summary := fmt.Sprintf("Model: %s; t=%d; seed=%d; selected %d/%d generated rows, %d/%d valid interactions. Complete selection: %t.\n\n", coverage.Model, coverage.Strength, coverage.Seed, coverage.SelectedRows, coverage.GeneratedRows, coverage.SelectedInteractions, coverage.ValidInteractions, coverage.Complete) + grid(*label, results)
	_ = os.WriteFile(filepath.Join(outDir, "summary.md"), []byte(summary), 0o644)
	if p := os.Getenv("GITHUB_STEP_SUMMARY"); p != "" {
		if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			_, _ = f.WriteString(summary)
			_ = f.Close()
		}
	}
	fmt.Printf("\n%d passed, %d failed, %d unsupported of %d selected rows (%s)\n", passed, failed, unsupported, len(results), *label)
	if failed > 0 {
		return 1
	}
	if passed == 0 {
		return 2
	}
	return 0
}

func resultCounts(results []result) (passed, failed, unsupported int) {
	for _, r := range results {
		switch r.Status {
		case "passed":
			passed++
		case "unsupported":
			unsupported++
		default:
			failed++
		}
	}
	return
}

func firstLines(s string, n int) string {
	l := strings.SplitN(s, "\n", n+1)
	if len(l) > n {
		l = append(l[:n], "…")
	}
	return strings.Join(l, "\n")
}

// grid is the run summary: one line per row.
func grid(label string, results []result) string {
	var b strings.Builder
	passed, failed, unsupported := resultCounts(results)
	fmt.Fprintf(&b, "### Scenario matrix %s: %d passed, %d failed, %d unsupported of %d selected rows\n\n", label, passed, failed, unsupported, len(results))
	b.WriteString("| # | op | agents | content | repo | naming | location | result | s |\n|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range results {
		mark := "✅"
		if r.Status == "unsupported" {
			mark = "Unsupported: " + strings.ReplaceAll(firstLines(r.Reason, 1), "|", "\\|")
		} else if !r.OK {
			mark = "❌ " + strings.ReplaceAll(firstLines(r.Error, 1), "|", "\\|")
		}
		fmt.Fprintf(&b, "| %d | %s | %s→%s | %s | %s | %s | %s | %s | %.0f |\n", r.Row.N, r.Row.Op, r.Row.From, r.Row.To,
			r.Row.Content, r.Row.Repo, r.Row.Naming, r.Row.Location, mark, r.Seconds)
	}
	b.WriteString("\n")
	return b.String()
}
