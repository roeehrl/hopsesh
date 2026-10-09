package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Both destinations commit their native files before either can acknowledge the
// common source. Expected operations/branches come from issued commands, not
// graph traversal. Receipt contention and retries may not lose either sibling,
// invent an authoritative departure, or rewrite later destination work.
func TestLineageConcurrentNativeTransferEffects(t *testing.T) {
	for _, targets := range [][2]string{{"claude", "claude"}, {"codex", "codex"}, {"claude", "codex"}} {
		for _, forks := range [][2]bool{{false, false}, {true, true}, {false, true}} {
			t.Run(fmt.Sprintf("%s-%s/forks-%t-%t", targets[0], targets[1], forks[0], forks[1]), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				root := t.TempDir()
				a := newLocation(t, "A", root)
				seed(t, a)
				destinations := [2]location{newLocation(t, "B", root), newLocation(t, "C", root)}
				source := list(t, a)[sid]
				beforeSource := movementBytes(t, source.Path)
				var inputs [2]move.Input
				var plans [2]*move.Plan
				var envs [2]move.Env
				for i, destination := range destinations {
					module, install := agent.Module(claude.New()), destination.in
					if targets[i] == "codex" {
						module, install = codex.New(), codexInstall(destination)
					}
					if err := os.MkdirAll(install.Root("home"), 0700); err != nil {
						t.Fatal(err)
					}
					inputs[i] = move.Input{Source: move.Side{Machine: a.m, Module: claude.New(), Install: a.in}, Session: source,
						Target: move.Side{Machine: destination.m, Module: module, Install: install}}
					var err error
					plans[i], err = move.Build(ctx, inputs[i], move.Options{OperationID: fmt.Sprintf("concurrent-native-operation-%d", i), TargetDir: destination.repo, Fork: forks[i], Notify: true})
					if err != nil || len(plans[i].Blockers) != 0 {
						t.Fatal("fixture requires two accepted plans", err)
					}
					envs[i] = move.Env{StateDir: t.TempDir()}
				}
				// Identity persistence precedes the concurrent actions, as it does
				// for established devices. No real agent or cloud account is used.
				if err := a.m.CommitIdentity(ctx); err != nil {
					t.Fatal(err)
				}
				ready, release := make(chan int, 2), make(chan struct{})
				var releaseOnce sync.Once
				unblock := func() { releaseOnce.Do(func() { close(release) }) }
				type outcome struct {
					index  int
					result *move.Result
					err    error
				}
				done := make(chan outcome, 2)
				var workers sync.WaitGroup
				defer func() { unblock(); cancel(); workers.Wait() }()
				for i := range plans {
					env := envs[i]
					env.Failpoint = func(stage string) error {
						if stage == "native-written" {
							ready <- i
							select {
							case <-release:
							case <-ctx.Done():
								return ctx.Err()
							}
						}
						return nil
					}
					workers.Go(func() {
						result, err := move.Apply(ctx, plans[i], inputs[i], env)
						done <- outcome{i, result, err}
					})
				}
				for range 2 {
					select {
					case <-ready:
					case result := <-done:
						t.Fatalf("operation %d ended before both native commits: %v", result.index, result.err)
					case <-ctx.Done():
						t.Fatal("concurrent native commits did not reach the barrier", ctx.Err())
					}
				}
				unblock()
				var results [2]*move.Result
				for range 2 {
					select {
					case result := <-done:
						if result.err != nil || result.result == nil {
							t.Fatal("concurrent native apply failed", result.index, result.err)
						}
						results[result.index] = result.result
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
				}
				workers.Wait()
				for i, destination := range destinations {
					written := findRouteSession(t, destination, inputs[i].Target.Module, inputs[i].Target.Install, plans[i].Placement.Key)
					if targets[i] == "claude" {
						appendTurn(t, written.Path, fmt.Sprintf("INDEPENDENT-DESTINATION-%d", i))
					} else {
						appendCodexTurn(t, written.Path, fmt.Sprintf("INDEPENDENT-DESTINATION-%d", i), "preserve this reply")
					}
					beforeRetry := movementBytes(t, written.Path)
					for range 2 {
						retried, err := move.Apply(ctx, plans[i], inputs[i], envs[i])
						if err != nil || retried.Journal != results[i].Journal {
							t.Fatal("exact retry changed its effect identity", err)
						}
					}
					if !bytes.Equal(beforeRetry, movementBytes(t, written.Path)) || len(listAgent(t, destination, inputs[i].Target.Module, inputs[i].Target.Install)) != 1 {
						t.Fatal("retry duplicated native output or rewrote later destination work")
					}
					j, err := journal.Load(envs[i].StateDir, results[i].Journal)
					if err != nil || j.PendingReceipts() {
						t.Fatal("retry did not finish durable receipt recovery", err)
					}
				}
				if !bytes.Equal(beforeSource, movementBytes(t, source.Path)) {
					t.Fatal("parallel transfers changed the original conversation")
				}
				graph := movementGraph(t, source)
				want := map[string]bool{plans[0].OperationID: forks[0], plans[1].OperationID: forks[1]}
				for _, hop := range graph.ActiveHops() {
					fork, exists := want[hop.ID]
					if !exists || hop.Fork != fork {
						t.Fatal("source receipt differs from issued operations", hop.ID)
					}
					delete(want, hop.ID)
					if hop.Fork && graph.ForBranch(hop.Line).Journey().Transfers != 0 {
						t.Fatal("fork creation inherited another branch's transfer count")
					}
				}
				if len(want) != 0 {
					t.Fatal("concurrent receipt lost an issued operation", want)
				}
				_, original, ok := graph.FindEndpoint(source.Key, a.m.Facts.Endpoint)
				if !ok {
					t.Fatal("original replica missing from merged receipt")
				}
				if _, chosen := graph.Departure(original); chosen {
					t.Fatal("concurrent original/fork movements silently chose a destination")
				}
				transfers := 0
				for _, fork := range forks {
					if !fork {
						transfers++
					}
				}
				if graph.Journey().Transfers != transfers {
					t.Fatal("source journey differs from confirmed original-branch operations")
				}
			})
		}
	}
}
