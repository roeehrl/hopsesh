package e2e

import (
	"context"
	"fmt"
	"os"
	"reflect"
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

// All reads and writes use the real modules, isolated in fixture homes. A native
// title mark is metadata; enabling movement notices must never add conversation work.
func TestMovementTransferReturn(t *testing.T) {
	for _, from := range []string{"claude", "codex"} {
		for _, to := range []string{"claude", "codex"} {
			t.Run(from+"-"+to, func(t *testing.T) {
				var nodeCounts []int
				for _, notify := range []bool{false, true} {
					t.Run(fmt.Sprintf("notify=%t", notify), func(t *testing.T) {
						a, b, in := movementInput(t, from, to)
						ctx := context.Background()
						env := move.Env{StateDir: t.TempDir()}
						before := readAll(t, a, in.Source.Module, in.Source.Install, in.Session)
						p, res := applyMovement(t, in, move.Options{TargetDir: b.repo, Mark: true, Notify: notify}, env)
						left := findRouteSession(t, a, in.Source.Module, in.Source.Install, in.Session.Key)
						dst := findRouteSession(t, b, in.Target.Module, in.Target.Install, p.Placement.Key)
						if got := readAll(t, a, in.Source.Module, in.Source.Install, left); !reflect.DeepEqual(before.Nodes, got.Nodes) {
							t.Fatal("movement notice changed the source conversation nodes")
						}
						if from != to && (left.Mark == nil || left.Mark.Kind != agent.MarkPrepared) {
							t.Fatalf("transfer only prepares the other agent: %+v", left.Mark)
						}
						nodeCounts = append(nodeCounts, len(readAll(t, b, in.Target.Module, in.Target.Install, dst).Nodes))
						for _, s := range []agent.Summary{left, dst} {
							g := movementGraph(t, s)
							hops := g.ActiveHops()
							if len(hops) != 1 || hops[0].ID != p.OperationID || hops[0].Notify != notify {
								t.Fatalf("persisted movement: %+v", hops)
							}
							h := hops[0]
							if d, ok := g.Departure(h.From); ok != notify || ok && d.ID != h.ID {
								t.Fatalf("source departure: %+v, %t", d, ok)
							}
							if _, ok := g.Departure(h.To); ok {
								t.Fatal("arrival must not have a departure notice")
							}
							returns := g.ReturnReplicas(h.To)
							if len(returns) != 1 || returns[0].ID != h.From {
								t.Fatalf("notify=%t must retain original return candidate: %+v", notify, returns)
							}
						}
						// A completed retry uses the persisted result and adds no native records.
						native := movementBytes(t, dst.Path)
						receipt := movementBytes(t, lineage.PathFor(dst.Path))
						again, err := move.Apply(ctx, p, in, env)
						if err != nil || again.Journal != res.Journal {
							t.Fatalf("completed retry: %+v %v", again, err)
						}
						if string(native) != string(movementBytes(t, dst.Path)) || string(receipt) != string(movementBytes(t, lineage.PathFor(dst.Path))) {
							t.Fatal("completed retry changed native work or movement metadata")
						}
						back := move.Input{Source: in.Target, Session: dst, Lineage: movementGraph(t, dst), Target: in.Source,
							Copies: []move.Copy{{Summary: left, Lineage: movementGraph(t, left)}}}
						empty, err := move.Build(ctx, back, move.Options{TargetDir: a.repo, Notify: notify})
						if err != nil || !empty.NoWork || len(empty.Blockers) != 0 {
							t.Fatalf("notice alone is not returnable work: %+v %v", empty, err)
						}
						if to == "claude" {
							appendTurn(t, dst.Path, "MOVEMENT-RETURN-WORK")
						} else {
							appendCodexTurn(t, dst.Path, "MOVEMENT-RETURN-WORK", "MOVEMENT-RETURN-REPLY")
						}
						back.Session = findRouteSession(t, b, in.Target.Module, in.Target.Install, dst.Key)
						returned, _ := applyMovement(t, back, move.Options{TargetDir: a.repo, Mark: true, Notify: notify}, env)
						if returned.Placement.Key != in.Session.Key {
							t.Fatalf("return lost original native identity: %s", returned.Placement.Key)
						}
						home := findRouteSession(t, a, in.Source.Module, in.Source.Install, returned.Placement.Key)
						g := movementGraph(t, home)
						hops := g.ActiveHops()
						if len(hops) != 2 || hops[1].Notify != notify || g.Journey().RoundTrips != 1 {
							t.Fatalf("return metadata: %+v", g)
						}
						if _, ok := g.Departure(hops[1].To); ok || home.Mark != nil {
							t.Fatal("return must clear the original's movement notice and title mark")
						}
						if got := g.ReturnReplicas(hops[1].To); len(got) != 1 || got[0].ID != hops[0].To {
							t.Fatalf("return candidate after round trip: %+v", got)
						}
						if !mentions(readAll(t, a, in.Source.Module, in.Source.Install, home), "MOVEMENT-RETURN-WORK") {
							t.Fatal("return lost native work")
						}
					})
				}
				if len(nodeCounts) == 2 && nodeCounts[0] != nodeCounts[1] {
					t.Fatalf("notice added destination conversation nodes: off=%d on=%d", nodeCounts[0], nodeCounts[1])
				}
			})
		}
	}
}

func TestMovementForkAndUndo(t *testing.T) {
	for _, from := range []string{"claude", "codex"} {
		for _, to := range []string{"claude", "codex"} {
			t.Run(from+"-"+to, func(t *testing.T) {
				a, b, in := movementInput(t, from, to)
				before := readAll(t, a, in.Source.Module, in.Source.Install, in.Session)
				env := move.Env{StateDir: t.TempDir()}
				p, res := applyMovement(t, in, move.Options{TargetDir: b.repo, Fork: true, Mark: true, Notify: true}, env)
				dst := findRouteSession(t, b, in.Target.Module, in.Target.Install, p.Placement.Key)
				g := movementGraph(t, dst)
				hops := g.ActiveHops()
				if len(hops) != 1 || !hops[0].Fork || !hops[0].Notify {
					t.Fatalf("fork receipt: %+v", hops)
				}
				h := hops[0]
				if got := g.ReturnReplicas(h.To); len(got) != 0 {
					t.Fatalf("a fork must not offer its parent as a same-branch return: %+v", got)
				}
				if departure, ok := g.Departure(h.From); !ok || !departure.Fork {
					t.Fatalf("parent needs a fork notice: %+v %t", departure, ok)
				}
				left := findRouteSession(t, a, in.Source.Module, in.Source.Install, in.Session.Key)
				if got := readAll(t, a, in.Source.Module, in.Source.Install, left); !reflect.DeepEqual(before.Nodes, got.Nodes) {
					t.Fatal("fork notice changed the parent's conversation")
				}
				j, err := journal.Load(env.StateDir, res.Journal)
				if err != nil {
					t.Fatal(err)
				}
				if err := j.Undo(context.Background(), journal.Files(func(string) (host.FS, error) { return host.LocalFS(), nil }), false); err != nil {
					t.Fatal(err)
				}
				g = movementGraph(t, left)
				if len(g.Hops) != 1 || len(g.Compensations) != 1 || len(g.ActiveHops()) != 0 {
					t.Fatalf("undo must retain history but compensate movement: %+v", g)
				}
				if _, ok := g.Departure(h.From); ok || len(g.ReturnReplicas(h.From)) != 0 {
					t.Fatal("undo left a notice or return action")
				}
				if _, err := os.Stat(dst.Path); !os.IsNotExist(err) {
					t.Fatalf("undo left fork transcript: %v", err)
				}
				left = findRouteSession(t, a, in.Source.Module, in.Source.Install, in.Session.Key)
				if got := readAll(t, a, in.Source.Module, in.Source.Install, left); !reflect.DeepEqual(before.Nodes, got.Nodes) {
					t.Fatal("undo changed the parent's conversation")
				}
			})
		}
	}
}

func TestMovementNativeBackupIsNotReturnDestination(t *testing.T) {
	a, b, in := movementInput(t, "claude", "codex")
	if err := os.MkdirAll(b.in.Root("home"), 0o700); err != nil {
		t.Fatal(err)
	}
	in.Native = &move.NativeSide{Target: move.Side{Machine: b.m, Module: claude.New(), Install: b.in}}
	before := readAll(t, a, in.Source.Module, in.Source.Install, in.Session)
	p, _ := applyMovement(t, in, move.Options{TargetDir: b.repo, Mark: true, Notify: true}, move.Env{StateDir: t.TempDir()})
	dst := findRouteSession(t, b, in.Target.Module, in.Target.Install, p.Placement.Key)
	g := movementGraph(t, dst)
	if len(g.Hops) != 2 || len(g.ActiveHops()) != 1 {
		t.Fatalf("native backup must not become an active movement: %+v", g.Hops)
	}
	h := g.ActiveHops()[0]
	if returns := g.ReturnReplicas(h.To); len(returns) != 1 || returns[0].ID != h.From {
		t.Fatalf("unvisited backup offered as return: %+v", returns)
	}
	backup := list(t, b)[sid]
	if backup.Mark == nil || backup.Mark.Kind != agent.MarkPrepared {
		t.Fatalf("backup may report preparation, never actual continuation: %+v", backup.Mark)
	}
	if !reflect.DeepEqual(before.Nodes,
		readAll(t, a, in.Source.Module, in.Source.Install, list(t, a)[sid]).Nodes) {
		t.Fatal("backup bookkeeping changed source conversation nodes")
	}
}

func movementInput(t *testing.T, from, to string) (location, location, move.Input) {
	t.Helper()
	root := t.TempDir()
	a, b := newLocation(t, "A", root), newLocation(t, "B", root)
	seed(t, a)
	mods := map[string]agent.Module{"claude": claude.New(), "codex": codex.New()}
	srcInstall, dstInstall := a.in, b.in
	src := list(t, a)[sid]
	if from == "codex" {
		srcInstall = codexInstall(a)
		j, err := journal.New(t.TempDir(), journal.KindContinue, "seed")
		if err != nil {
			t.Fatal(err)
		}
		h, err := a.m.For(context.Background(), mods[from].Spec(), srcInstall, j)
		if err != nil {
			t.Fatal(err)
		}
		_, err = mods[from].(agent.Writer).Write(context.Background(), h, srcInstall, ir.WriteRequest{
			SessionID: sid, Header: ir.Header{CWD: a.repo}, Mode: ir.WriteNew,
			Items: []ir.Item{{Node: "seed", Role: ir.RoleUser, Text: "MOVEMENT-SEED"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		src = listAgent(t, a, mods[from], srcInstall)[0]
	}
	if to == "codex" {
		dstInstall = codexInstall(b)
	}
	if err := os.MkdirAll(dstInstall.Root("home"), 0o700); err != nil {
		t.Fatal(err)
	}
	return a, b, move.Input{Source: move.Side{Machine: a.m, Module: mods[from], Install: srcInstall}, Session: src,
		Target: move.Side{Machine: b.m, Module: mods[to], Install: dstInstall}}
}

func applyMovement(t *testing.T, in move.Input, opt move.Options, env move.Env) (*move.Plan, *move.Result) {
	t.Helper()
	p, err := move.Build(context.Background(), in, opt)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blockers) != 0 {
		t.Fatalf("movement blocked: %v", p.Blockers)
	}
	res, err := move.Apply(context.Background(), p, in, env)
	if err != nil {
		t.Fatal(err)
	}
	return p, res
}

func movementGraph(t *testing.T, s agent.Summary) *lineage.Manifest {
	t.Helper()
	g, err := lineage.Read(host.LocalFS(), s.Path)
	if err != nil || g == nil {
		t.Fatalf("movement metadata for %s: %v", s.Key, err)
	}
	return g
}

func movementBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
