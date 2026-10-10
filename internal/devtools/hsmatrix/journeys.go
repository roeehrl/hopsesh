package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
)

type journeyCase struct {
	Name   string   `json:"name"`
	Route  string   `json:"route"`
	Agents []string `json:"agents"`
	Fork   bool     `json:"fork"`
}

type journeyOutcome struct {
	Case    journeyCase `json:"case"`
	Error   string      `json:"error,omitempty"`
	Seconds float64     `json:"seconds"`
}

// An interrupted workflow retains completed cases without representing them as
// a complete qualification or claiming post-execution source verification.
func saveJourneyProgress(directory, revision string, results []journeyOutcome) error {
	failed := 0
	for _, result := range results {
		if result.Error != "" {
			failed++
		}
	}
	body, err := json.MarshalIndent(map[string]any{"model": "three-native-machines/v1", "sourceRevision": revision, "qualificationComplete": false, "expectedCases": len(journeyCases()), "completedCases": len(results), "passed": len(results) - failed, "failed": failed, "cases": results}, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(directory, ".journey-progress-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(body); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), filepath.Join(directory, "progress.json"))
}

// Route order is part of the model: pairwise combinations cannot substitute for
// visiting C, revisiting B and finally returning to the branch's origin.
func journeyCases() []journeyCase {
	var cases []journeyCase
	for _, route := range []string{"ABCA", "ABCBCAB", "ABCACBA"} {
		for _, agents := range [][]string{{"claude", "claude", "claude"}, {"codex", "codex", "codex"}, {"claude", "codex", "claude"}, {"codex", "claude", "codex"}} {
			for _, fork := range []bool{false, true} {
				cases = append(cases, journeyCase{Name: fmt.Sprintf("%s-%s-fork=%t", route, strings.Join(agents, "-"), fork), Route: route, Agents: agents, Fork: fork})
			}
		}
	}
	return cases
}

func journeysMain(args []string) int {
	fl := flag.NewFlagSet("journeys", flag.ContinueOnError)
	nodes := fl.String("nodes", "hs-linux,hs-macos,hs-windows", "three mutually reachable SSH aliases, in A/B/C order")
	out := fl.String("out", "hsmatrix-journeys-out", "evidence directory")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	aliases := strings.Split(*nodes, ",")
	if len(aliases) != 3 || aliases[0] == aliases[1] || aliases[0] == aliases[2] || aliases[1] == aliases[2] {
		fmt.Fprintln(os.Stderr, "three distinct SSH aliases are required")
		return 2
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var endpoints [3]side
	var platforms []string
	build, ok := debug.ReadBuildInfo()
	if !ok {
		fmt.Fprintln(os.Stderr, "missing controller build identity")
		return 2
	}
	revision, modified := matrixBuildSource(build)
	if revision == "" || modified != "false" {
		fmt.Fprintln(os.Stderr, "mixed-OS qualification requires binaries built from a clean source revision")
		return 2
	}
	log := &rowLog{}
	// Setup failures happen before per-journey reports exist. Persist the exact
	// endpoint/command and its diagnostics even when preflight exits early.
	setupComplete := false
	defer func() {
		if !setupComplete {
			if err := os.WriteFile(filepath.Join(*out, "setup.log"), []byte(log.b.String()), 0o600); err != nil {
				fmt.Fprintln(os.Stderr, "save journey setup diagnostics:", err)
			}
		}
	}()
	for i, alias := range aliases {
		endpoints[i] = remoteSide{dest: alias, helper: "hsmatrix", log: log}
		var info map[string]string
		if err := endpoints[i].do("base", nil, &info); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		platforms = append(platforms, info["os"])
		if err := verifyJourneySource(info, revision); err != nil {
			fmt.Fprintln(os.Stderr, alias, err)
			return 2
		}
	}
	sorted := slices.Clone(platforms)
	slices.Sort(sorted)
	if !slices.Equal(sorted, []string{"darwin", "linux", "windows"}) {
		fmt.Fprintf(os.Stderr, "real mixed-OS qualification requires darwin/linux/windows, got %v\n", platforms)
		return 2
	}
	// Set up every directed edge using each endpoint's own native CLI process.
	for i, endpoint := range endpoints {
		for j, alias := range aliases {
			if i == j {
				continue
			}
			for _, argv := range [][]string{{"hosts", "add", alias, alias}, {"trust", alias, "--yes"}} {
				log.printf("setup %s: hopsesh %q\n", aliases[i], argv)
				if err := endpoint.do("command", commandReq{Args: argv}, &struct{}{}); err != nil {
					log.printf("setup failed: %v\n", err)
					fmt.Fprintln(os.Stderr, err)
					return 1
				}
			}
			var probe matrixProbeResult
			if err := endpoint.do("probe", alias, &probe); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if probe.Error != "" || probe.Facts.OS != platforms[j] {
				fmt.Fprintf(os.Stderr, "native route %s -> %s: expected OS %s; probe=%+v\n", aliases[i], alias, platforms[j], probe)
				return 1
			}
		}
	}
	if err := os.WriteFile(filepath.Join(*out, "setup.log"), []byte(log.b.String()), 0600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	setupComplete = true
	var results []journeyOutcome
	failed := 0
	for i, c := range journeyCases() {
		log.b.Reset()
		start := time.Now()
		err := runJourney(c, endpoints, aliases)
		res := journeyOutcome{Case: c, Seconds: time.Since(start).Seconds()}
		if err != nil {
			failed++
			res.Error = err.Error()
		}
		results = append(results, res)
		fmt.Printf("%s %.1fs %s\n", c.Name, res.Seconds, res.Error)
		if err := os.WriteFile(filepath.Join(*out, fmt.Sprintf("journey-%02d.log", i)), []byte(log.b.String()+"\n"+res.Error), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := saveJourneyProgress(*out, revision, results); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	sourceError := ""
	for i, endpoint := range endpoints {
		var info map[string]string
		err := endpoint.do("base", nil, &info)
		if err == nil {
			err = verifyJourneySource(info, revision)
		}
		if err != nil || info["os"] != platforms[i] {
			sourceError = fmt.Sprintf("endpoint %s changed or cannot be verified after execution: %v", aliases[i], err)
			break
		}
	}
	b, err := json.MarshalIndent(map[string]any{"model": "three-native-machines/v1", "sourceRevision": revision, "sourceError": sourceError, "qualificationComplete": failed == 0 && sourceError == "", "platforms": platforms, "cases": results, "passed": len(results) - failed, "failed": failed}, "", "  ")
	if err == nil {
		err = os.WriteFile(filepath.Join(*out, "results.json"), b, 0o600)
	}
	if err != nil || failed != 0 || sourceError != "" {
		fmt.Fprintf(os.Stderr, "journeys: %d failed; source: %s; report error: %v\n", failed, sourceError, err)
		return 1
	}
	return 0
}

func verifyJourneySource(info map[string]string, revision string) error {
	if revision == "" || info["helperRevision"] != revision || info["appRevision"] != revision || info["helperModified"] != "false" || info["appModified"] != "false" {
		return fmt.Errorf("endpoint helper and app must match the clean controller source revision")
	}
	return nil
}

func journeyFind(on side, marker, agent, id string, needles []string) (Found, error) {
	var found []Found
	if err := on.do("find", FindReq{Marker: marker, Needles: needles}, &found); err != nil {
		return Found{}, err
	}
	var matches []Found
	for _, f := range found {
		if f.Agent == agent && f.ID == id {
			matches = append(matches, f)
		}
	}
	if len(matches) != 1 {
		return Found{}, fmt.Errorf("want exactly one %s/%s arrival, got %d", agent, id, len(matches))
	}
	return matches[0], nil
}

// Expected counts derive only from the requested route, never from the graph
// under test. Fork creation starts a new journey at the first destination.
func expectedJourney(route string, fork bool) (transfers, returns, rounds int) {
	if fork {
		route = route[1:]
	}
	seen := map[byte]bool{route[0]: true}
	for i := 1; i < len(route); i++ {
		transfers++
		if seen[route[i]] {
			returns++
			if route[i] == route[0] {
				rounds++
			}
		}
		seen[route[i]] = true
	}
	return
}

func checkJourney(g *lineage.Manifest, route string, fork bool) error {
	if g == nil {
		return fmt.Errorf("missing lineage")
	}
	if err := g.Validate(); err != nil {
		return err
	}
	t, r, rt := expectedJourney(route, fork)
	j := g.Journey()
	if j.Transfers != t || j.Returns != r || j.RoundTrips != rt || j.MachineTransfers != t || j.MachineReturns != r || j.MachineRoundTrips != rt || j.Fork != fork {
		return fmt.Errorf("route %s fork=%t: want transfers/returns/roundTrips=%d/%d/%d, got %+v; replicas=%+v", route, fork, t, r, rt, j, g.Replicas)
	}
	return nil
}

func runJourney(c journeyCase, endpoints [3]side, aliases []string) error {
	id := newID()
	marker := "three-hosts-" + id
	var cwd [3]string
	var current Found
	for i, endpoint := range endpoints {
		var seeded SeedRes
		if err := endpoint.do("seed", SeedReq{Agent: c.Agents[i], ID: id, Title: c.Name, Text: marker, Repo: "journey-" + id, State: "clean", Session: i == 0}, &seeded); err != nil {
			return err
		}
		cwd[i] = seeded.Cwd
		if i == 0 {
			current = Found{ID: id, Agent: c.Agents[i], Path: seeded.File}
		}
	}
	// Establish owner-observed account bindings before departure. A previously
	// unknown binding becoming known during the route is a different scenario;
	// it must not be mistaken for a return to the original account binding.
	for _, endpoint := range endpoints {
		if err := endpoint.do("command", commandReq{Args: []string{"accounts", "scan"}}, &struct{}{}); err != nil {
			return err
		}
	}
	work := []string{marker}
	currentRef := current.Agent + "/" + current.ID
	originalRef := currentRef
	var original Found
	var family, branch string
	for hop := 1; hop < len(c.Route); hop++ {
		from, to := int(c.Route[hop-1]-'A'), int(c.Route[hop]-'A')
		text := fmt.Sprintf("work-%d-%s", hop, marker)
		work = append(work, text)
		if err := endpoints[from].do("append", AppendReq{Agent: current.Agent, Path: current.Path, ID: current.ID, Text: text}, &struct{}{}); err != nil {
			return err
		}
		if hop == 1 && c.Fork {
			var err error
			original, err = journeyFind(endpoints[0], marker, current.Agent, current.ID, nil)
			if err != nil {
				return err
			}
		}
		argv := []string{"pull", aliases[from] + ":" + currentRef, "--in", c.Agents[to], "--to", cwd[to], "--new-session", "--yes", "--json", "--operation-id", fmt.Sprintf("journey-%s-%d", id, hop)}
		if hop == 1 && c.Fork {
			argv = append(argv, "--fork")
		}
		var reply struct{ Plan move.Plan }
		if err := endpoints[to].do("command", commandReq{Args: argv}, &reply); err != nil {
			return err
		}
		if hop == 1 {
			originalRef = reply.Plan.Key.String()
		}
		arrivalID := string(reply.Plan.Placement.Key.Session)
		if arrivalID == "" || arrivalID == current.ID || !reply.Plan.Options.NewReplica {
			return fmt.Errorf("hop %d lost explicit new replica intent", hop)
		}
		needles := append(slices.Clone(work), cwd[to])
		arrival, err := journeyFind(endpoints[to], marker, c.Agents[to], arrivalID, append(slices.Clone(needles), cwd[from]))
		if err != nil {
			return err
		}
		for _, needle := range needles {
			if !arrival.Has[needle] {
				return fmt.Errorf("hop %d lost %q", hop, needle)
			}
		}
		if err := checkMovement(arrival, hop, true, hop == 1 && c.Fork); err != nil {
			return err
		}
		if cwd[from] != cwd[to] && arrival.Has[cwd[from]] {
			return fmt.Errorf("hop %d retained the previous machine's working directory", hop)
		}
		if err := checkJourney(arrival.Graph, c.Route[:hop+1], c.Fork); err != nil {
			return err
		}
		if hop == 1 {
			family, branch = arrival.Graph.Family, arrival.Graph.Branch
		}
		if arrival.Graph.Family != family || arrival.Graph.Branch != branch {
			return fmt.Errorf("hop %d changed family/branch", hop)
		}
		// Re-execute in a new CLI process with the exact operation, after commit.
		var repeated struct{ Plan move.Plan }
		if err := endpoints[to].do("command", commandReq{Args: argv}, &repeated); err != nil {
			return err
		}
		if repeated.Plan.Placement.Key != reply.Plan.Placement.Key {
			return fmt.Errorf("hop %d retry created a different native arrival", hop)
		}
		retry, err := journeyFind(endpoints[to], marker, c.Agents[to], arrivalID, nil)
		if err != nil {
			return err
		}
		if retry.SHA256 != arrival.SHA256 || retry.Graph == nil || !slices.Equal(retry.Graph.Encode(), arrival.Graph.Encode()) {
			return fmt.Errorf("hop %d retry altered native content or lineage", hop)
		}
		current = arrival
		currentRef = reply.Plan.Placement.Key.String()
	}
	if !c.Fork {
		return nil
	}
	preserved, err := journeyFind(endpoints[0], marker, original.Agent, original.ID, nil)
	if err != nil {
		return err
	}
	if preserved.SHA256 != original.SHA256 || preserved.Mark != original.Mark || preserved.Graph == nil || preserved.Graph.Branch == branch {
		return fmt.Errorf("travelling fork changed its original or merged branches")
	}
	own := "independent-original-" + marker
	if err := endpoints[0].do("append", AppendReq{Agent: original.Agent, Path: original.Path, ID: original.ID, Text: own}, &struct{}{}); err != nil {
		return err
	}
	var reply struct{ Plan move.Plan }
	argv := []string{"pull", aliases[0] + ":" + originalRef, "--in", c.Agents[2], "--to", cwd[2], "--new-session", "--yes", "--json", "--operation-id", "parent-" + id}
	if err := endpoints[2].do("command", commandReq{Args: argv}, &reply); err != nil {
		return err
	}
	parent, err := journeyFind(endpoints[2], marker, c.Agents[2], string(reply.Plan.Placement.Key.Session), append(slices.Clone(work[2:]), own))
	if err != nil {
		return err
	}
	if parent.Graph == nil || parent.Graph.Family != family || parent.Graph.Branch == branch || !parent.Has[own] {
		return fmt.Errorf("parent lost independent family/branch/work")
	}
	for _, childOnly := range work[2:] {
		if parent.Has[childOnly] {
			return fmt.Errorf("child work leaked into original")
		}
	}
	return checkJourney(parent.Graph, "AC", false)
}
