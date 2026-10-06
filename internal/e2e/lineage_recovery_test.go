package e2e

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestLineageInterruptedWriteAndIdempotentRetry(t *testing.T) {
	for _, target := range []string{"claude", "codex"} {
		t.Run(target, func(t *testing.T) {
			root := t.TempDir()
			a, b := newLocation(t, "A", root), newLocation(t, "B", root)
			seed(t, a)
			mod := agent.Module(claude.New())
			install := b.in
			if target == "codex" {
				mod = codex.New()
				install = codexInstall(b)
			}
			os.MkdirAll(install.Root("home"), 0o700)
			ctx := context.Background()
			opt := move.Options{TargetDir: b.repo, OperationID: "stable-transfer", Mark: true}
			in := move.Input{Source: move.Side{Machine: a.m, Module: claude.New(), Install: a.in}, Session: list(t, a)[sid], Target: move.Side{Machine: b.m, Module: mod, Install: install}}
			p, err := move.Build(ctx, in, opt)
			if err != nil {
				t.Fatal(err)
			}
			env := move.Env{StateDir: t.TempDir(), Failpoint: func(stage string) error {
				if stage == "native-written" {
					return errors.New("simulated process exit")
				}
				return nil
			}}
			if _, err = move.Apply(ctx, p, in, env); err == nil {
				t.Fatal("fault did not interrupt native commit")
			}
			sessions := listAgent(t, b, mod, install)
			var written agent.Summary
			for _, s := range sessions {
				if s.Key == p.Placement.Key {
					written = s
				}
			}
			if written.Path == "" {
				t.Fatal("fixture did not commit native file")
			}
			original, _ := os.ReadFile(written.Path)
			// Rebuild from disk: no in-memory plan or writer result survives.
			p, err = move.Build(ctx, in, opt)
			if err != nil {
				t.Fatal(err)
			}
			env.Failpoint = nil
			res, err := move.Apply(ctx, p, in, env)
			if err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(written.Path)
			if string(original) != string(after) {
				t.Fatal("receipt recovery wrote native conversation twice")
			}
			graph, err := lineage.Read(host.LocalFS(), written.Path)
			if err != nil || graph == nil {
				t.Fatalf("recovered receipt %v", err)
			}
			if graph.Journey().Transfers != 1 {
				t.Fatalf("recovery counted twice: %+v", graph.Journey())
			}
			again, err := move.Apply(ctx, p, in, env)
			if err != nil || again.Journal != res.Journal {
				t.Fatalf("duplicate request %v %+v", err, again)
			}
			after, _ = os.ReadFile(written.Path)
			if string(original) != string(after) {
				t.Fatal("completed retry wrote conversation twice")
			}
		})
	}
}
func TestLineageRecoveryRefusesLaterDestinationWork(t *testing.T) {
	root := t.TempDir()
	a, b := newLocation(t, "A", root), newLocation(t, "B", root)
	seed(t, a)
	cx := codex.New()
	ci := codexInstall(b)
	os.MkdirAll(ci.Root("home"), 0o700)
	ctx := context.Background()
	in := move.Input{Source: move.Side{Machine: a.m, Module: claude.New(), Install: a.in}, Session: list(t, a)[sid], Target: move.Side{Machine: b.m, Module: cx, Install: ci}}
	p, _ := move.Build(ctx, in, move.Options{TargetDir: b.repo, OperationID: "later-work"})
	env := move.Env{StateDir: t.TempDir(), Failpoint: func(string) error { return errors.New("interrupted") }}
	if _, err := move.Apply(ctx, p, in, env); err == nil {
		t.Fatal("fault not triggered")
	}
	session := listAgent(t, b, cx, ci)[0]
	appendCodexTurn(t, session.Path, "work after crash", "must stay")
	before, _ := os.ReadFile(session.Path)
	env.Failpoint = nil
	if _, err := move.Apply(ctx, p, in, env); err == nil {
		t.Fatal("later work must block automatic native recovery")
	}
	after, _ := os.ReadFile(session.Path)
	if string(before) != string(after) {
		t.Fatal("recovery lost later work")
	}
}

