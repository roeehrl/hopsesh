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
	"github.com/roeehrl/hopsesh/agents/opencode"
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

// runLineageRoute runs a route whose stops B and C use Codex where mask has their bit
// (B 2, C 4), else Claude Code; A is start's.
func runLineageRoute(t *testing.T, route, start string, mask int, patterns ...string) {
	t.Helper()
	at := map[byte]string{'A': start}
	for _, c := range []byte{'B', 'C'} {
		at[c] = "claude"
		if mask&(1<<uint(c-'A')) != 0 {
			at[c] = "codex"
		}
	}
	runRoute(t, route, at, patterns...)
}

// runRoute runs a route through stops A, B and C, each with its agent (at), starting at
// A and returning there to the original.
func runRoute(t *testing.T, route string, at map[byte]string, patterns ...string) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	start := at['A']
	places := map[byte]location{}
	mods := map[string]agent.Module{"claude": claude.New(), "codex": codex.New(), "opencode": opencode.New()}
	installs := map[byte]map[string]agent.Install{}
	withOC := false
	for _, c := range []byte(route) {
		withOC = withOC || at[c] == "opencode"
	}
	ocBin := ""
	if withOC {
		ocBin = ocOnPath(t, root)
	}
	for _, c := range []byte{'A', 'B', 'C'} {
		l := newLocation(t, string(c), root)
		if ocBin != "" {
			l.m.Facts.Binaries["opencode"] = agent.BinaryFact{Path: ocBin, Version: "opencode v2.3.0"}
		}
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
		if ocBin != "" {
			installs[c]["opencode"] = ocInstall(l)
		}
		if len(patterns) > 0 && patterns[0] == "accounts" {
			places[c].m.Facts.Endpoint = strings.Repeat("e", 64)
			for id, in := range installs[c] {
				in.Profile = &agent.RuntimeProfile{ID: string(c) + "-" + id, Endpoint: places[c].m.Facts.Endpoint, Agent: agent.ID(id), Name: "Personal", Tags: []string{"Personal"}, Root: in.Root("home"), Binding: string(c) + "-binding", Generation: 1}
				installs[c][id] = in
			}
		}
		for _, in := range installs[c] {
			if err := os.MkdirAll(in.Main(), 0o700); err != nil {
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
		cl, w := mods["claude"], mods[start]
		seg := readAll(t, places['A'], cl, installs['A']["claude"], list(t, places['A'])[sid])
		j, _ := journal.New(t.TempDir(), journal.KindContinue, "seed")
		h, _ := places['A'].m.For(ctx, w.Spec(), installs['A'][start], j)
		seeded := sid
		if start == "opencode" {
			seeded = "ses_e29a0000000eRouteSeed00000"
		}
		_, err := w.(agent.Writer).Write(ctx, h, installs['A'][start], ir.WriteRequest{SessionID: seeded, Header: seg.Header, Mode: ir.WriteNew, Items: []ir.Item{{Node: "seed", Role: ir.RoleUser, Text: "ROUTE-SEED"}}})
		if err != nil {
			t.Fatal(err)
		}
		current = listAgent(t, places['A'], w, installs['A'][start])[0]
	}
	original := current.Key.Session
	current.Key.Profile = installs['A'][currentAgent].ProfileID()
	originalKey := current.Key
	// The sessions at A of this conversation, and the original's text when it was last left.
	aCopies := map[agent.SessionID]bool{original: true}
	var originalText string
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
			switch currentAgent {
			case "claude":
				appendTurn(t, current.Path, sentinel)
			case "opencode":
				ocTurn(t, src, current.Key.Session, sentinel, "reply to "+fmt.Sprint(i))
			default:
				appendCodexTurn(t, current.Path, sentinel, "reply to "+fmt.Sprint(i))
			}
		}
		current = findRouteSession(t, src, mods[currentAgent], installs[from][currentAgent], current.Key)
		if from == 'A' && current.Key.Session == original {
			originalText = routeText(t, src, mods[currentAgent], installs[from][currentAgent], current)
		}
		lin, err := lineage.Read(host.LocalFS(), current.Path)
		if err != nil {
			t.Fatal(err)
		}
		nextAgent := at[to]
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
		p, err := move.Build(ctx, input, move.Options{TargetDir: dst.repo, Notify: true})
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
		if pattern != "pressure" && to == 'A' && current.Key.Session != original {
			if !rollsOver(mods[nextAgent]) {
				t.Fatalf("return must select original: %s", current.Key)
			}
			// D4: the return continues a copy here in a new session, and that copy stays as it was.
			if len(p.Blockers) == 0 && !p.NoWork {
				if aCopies[current.Key.Session] {
					t.Fatalf("hop %d: the return wrote into the existing copy %s", i, current.Key)
				}
				var rolled *lineage.Rollover
				for _, h := range graph.Hops {
					if h.ID == p.OperationID && h.To == replica {
						rolled = h.Rollover
					}
				}
				if rolled == nil {
					t.Fatalf("hop %d: the return to %s records no rollover", i, current.Key)
				}
				var continued agent.SessionID
				for _, r := range graph.Replicas {
					if r.ID == rolled.Replica {
						continued = r.Key.Session
					}
				}
				if !aCopies[continued] {
					t.Fatalf("hop %d: the return to %s continues %q, not a copy here", i, current.Key, continued)
				}
				aCopies[current.Key.Session] = true
			}
			orig := findRouteSession(t, dst, mods[nextAgent], installs[to][nextAgent], originalKey)
			if got := routeText(t, dst, mods[nextAgent], installs[to][nextAgent], orig); got != originalText {
				t.Fatalf("hop %d: the original changed on a return:\n%s\nwas:\n%s", i, got, originalText)
			}
		}
	}
	graph, _ := lineage.Read(host.LocalFS(), current.Path)
	expected := returns
	if j := graph.Journey(); j.RoundTrips != expected {
		t.Fatalf("journey %+v; origin returns=%d", j, expected)
	}
}

