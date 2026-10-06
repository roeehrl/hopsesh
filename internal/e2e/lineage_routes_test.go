package e2e

import (
	"context"
	"fmt"
	"math/rand"
	"os"
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
		places[c] = l
		installs[c] = map[string]agent.Install{"claude": l.in, "codex": codexInstall(l)}
		for _, in := range installs[c] {
			if err := os.MkdirAll(in.Root("home"), 0o700); err != nil {
				t.Fatal(err)
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
		if pattern == "all" || pattern == "alternating" && i%2 == 0 {
			sentinel := fmt.Sprintf("ROUTE-WORK-%d-UNIQUE", i)
			sentinels = append(sentinels, sentinel)
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
		p, err := move.Build(ctx, input, move.Options{TargetDir: dst.repo, Mark: true})
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
		var text strings.Builder
		for _, n := range seg.Nodes {
			text.WriteString(n.Text)
			text.WriteByte('\n')
		}
		for _, s := range sentinels {
			if n := strings.Count(text.String(), s); n != 1 {
				t.Fatalf("hop %d: %s appears %d times", i, s, n)
			}
		}
		graph, e := lineage.Read(host.LocalFS(), current.Path)
		if e != nil {
			t.Fatal(e)
		}
		journey := graph.Journey()
		if journey.Transfers != transfers {
			t.Fatalf("transfers %+v at hop %d", journey, i)
		}
		if to == 'A' && current.Key.Session != sid {
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
