package e2e

import (
	"bytes"
	"context"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/move"
)

// A missing return destination must not silently become another surviving copy.
// Explicit new-replica intent preserves all existing native sessions, including
// when a single same-branch candidate would otherwise be automatically selected.
func TestMovementExplicitNewReplicaPreservesSurvivingCopy(t *testing.T) {
	for _, from := range []string{"claude", "codex"} {
		for _, to := range []string{"claude", "codex"} {
			t.Run(from+"-"+to, func(t *testing.T) {
				a, b, in := movementInput(t, from, to)
				env := move.Env{StateDir: t.TempDir()}
				p, _ := applyMovement(t, in, move.Options{TargetDir: b.repo, Notify: true}, env)
				left := findRouteSession(t, a, in.Source.Module, in.Source.Install, in.Session.Key)
				dst := findRouteSession(t, b, in.Target.Module, in.Target.Install, p.Placement.Key)
				if to == "claude" {
					appendTurn(t, dst.Path, "NEW-REPLICA-WORK")
				} else {
					appendCodexTurn(t, dst.Path, "NEW-REPLICA-WORK", "New work completed")
				}
				dst = findRouteSession(t, b, in.Target.Module, in.Target.Install, dst.Key)
				original := movementBytes(t, left.Path)
				back := move.Input{Source: in.Target, Session: dst, Lineage: movementGraph(t, dst), Target: in.Source,
					Copies: []move.Copy{{Summary: left, Lineage: movementGraph(t, left)}}}
				opt := move.Options{TargetDir: a.repo, NewReplica: true, Notify: true}
				created, _ := applyMovement(t, back, opt, env)
				if created.Placement.Key == left.Key || created.NoWork || created.Continue.AppendTo != nil || created.Options.Fork {
					t.Fatalf("new replica intent selected or forked an existing copy: %+v", created)
				}
				home := findRouteSession(t, a, in.Source.Module, in.Source.Install, created.Placement.Key)
				if !mentions(readAll(t, a, in.Source.Module, in.Source.Install, home), "NEW-REPLICA-WORK") {
					t.Fatal("new replica lost incoming work")
				}
				if !bytes.Equal(original, movementBytes(t, left.Path)) {
					t.Fatal("new replica modified the surviving native copy")
				}
				if movementGraph(t, home).Branch != movementGraph(t, left).Branch {
					t.Fatal("new replica changed the logical branch")
				}
				opt.TargetSession = left.Key.String()
				if _, err := move.Build(context.Background(), back, opt); err == nil {
					t.Fatal("accepted contradictory new and existing destination choices")
				}
				opt.TargetSession = ""
				if from == "claude" {
					appendTurn(t, left.Path, "INDEPENDENT-SURVIVOR-WORK")
				} else {
					appendCodexTurn(t, left.Path, "INDEPENDENT-SURVIVOR-WORK", "Keep this separate")
				}
				back.Copies[0].Summary = findRouteSession(t, a, in.Source.Module, in.Source.Install, left.Key)
				blocked, err := move.Build(context.Background(), back, opt)
				if err != nil || len(blocked.Blockers) == 0 {
					t.Fatalf("new replica bypassed independent-work conflict: %+v %v", blocked, err)
				}
			})
		}
	}
}