func TestLineageInterruptedDestinationReceipt(t *testing.T) {
	root := t.TempDir()
	a, b := newLocation(t, "A", root), newLocation(t, "B", root)
	seed(t, a)
	cx, ci := codex.New(), codexInstall(b)
	os.MkdirAll(ci.Root("home"), 0700)
	ctx := context.Background()
	in := move.Input{Source: move.Side{Machine: a.m, Module: claude.New(), Install: a.in}, Session: list(t, a)[sid], Target: move.Side{Machine: b.m, Module: cx, Install: ci}}
	opt := move.Options{TargetDir: b.repo, OperationID: "receipt-crash"}
	p, err := move.Build(ctx, in, opt)
	if err != nil {
		t.Fatal(err)
	}
	blocked := ""
	env := move.Env{StateDir: t.TempDir(), Failpoint: func(stage string) error {
		if stage == "native-written" {
			ss := listAgent(t, b, cx, ci)
			if len(ss) != 1 {
				t.Fatal("expected committed native destination")
			}
			blocked = lineage.PathFor(ss[0].Path)
			return os.Mkdir(blocked, 0700)
		}
		return nil
	}}
	if _, err = move.Apply(ctx, p, in, env); err == nil {
		t.Fatal("receipt write must fail")
	}
	dst := listAgent(t, b, cx, ci)[0]
	before, _ := os.ReadFile(dst.Path)
	if err = os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	env.Failpoint = nil
	rebuilt, err := move.Build(ctx, in, opt)
	if err != nil {
		t.Fatal(err)
	}
	res, err := move.Apply(ctx, rebuilt, in, env)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(dst.Path)
	if string(before) != string(after) {
		t.Fatal("receipt retry rewrote native work")
	}
	graph, err := lineage.Read(host.LocalFS(), dst.Path)
	if err != nil {
		t.Fatal(err)
	}
	if graph.Journey().Transfers != 1 {
		t.Fatalf("retry counted more than one transfer: %+v", graph.Journey())
	}
	again, err := move.Apply(ctx, rebuilt, in, env)
	if err != nil || again.Journal != res.Journal {
		t.Fatalf("retry not idempotent: %+v %v", again, err)
	}
}

func TestLineageReverseAgentCrashRecovery(t *testing.T) {
	root := t.TempDir()
	a, b, c := newLocation(t, "A", root), newLocation(t, "B", root), newLocation(t, "C", root)
	seed(t, a)
	cx, ci := codex.New(), codexInstall(b)
	os.MkdirAll(ci.Root("home"), 0700)
	ctx := context.Background()
	env := move.Env{StateDir: t.TempDir()}
	in := move.Input{Source: move.Side{Machine: a.m, Module: claude.New(), Install: a.in}, Session: list(t, a)[sid], Target: move.Side{Machine: b.m, Module: cx, Install: ci}}
	p, err := move.Build(ctx, in, move.Options{TargetDir: b.repo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = move.Apply(ctx, p, in, env); err != nil {
		t.Fatal(err)
	}
	ss := listAgent(t, b, cx, ci)[0]
	appendCodexTurn(t, ss.Path, "reverse-recovery-sentinel", "must appear once")
	graph, err := lineage.Read(host.LocalFS(), ss.Path)
	if err != nil {
		t.Fatal(err)
	}
	in = move.Input{Source: move.Side{Machine: b.m, Module: cx, Install: ci}, Session: ss, Lineage: graph, Target: move.Side{Machine: c.m, Module: claude.New(), Install: c.in}}
	opt := move.Options{TargetDir: c.repo, OperationID: "codex-to-claude-crash"}
	p, err = move.Build(ctx, in, opt)
	if err != nil {
		t.Fatal(err)
	}
	env.Failpoint = func(stage string) error {
		if stage == "native-written" {
			return errors.New("crash")
		}
		return nil
	}
	if _, err = move.Apply(ctx, p, in, env); err == nil {
		t.Fatal("expected simulated crash")
	}
	dst := listAgent(t, c, claude.New(), c.in)[0]
	before, _ := os.ReadFile(dst.Path)
	env.Failpoint = nil
	p, err = move.Build(ctx, in, opt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = move.Apply(ctx, p, in, env); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(dst.Path)
	if string(before) != string(after) {
		t.Fatal("recovery appended twice")
	}
	g, err := lineage.Read(host.LocalFS(), dst.Path)
	if err != nil {
		t.Fatal(err)
	}
	if g.Journey().Transfers != 2 {
		t.Fatalf("lost route during recovery %+v", g.Journey())
	}
}
