package e2e

import (
	"context"
	"fmt"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
	"strings"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"os"
	"testing"
)

func TestLineageThreeDestinationsHaveIndependentReceipts(t *testing.T) {
	root := t.TempDir()
	a, b := newLocation(t, "A", root), newLocation(t, "B", root)
	seed(t, a)
	cl, cx := claude.New(), codex.New()
	ci := codexInstall(b)
	os.MkdirAll(ci.Root("home"), 0700)
	os.MkdirAll(b.in.Root("home"), 0700)
	ctx := context.Background()
	env := move.Env{StateDir: t.TempDir()}
	in := move.Input{Source: move.Side{Machine: a.m, Module: cl, Install: a.in}, Session: list(t, a)[sid], Target: move.Side{Machine: b.m, Module: cx, Install: ci}, Native: &move.NativeSide{Target: move.Side{Machine: b.m, Module: cl, Install: b.in}}}
	p, err := move.Build(ctx, in, move.Options{TargetDir: b.repo, Mark: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = move.Apply(ctx, p, in, env); err != nil {
		t.Fatal(err)
	}
	th := listAgent(t, b, cx, ci)[0]
	appendCodexTurn(t, th.Path, "New work on B", "ONLY-B-DELTA")
	th = listAgent(t, b, cx, ci)[0]
	lin, _ := lineage.Read(host.LocalFS(), th.Path)
	back := move.Input{Source: move.Side{Machine: b.m, Module: cx, Install: ci}, Session: th, Lineage: lin, Target: move.Side{Machine: b.m, Module: cl, Install: b.in}, Copies: []move.Copy{{Summary: list(t, b)[sid]}}}
	p, err = move.Build(ctx, back, move.Options{TargetDir: b.repo, Mark: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = move.Apply(ctx, p, back, env); err != nil {
		t.Fatal(err)
	}
	// B's Claude received the delta; A's original did not. Return Codex to A.
	th = listAgent(t, b, cx, ci)[0]
	lin, _ = lineage.Read(host.LocalFS(), th.Path)
	back.Session, back.Lineage = th, lin
	back.Target = move.Side{Machine: a.m, Module: cl, Install: a.in}
	back.Copies = []move.Copy{{Summary: list(t, a)[sid]}}
	p, err = move.Build(ctx, back, move.Options{TargetDir: a.repo})
	if err != nil {
		t.Fatal(err)
	}
	if mentions(readAll(t, a, cl, a.in, list(t, a)[sid]), "ONLY-B-DELTA") {
		t.Fatal("bad audit fixture")
	}
	if p.Continue.Relation != move.RelationAppend {
		t.Fatalf("A lacks ONLY-B-DELTA, but planner says relation=%s blockers=%v", p.Continue.Relation, p.Blockers)
	}
}

func TestLineageForkBranchesRemainIndependent(t *testing.T) {
	root := t.TempDir()
	a, b := newLocation(t, "A", root), newLocation(t, "B", root)
	seed(t, a)
	ctx := context.Background()
	env := move.Env{StateDir: t.TempDir()}
	in := input(t, a, b)
	p, err := move.Build(ctx, in, move.Options{TargetDir: b.repo, Mark: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = move.Apply(ctx, p, in, env); err != nil {
		t.Fatal(err)
	}
	appendTurn(t, list(t, a)[sid].Path, "original branch work")
	appendTurn(t, list(t, b)[sid].Path, "other branch work")
	in = input(t, b, a)
	p, err = move.Build(ctx, in, move.Options{TargetDir: a.repo, Conflict: move.ConflictKeepBoth})
	if err != nil {
		t.Fatal(err)
	}
	forkID := string(p.Placement.Key.Session)
	if _, err = move.Apply(ctx, p, in, env); err != nil {
		t.Fatal(err)
	}
	original, fork := list(t, a)[sid], list(t, a)[forkID]
	ol, _ := lineage.Read(host.LocalFS(), original.Path)
	fl, _ := lineage.Read(host.LocalFS(), fork.Path)
	t.Run("both selectable", func(t *testing.T) {
		inv := &app.Inventory{Entries: []app.Entry{{Machine: "A", Agent: "claude", Session: original, Lineage: ol}, {Machine: "A", Agent: "claude", Session: fork, Lineage: fl}}}
		if got := len(inv.Items()); got != 2 {
			t.Fatalf("Keep both created two IDs but the shared GUI/TUI inventory produces %d row(s)", got)
		}
	})
	t.Run("replace only matching branch", func(t *testing.T) {
		in = input(t, b, a)
		in.Copies = []move.Copy{{Summary: original, Lineage: ol}, {Summary: fork, Lineage: fl}}
		p, err := move.Build(ctx, in, move.Options{TargetDir: a.repo, Conflict: move.ConflictReplace})
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range p.SetAside {
			if s.Key == fork.Key {
				t.Fatalf("bringing original %s back also sets aside independent fork %s", in.Session.Key, s.Key)
			}
		}
	})
}

func TestLineageForkMultiHopDoesNotReplaceOriginal(t *testing.T) {
	root := t.TempDir()
	a, b, c := newLocation(t, "A", root), newLocation(t, "B", root), newLocation(t, "C", root)
	seed(t, a)
	ctx := context.Background()
	env := move.Env{StateDir: t.TempDir()}
	original := list(t, a)[sid]
	before, _ := os.ReadFile(original.Path)
	in := input(t, a, b)
	p, err := move.Build(ctx, in, move.Options{TargetDir: b.repo, Fork: true, Mark: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = move.Apply(ctx, p, in, env); err != nil {
		t.Fatal(err)
	}
	forkID := string(p.Placement.Key.Session)
	if forkID == sid {
		t.Fatal("fork needs its own native ID")
	}
	fl, err := lineage.Read(host.LocalFS(), list(t, b)[forkID].Path)
	if err != nil {
		t.Fatal(err)
	}
	originGraph, _ := lineage.Read(host.LocalFS(), original.Path)
	if fl.Family != originGraph.Family || fl.Branch == originGraph.Branch {
		t.Fatal("fork ancestry or independent identity missing")
	}
	for i, leg := range [][2]location{{b, c}, {c, b}, {b, a}} {
		source := list(t, leg[0])[forkID]
		appendTurn(t, source.Path, fmt.Sprintf("fork step %d", i))
		source = list(t, leg[0])[forkID]
		graph, e := lineage.Read(host.LocalFS(), source.Path)
		if e != nil {
			t.Fatal(e)
		}
		in = move.Input{Source: move.Side{Machine: leg[0].m, Module: claude.New(), Install: leg[0].in}, Session: source, Lineage: graph, Target: move.Side{Machine: leg[1].m, Module: claude.New(), Install: leg[1].in}}
		for _, s := range list(t, leg[1]) {
			m, _ := lineage.Read(host.LocalFS(), s.Path)
			in.Copies = append(in.Copies, move.Copy{Summary: s, Lineage: m})
		}
		p, e = move.Build(ctx, in, move.Options{TargetDir: leg[1].repo, Mark: true})
		if e != nil {
			t.Fatal(e)
		}
		for _, s := range p.SetAside {
			if s.Key.Session == sid {
				t.Fatal("fork move selected original")
			}
		}
		if _, e = move.Apply(ctx, p, in, env); e != nil {
			t.Fatal(e)
		}
	}
	after, _ := os.ReadFile(original.Path)
	if string(after) != string(before) {
		t.Fatal("fork travel changed original native conversation")
	}
	fork := list(t, a)[forkID]
	graph, err := lineage.Read(host.LocalFS(), fork.Path)
	if err != nil {
		t.Fatal(err)
	}
	if j := graph.Journey(); !j.Fork || j.Transfers != 3 || j.RoundTrips != 1 {
		t.Fatalf("fork journey: %+v", j)
	}
	inv := &app.Inventory{Entries: []app.Entry{{Machine: "A", Session: original, Lineage: originGraph}, {Machine: "A", Session: fork, Lineage: graph}}}
	if len(inv.Items()) != 2 {
		t.Fatal("fork and original must each be selectable")
	}
}

func TestLineageDestinationChangedAfterPlanning(t *testing.T) {
	root := t.TempDir()
	a, b := newLocation(t, "A", root), newLocation(t, "B", root)
	seed(t, a)
	ctx := context.Background()
	env := move.Env{StateDir: t.TempDir()}
	in := input(t, a, b)
	p, err := move.Build(ctx, in, move.Options{TargetDir: b.repo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = move.Apply(ctx, p, in, env); err != nil {
		t.Fatal(err)
	}
	appendTurn(t, list(t, b)[sid].Path, "new work at B")
	in = input(t, b, a)
	p, err = move.Build(ctx, in, move.Options{TargetDir: a.repo})
	if err != nil {
		t.Fatal(err)
	}
	target := list(t, a)[sid]
	appendTurn(t, target.Path, "concurrent A work after planning")
	before, _ := os.ReadFile(target.Path)
	if _, err = move.Apply(ctx, p, in, env); err == nil {
		t.Fatal("stale native replacement must be refused")
	}
	after, _ := os.ReadFile(target.Path)
	if string(before) != string(after) {
		t.Fatal("stale replacement erased concurrent work")
	}
}

func TestLineageNoWorkReturnDoesNotCount(t *testing.T) {
	for _, name := range []string{"claude", "codex"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			a, b := newLocation(t, "A", root), newLocation(t, "B", root)
			seed(t, a)
			ctx := context.Background()
			env := move.Env{StateDir: t.TempDir()}
			in := input(t, a, b)
			if name == "codex" {
				in.Target.Module = codex.New()
				in.Target.Install = codexInstall(b)
				os.MkdirAll(in.Target.Install.Root("home"), 0700)
			}
			p, err := move.Build(ctx, in, move.Options{TargetDir: b.repo})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = move.Apply(ctx, p, in, env); err != nil {
				t.Fatal(err)
			}
			dst := findRouteSession(t, b, in.Target.Module, in.Target.Install, p.Placement.Key)
			graph, _ := lineage.Read(host.LocalFS(), dst.Path)
			back := move.Input{Source: in.Target, Session: dst, Lineage: graph, Target: in.Source, Copies: []move.Copy{{Summary: list(t, a)[sid]}}}
			p, err = move.Build(ctx, back, move.Options{TargetDir: a.repo})
			if err != nil {
				t.Fatal(err)
			}
			if !p.NoWork || len(p.Blockers) > 0 {
				t.Fatal("no-work return must synchronize only metadata", p.Blockers)
			}
			nativeBefore, _ := os.ReadFile(list(t, a)[sid].Path)
			if _, err = move.Apply(ctx, p, back, env); err != nil {
				t.Fatal(err)
			}
			nativeAfter, _ := os.ReadFile(list(t, a)[sid].Path)
			if string(nativeBefore) != string(nativeAfter) {
				t.Fatal("no-op changed native bytes")
			}
			m, _ := lineage.Read(host.LocalFS(), dst.Path)
			if m.Journey().Transfers != 1 {
				t.Fatal("no-op incremented route")
			}
		})
	}
}

func TestLineageRewrittenHistoryRequiresSeparateSnapshot(t *testing.T) {
	root := t.TempDir()
	a, b, c := newLocation(t, "A", root), newLocation(t, "B", root), newLocation(t, "C", root)
	seed(t, a)
	ctx := context.Background()
	env := move.Env{StateDir: t.TempDir()}
	in := input(t, a, b)
	p, err := move.Build(ctx, in, move.Options{TargetDir: b.repo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = move.Apply(ctx, p, in, env); err != nil {
		t.Fatal(err)
	}
	native := list(t, b)[sid]
	raw, _ := os.ReadFile(native.Path)
	raw = []byte(strings.Replace(string(raw), "What is the codeword", "What is the secretxx", 1))
	os.WriteFile(native.Path, raw, 0600)
	in = input(t, b, c)
	p, err = move.Build(ctx, in, move.Options{TargetDir: c.repo})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blockers) == 0 {
		t.Fatal("changed anchors must block automatic append")
	}
	p, err = move.Build(ctx, in, move.Options{TargetDir: c.repo, Fork: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blockers) > 0 {
		t.Fatal(p.Blockers)
	}
	if _, err = move.Apply(ctx, p, in, env); err != nil {
		t.Fatal(err)
	}
	dst := findRouteSession(t, c, in.Target.Module, c.in, p.Placement.Key)
	m, err := lineage.Read(host.LocalFS(), dst.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Journey().Fork {
		t.Fatal("snapshot lacks fork")
	}
	_, id, _ := m.FindEndpoint(dst.Key, c.m.Facts.Endpoint)
	state, _ := m.LatestState(id)
	if len(state.Loss) == 0 {
		t.Fatal("unverified snapshot must retain loss")
	}
}

func TestLineageOriginalAndSiblingForksMixedRoutes(t *testing.T) {
	for _, forkAgent := range []string{"claude", "codex"} {
		t.Run(forkAgent, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			places := map[byte]location{'A': newLocation(t, "A", root), 'B': newLocation(t, "B", root), 'C': newLocation(t, "C", root)}
			seed(t, places['A'])
			mods := map[string]agent.Module{"claude": claude.New(), "codex": codex.New()}
			ins := map[byte]map[string]agent.Install{}
			for c, l := range places {
				ins[c] = map[string]agent.Install{"claude": l.in, "codex": codexInstall(l)}
				for _, i := range ins[c] {
					os.MkdirAll(i.Root("home"), 0700)
				}
			}
			env := move.Env{StateDir: t.TempDir()}
			original := list(t, places['A'])[sid]
			rawBefore, _ := os.ReadFile(original.Path)
			transfer := func(from, to byte, agentName, target string, source agent.Summary, fork bool) agent.Summary {
				graph, err := lineage.Read(host.LocalFS(), source.Path)
				if err != nil {
					t.Fatal(err)
				}
				in := move.Input{Source: move.Side{Machine: places[from].m, Module: mods[agentName], Install: ins[from][agentName]}, Session: source, Lineage: graph, Target: move.Side{Machine: places[to].m, Module: mods[target], Install: ins[to][target]}}
				for _, s := range listAgent(t, places[to], mods[target], ins[to][target]) {
					cm, e := lineage.Read(host.LocalFS(), s.Path)
					if e != nil {
						t.Fatal(e)
					}
					in.Copies = append(in.Copies, move.Copy{Summary: s, Lineage: cm})
				}
				p, e := move.Build(ctx, in, move.Options{TargetDir: places[to].repo, Fork: fork})
				if e != nil {
					t.Fatal(e)
				}
				if len(p.Blockers) > 0 {
					t.Fatal(p.Blockers)
				}
				if _, e = move.Apply(ctx, p, in, env); e != nil {
					t.Fatal(e)
				}
				return findRouteSession(t, places[to], mods[target], ins[to][target], p.Placement.Key)
			}
			left := transfer('A', 'B', "claude", forkAgent, original, true)
			right := transfer('A', 'C', "claude", "codex", original, true)
			leftGraph, _ := lineage.Read(host.LocalFS(), left.Path)
			rightGraph, _ := lineage.Read(host.LocalFS(), right.Path)
			if leftGraph.Branch == rightGraph.Branch || leftGraph.Family != rightGraph.Family {
				t.Fatal("sibling fork identity")
			}
			current := left
			currentAgent := forkAgent
			route := "BCBCAB"
			var sentinels []string
			for i := 0; i < len(route)-1; i++ {
				sentinel := fmt.Sprintf("SIBLING-LEFT-WORK-%d", i)
				sentinels = append(sentinels, sentinel)
				if currentAgent == "claude" {
					appendTurn(t, current.Path, sentinel)
				} else {
					appendCodexTurn(t, current.Path, sentinel, "ack")
				}
				next := "claude"
				if i%2 == 0 {
					next = "codex"
				}
				current = transfer(route[i], route[i+1], currentAgent, next, current, false)
				currentAgent = next
				seg := readAll(t, places[route[i+1]], mods[next], ins[route[i+1]][next], current)
				var text strings.Builder
				for _, n := range seg.Nodes {
					text.WriteString(n.Text)
					text.WriteByte('\n')
				}
				for _, s := range sentinels {
					if strings.Count(text.String(), s) != 1 {
						t.Fatalf("fork route duplicated or lost %s", s)
					}
				}
			}
			// Advance the sibling and original independently after the fork travelled home.
			appendCodexTurn(t, right.Path, "SIBLING-RIGHT-ONLY", "ack")
			right = transfer('C', 'A', "codex", "claude", right, false)
			originalAfter, _ := os.ReadFile(original.Path)
			if string(originalAfter) != string(rawBefore) {
				t.Fatal("forks modified original native bytes")
			}
			appendTurn(t, original.Path, "ORIGINAL-ONLY")
			original = transfer('A', 'C', "claude", "codex", list(t, places['A'])[sid], false)
			for _, s := range []agent.Summary{original, right} {
				var seg ir.Segment
				if s.Key == right.Key {
					seg = readAll(t, places['A'], mods["claude"], ins['A']["claude"], s)
				} else {
					seg = readAll(t, places['C'], mods["codex"], ins['C']["codex"], s)
				}
				if mentions(seg, "SIBLING-LEFT-WORK-") {
					t.Fatal("sibling/parent received unrelated fork work")
				}
			}
		})
	}
}
