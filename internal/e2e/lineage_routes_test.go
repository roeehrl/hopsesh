package e2e

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// These are mandatory stateful scenarios, not covering-array approximations. Every
// native record is read and written by the actual agent modules at every stop.
func TestLineageRoutes(t *testing.T) {
	for _, route := range []string{"ABABA", "ABCA", "ABCBCAB"} {
		for mask := 0; mask < 8; mask++ {
			for _, start := range []string{"claude", "codex"} {
				t.Run(fmt.Sprintf("%s/%s/%03b", route, start, mask), func(t *testing.T) { runLineageRoute(t, route, start, mask) })
			}
		}
	}
}
func runLineageRoute(t *testing.T, route, start string, mask int, patterns ...string) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	places := map[byte]location{}
	mods := map[string]agent.Module{"claude": claude.New(), "codex": codex.New()}
	installs := map[byte]map[string]agent.Install{}
	for _, c := range []byte{'A', 'B', 'C'} {
		l := newLocation(t, string(c), root)
		if len(patterns) > 0 && patterns[0] == "profiles" {
			l.m.Facts.Home = root
			l.m.Facts.Env["HOPSESH_CONFIG_DIR"] = filepath.Join(root, string(c), "config")
		}
		places[c] = l
		ci := codexInstall(l)
		if len(patterns) > 0 && patterns[0] == "profiles" {
			ci.Roots["home"] = filepath.Join(root, string(c), ".codex")
		}
		installs[c] = map[string]agent.Install{"claude": l.in, "codex": ci}
		if len(patterns) > 0 && patterns[0] == "accounts" {
			places[c].m.Facts.Endpoint = strings.Repeat("e", 64)
			for id, in := range installs[c] {
				in.Profile = &agent.RuntimeProfile{ID: string(c) + "-" + id, Endpoint: places[c].m.Facts.Endpoint, Agent: agent.ID(id), Name: "Personal", Tags: []string{"Personal"}, Root: in.Root("home"), Binding: string(c) + "-binding", Generation: 1}
				installs[c][id] = in
			}
		}
		for _, in := range installs[c] {
			if err := os.MkdirAll(in.Root("home"), 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(patterns) > 0 && patterns[0] == "accounts" {
		for c, group := range installs {
			for id, in := range group {
				root, err := filepath.EvalSymlinks(in.Root("home"))
				if err != nil {
					t.Fatal(err)
				}
				in.Roots["home"] = root
				in.Profile.Root = root
				installs[c][id] = in
			}
		}
	}
	seed(t, places['A'])
	currentAgent := start
	var current agent.Summary
	if start == "claude" {
		current = list(t, places['A'])[sid]
	} else {
		cl, cx := mods["claude"], mods["codex"]
		seg := readAll(t, places['A'], cl, installs['A']["claude"], list(t, places['A'])[sid])
		j, _ := journal.New(t.TempDir(), journal.KindContinue, "seed")
		h, _ := places['A'].m.For(ctx, cx.Spec(), installs['A']["codex"], j)
		_, err := cx.(agent.Writer).Write(ctx, h, installs['A']["codex"], ir.WriteRequest{SessionID: sid, Header: seg.Header, Mode: ir.WriteNew, Items: []ir.Item{{Node: "seed", Role: ir.RoleUser, Text: "ROUTE-SEED"}}})
		if err != nil {
			t.Fatal(err)
		}
		current = listAgent(t, places['A'], cx, installs['A']["codex"])[0]
	}
	current.Key.Profile = installs['A'][currentAgent].ProfileID()
	if len(patterns) > 0 && patterns[0] == "paginated" {
		promoteCodexPaginated(t, current.Path)
	}
	env := move.Env{StateDir: t.TempDir()}
	var sentinels []string
	transfers, returns := 0, 0
	pattern := "all"
	if len(patterns) > 0 {
		pattern = patterns[0]
	}
	for i := 0; i < len(route)-1; i++ {
		from, to := route[i], route[i+1]
		src, dst := places[from], places[to]
		if pattern == "pressure" || pattern == "all" || pattern == "accounts" || pattern == "profiles" || pattern == "paginated" || pattern == "alternating" && i%2 == 0 {
			sentinel := fmt.Sprintf("ROUTE-WORK-%d-UNIQUE", i)
			sentinels = append(sentinels, sentinel)
			if pattern == "pressure" {
				sentinel += "\n" + strings.Repeat("capacity-pressure ", 4000)
			}
			if currentAgent == "claude" {
				appendTurn(t, current.Path, sentinel)
			} else {
				appendCodexTurn(t, current.Path, sentinel, "reply to "+fmt.Sprint(i))
			}
		}
		current = findRouteSession(t, src, mods[currentAgent], installs[from][currentAgent], current.Key)
		lin, err := lineage.Read(host.LocalFS(), current.Path)
		if err != nil {
			t.Fatal(err)
		}
		nextAgent := "claude"
		if mask&(1<<uint(to-'A')) != 0 {
			nextAgent = "codex"
		}
		if to == 'A' {
			nextAgent = start
		}
		input := move.Input{Source: move.Side{Machine: src.m, Module: mods[currentAgent], Install: installs[from][currentAgent]}, Session: current, Lineage: lin, Target: move.Side{Machine: dst.m, Module: mods[nextAgent], Install: installs[to][nextAgent]}}
		for _, copy := range listAgent(t, dst, mods[nextAgent], installs[to][nextAgent]) {
			cm, e := lineage.Read(host.LocalFS(), copy.Path)
			if e != nil {
				t.Fatal(e)
			}
			if cm != nil && lin != nil && cm.Family == lin.Family && cm.Branch == lin.Branch {
				input.Copies = append(input.Copies, move.Copy{Summary: copy, Lineage: cm})
			}
		}
		p, err := move.Build(ctx, input, move.Options{TargetDir: dst.repo, Mark: true, Notify: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Blockers) > 0 {
			noop := len(input.Copies) == 1 && (p.Continue != nil && p.Continue.Relation == move.RelationSame || strings.Contains(strings.Join(p.Blockers, " "), "destination already has everything"))
			if !noop {
				t.Fatalf("hop %d %c/%s→%c/%s: %v", i, from, currentAgent, to, nextAgent, p.Blockers)
			}
			p.Placement.Key = input.Copies[0].Summary.Key
		} else {
			if _, err = move.Apply(ctx, p, input, env); err != nil {
				t.Fatalf("hop %d: %v", i, err)
			}
			if !p.NoWork {
				transfers++
			}
			if to == 'A' && !p.NoWork {
				returns++
			}
		}
		currentAgent = nextAgent
		current = findRouteSession(t, dst, mods[nextAgent], installs[to][nextAgent], p.Placement.Key)
		seg := readAll(t, dst, mods[nextAgent], installs[to][nextAgent], current)
		if pattern == "pressure" && p.Continue != nil && p.Continue.Report.Summarised > 0 {
			request := fmt.Sprintf("ROUTE-WORK-%d-UNIQUE", i)
			found := false
			for _, n := range seg.Nodes {
				if n.Actor == ir.User && strings.Contains(n.Text, request) {
					if strings.Contains(n.Text, "Transfer context") {
						t.Fatalf("hop %d: transfer context merged into actual user request", i)
					}
					found = true
				}
			}
			if !found {
				t.Fatalf("hop %d: latest user request lost under context pressure", i)
			}
		}
		var text strings.Builder
		for _, n := range seg.Nodes {
			text.WriteString(n.Text)
			text.WriteByte('\n')
		}
		if pattern == "pressure" {
			path := filepath.Join(installs[to][nextAgent].Root("home"), "hopsesh", "archives", string(current.Key.Session)+".jsonl")
			archived, e := os.ReadFile(path)
			if e != nil && !os.IsNotExist(e) {
				t.Fatal(e)
			}
			if e == nil {
				text.Reset()
				text.Write(archived)
			}
		}
		for _, s := range sentinels {
			if n := strings.Count(text.String(), s); n != 1 && pattern != "pressure" || n < 1 {
				t.Fatalf("hop %d: %s appears %d times", i, s, n)
			}
		}
		graph, e := lineage.Read(host.LocalFS(), current.Path)
		if e != nil {
			t.Fatal(e)
		}
		// Query the receipt reloaded from disk at every stop, including workless
		// stops and profile routes. A no-work stop records no new arrival.
		// A committed arrival must not look departed; return candidates are unique, same-branch visited replicas.
		_, replica, ok := graph.FindOnBranch(current.Key, dst.m.Name, graph.Branch)
		if !ok {
			t.Fatalf("hop %d: current native replica missing", i)
		}
		if h, departed := graph.Departure(replica); departed && !p.NoWork && len(p.Blockers) == 0 {
			t.Fatalf("hop %d: arrival has a departure notice: %+v", i, h)
		}
		seenReturns := map[lineage.ReplicaID]bool{}
		for _, r := range graph.ReturnReplicas(replica) {
			if r.ID == replica || r.Line != graph.Branch || seenReturns[r.ID] {
				t.Fatalf("hop %d: invalid return candidate: %+v", i, r)
			}
			seenReturns[r.ID] = true
		}
		hops := graph.ActiveHops()
		if len(hops) != transfers {
			t.Fatalf("hop %d: active movements=%d, transfers=%d", i, len(hops), transfers)
		}
		for _, h := range hops {
			if !h.Notify {
				t.Fatalf("hop %d: lost notice preference: %+v", i, h)
			}
		}
		if len(hops) > 0 && pattern != "pressure" {
			last := hops[len(hops)-1]
			if last.To == replica {
				if !seenReturns[last.From] {
					t.Fatalf("hop %d: previous stop missing from returns", i)
				}
				if h, ok := graph.Departure(last.From); !ok || h.ID != last.ID {
					t.Fatalf("hop %d: previous stop departure: %+v %t", i, h, ok)
				}
			}
		}
		journey := graph.Journey()
		if pattern == "accounts" && (journey.MachineTransfers != 0 || journey.MachineRoundTrips != 0) {
			t.Fatalf("account route counted as machine travel: %+v", journey)
		}
		if journey.Transfers != transfers {
			t.Fatalf("transfers %+v at hop %d", journey, i)
		}
		if pattern != "pressure" && to == 'A' && current.Key.Session != sid {
			t.Fatalf("return must select original: %s", current.Key)
		}
	}
	graph, _ := lineage.Read(host.LocalFS(), current.Path)
	expected := returns
	if j := graph.Journey(); j.RoundTrips != expected {
		t.Fatalf("journey %+v; origin returns=%d", j, expected)
	}
}
func findRouteSession(t *testing.T, l location, m agent.Module, in agent.Install, key agent.SessionKey) agent.Summary {
	t.Helper()
	for _, s := range listAgent(t, l, m, in) {
		if s.Key == key {
			return s
		}
	}
	t.Fatalf("missing %s on %s", key, l.m.Name)
	return agent.Summary{}
}

func TestLineageRoutesWithWorklessStops(t *testing.T) {
	for _, route := range []string{"ABABA", "ABCA", "ABCBCAB"} {
		for _, start := range []string{"claude", "codex"} {
			for _, pattern := range []string{"none", "alternating"} {
				t.Run(route+"/"+start+"/"+pattern, func(t *testing.T) { runLineageRoute(t, route, start, 3, pattern) })
			}
		}
	}
}

func TestLineageSeededLongHistories(t *testing.T) {
	if os.Getenv("HOPSESH_LINEAGE_NIGHTLY") != "1" {
		t.Skip("nightly temporal histories")
	}
	for seed := int64(0); seed < 12; seed++ {
		r := rand.New(rand.NewSource(seed))
		route := []byte{'A'}
		for i := 0; i < 24; i++ {
			next := byte('A' + r.Intn(3))
			if next == route[len(route)-1] {
				next = 'A' + (next-'A'+1)%3
			}
			route = append(route, next)
		}
		t.Run(fmt.Sprint(seed), func(t *testing.T) { runLineageRoute(t, string(route), "claude", int(seed%8), "alternating") })
	}
}

// Windows loopback SSH uses the same administrator account with isolated agent
// and configuration folders. These must not collapse into one native replica.
func TestLineageRoutesSeparateProfilesOnOneAccount(t *testing.T) {
	for _, start := range []string{"claude", "codex"} {
		t.Run(start, func(t *testing.T) { runLineageRoute(t, "ABABA", start, 3, "profiles") })
	}
}

func TestAccountProfileRoutes(t *testing.T) {
	for _, route := range []string{"ABABA", "ABCA", "ABCBCAB"} {
		for _, start := range []string{"claude", "codex"} {
			for mask := 0; mask < 8; mask++ {
				t.Run(fmt.Sprintf("%s/%s/%03b", route, start, mask), func(t *testing.T) { runLineageRoute(t, route, start, mask, "accounts") })
			}
		}
	}
}

// These payloads exceed the conservative incoming allowance at every stop; routes
// exercise archive transport, repeated summaries, same-branch returns and discovery.
func TestContextPressureRoutes(t *testing.T) {
	for _, route := range []string{"ABABA", "ABCA", "ABCBCAB"} {
		for _, start := range []string{"claude", "codex"} {
			t.Run(route+"/"+start, func(t *testing.T) { runLineageRoute(t, route, start, 2, "pressure") })
		}
	}
}
