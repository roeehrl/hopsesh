// Command hsmatrix runs hopsesh's scenario matrix between this machine ("here") and
// another one reached over ssh ("there"): moves, continuing in the other agent, pushes,
// round trips, conflicts, undo, skills, in every language and repository state the model
// names. The other machine needs hopsesh, this program (as its helper) and the stand-in
// agents (internal/devtools/fakeagent) on its PATH; CI jobs set that up for each pair of
// systems. Results go to a grid in the run summary and a log per row.
//
//	hsmatrix run -hopsesh bin/hopsesh -there hsremote@127.0.0.1 -alias hsm-box -label linux→linux -out out/
//	hsmatrix rows [-all]
//	hsmatrix agent seed|find|append|head|base   (the helper; JSON in, JSON out)
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
			fmt.Fprintln(os.Stderr, "usage: hsmatrix agent seed|find|append|head|base")
			os.Exit(2)
		}
		if err := helperMain(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, "hsmatrix:", err)
			os.Exit(1)
		}
	case "rows":
		fl := flag.NewFlagSet("rows", flag.ExitOnError)
		all := fl.Bool("all", false, "every combination, not pairwise")
		seedN := fl.Int64("seed", 1, "pairwise seed")
		_ = fl.Parse(os.Args[2:])
		rows := pairwise(*seedN)
		if *all {
			rows = every()
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
	seedN := fl.Int64("seed", 1, "pairwise seed")
	only := fl.String("only", "", "comma-separated row numbers or ops to run")
	_ = fl.Parse(args)
	if *there == "" {
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
		[]byte("schema = 3\nrepos_dir = "+q(env["HSMATRIX_BASE"])+"\nlayout = \"flat\"\nupdate_check = \"off\"\n"), 0o644)

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
	if err := setup(); err != nil {
		_ = os.WriteFile(filepath.Join(outDir, "setup-FAIL.log"), []byte(r.log.b.String()+"\n"+err.Error()), 0o644)
		fmt.Fprintln(os.Stderr, "setup:", err)
		return 1
	}

	rows := pairwise(*seedN)
	if *all {
		rows = every()
	}
	if *only != "" {
		keep := map[string]bool{}
		for _, s := range strings.Split(*only, ",") {
			keep[strings.TrimSpace(s)] = true
		}
		var sel []Row
		for _, row := range rows {
			if keep[strconv.Itoa(row.N)] || keep[row.Op] {
				sel = append(sel, row)
			}
		}
		rows = sel
	}
	var results []result
	failed := 0
	for _, row := range rows {
		res := r.run(row)
		results = append(results, res)
		status := "ok  "
		if !res.OK {
			status = "FAIL"
			failed++
		}
		fmt.Printf("%s %5.1fs %s\n", status, res.Seconds, row)
		if !res.OK {
			fmt.Println("     ", strings.ReplaceAll(firstLines(res.Error, 6), "\n", "\n      "))
		}
	}
	b, _ := json.MarshalIndent(map[string]any{"label": *label, "results": results}, "", "  ")
	_ = os.WriteFile(filepath.Join(outDir, "results.json"), b, 0o644)
	summary := grid(*label, results)
	_ = os.WriteFile(filepath.Join(outDir, "summary.md"), []byte(summary), 0o644)
	if p := os.Getenv("GITHUB_STEP_SUMMARY"); p != "" {
		if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			f.WriteString(summary)
			f.Close()
		}
	}
	fmt.Printf("\n%d of %d rows passed (%s)\n", len(results)-failed, len(results), *label)
	if failed > 0 {
		return 1
	}
	return 0
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
	ok := 0
	for _, r := range results {
		if r.OK {
			ok++
		}
	}
	fmt.Fprintf(&b, "### Scenario matrix %s: %d of %d passed\n\n", label, ok, len(results))
	b.WriteString("| # | op | agents | content | repo | naming | result | s |\n|---|---|---|---|---|---|---|---|\n")
	for _, r := range results {
		mark := "✅"
		if !r.OK {
			mark = "❌ " + strings.ReplaceAll(firstLines(r.Error, 1), "|", "\\|")
		}
		fmt.Fprintf(&b, "| %d | %s | %s→%s | %s | %s | %s | %s | %.0f |\n", r.Row.N, r.Row.Op, r.Row.From, r.Row.To,
			r.Row.Content, r.Row.Repo, r.Row.Naming, mark, r.Seconds)
	}
	b.WriteString("\n")
	return b.String()
}