// routeText is a session's text as its agent reads it.
func routeText(t *testing.T, l location, m agent.Module, in agent.Install, s agent.Summary) string {
	t.Helper()
	var b strings.Builder
	for _, n := range readAll(t, l, m, in, s).Nodes {
		b.WriteString(n.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

// rollsOver reports whether a return into the agent's existing copy continues it in a
// new session instead (OpenCode cannot add to a session: decision D4).
func rollsOver(m agent.Module) bool {
	w, ok := m.(agent.Writer)
	return ok && !w.Profile(agent.Install{}).PortableAppend && agent.IsVirtual(m)
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

// Routes through Claude Code, Codex and OpenCode: every ordered pair of agents with
// OpenCode on one side alternates (ABABA), each triangle goes round (ABCA), and two longer
// routes come back through both other stops. The nightly run takes every assignment of
// agents to the stops. OpenCode is the stand-in 2.3.0 (not on Windows: it runs as a link
// to the test binary).
func TestLineageRoutesThreeAgents(t *testing.T) {
	agents := []string{"claude", "codex", "opencode"}
	type routeCase struct {
		route   string
		a, b, c string
	}
	var cases []routeCase
	if os.Getenv("HOPSESH_LINEAGE_NIGHTLY") == "1" {
		for _, route := range []string{"ABABA", "ABCA", "ABCBCAB"} {
			for _, a := range agents {
				for _, b := range agents {
					for _, c := range agents {
						if route == "ABABA" && c != "claude" {
							continue // no stop C
						}
						cases = append(cases, routeCase{route, a, b, c})
					}
				}
			}
		}
	} else {
		for _, a := range agents {
			for _, b := range agents {
				if a == "opencode" || b == "opencode" {
					cases = append(cases, routeCase{"ABABA", a, b, "claude"})
				}
				for _, c := range agents {
					if a != b && b != c && a != c && (a == "opencode" || b == "opencode" || c == "opencode") {
						cases = append(cases, routeCase{"ABCA", a, b, c})
					}
				}
			}
		}
		cases = append(cases, routeCase{"ABCBCAB", "opencode", "claude", "codex"}, routeCase{"ABCBCAB", "claude", "opencode", "codex"})
	}
	for _, c := range cases {
		name := c.route + "/" + c.a + "-" + c.b
		if c.route != "ABABA" {
			name += "-" + c.c
		}
		t.Run(name, func(t *testing.T) {
			runRoute(t, c.route, map[byte]string{'A': c.a, 'B': c.b, 'C': c.c})
		})
	}
}
