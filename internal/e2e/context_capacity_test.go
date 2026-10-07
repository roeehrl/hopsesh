package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// These cases run in the existing macOS/Linux/Windows context scenario matrix.
// Native readers, writers, archives, receipts and undo all participate.
func TestContextReadableTransferBothDirections(t *testing.T) {
	for _, from := range []string{"claude", "codex"} {
		t.Run(from, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			a, b := newLocation(t, "A", root), newLocation(t, "B", root)
			var src, dst agent.Module = claude.New(), codex.New()
			si, di := a.in, codexInstall(b)
			if from == "codex" {
				src, dst, si, di = codex.New(), claude.New(), codexInstall(a), b.in
			}
			seedJournal, err := journal.New(t.TempDir(), journal.KindContinue, "context source fixture")
			if err != nil {
				t.Fatal(err)
			}
			h, err := a.m.For(ctx, src.Spec(), si, seedJournal)
			if err != nil {
				t.Fatal(err)
			}
			w, err := src.(agent.Writer).Write(ctx, h, si, ir.WriteRequest{Mode: ir.WriteNew, Header: ir.Header{CWD: a.repo}, Items: []ir.Item{{Role: ir.RoleUser, Node: "initial", Text: "Investigate the worker payout presentation."}, {Role: ir.RoleAgent, Node: "reply", Text: "I will check the payout records."}}})
			if err != nil {
				t.Fatal(err)
			}
			original, _ := os.ReadFile(w.Path)
			parent := ""
			for _, line := range strings.Split(string(original), "\n") {
				var r map[string]any
				_ = json.Unmarshal([]byte(line), &r)
				if id, ok := r["uuid"].(string); ok {
					parent = id
				}
			}
			var extra strings.Builder
			write := func(v any) { raw, _ := json.Marshal(v); extra.Write(raw); extra.WriteByte('\n') }
			summary := "## Worker task\nKeep the mounted proxy enabled.\n\n### Outstanding work\nVerify settled payouts; never count forfeited alpha as paid."
			if from == "codex" {
				write(map[string]any{"type": "compacted", "payload": map[string]any{"message": summary}})
			}
			message := func(i int, role, text string, compact bool) {
				if from == "claude" {
					id := fmt.Sprintf("context-%d", i)
					write(map[string]any{"type": role, "uuid": id, "parentUuid": parent, "sessionId": w.SessionID, "cwd": a.repo, "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "isCompactSummary": compact, "message": map[string]any{"role": role, "content": text}})
					parent = id
				} else {
					kind := "input_text"
					if role == "assistant" {
						kind = "output_text"
					}
					write(map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": role, "content": []any{map[string]any{"type": kind, "text": text}}}})
				}
			}
			if from == "claude" {
				message(0, "user", summary, true)
			}
			for i := 1; i <= 100; i++ {
				role := "user"
				if i%2 == 0 {
					role = "assistant"
				}
				message(i, role, strings.Repeat("Historical worker evidence.\n", 150), false)
			}
			const current = "Current request: fix the final payout label and run the payout tests."
			message(101, "user", current, false)
			if from == "claude" {
				write(map[string]any{"type": "last-prompt", "leafUuid": parent, "sessionId": w.SessionID, "lastPrompt": current})
			}
			if err = os.WriteFile(w.Path, append(original, []byte(extra.String())...), 0600); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(w.Path)
			s := findRouteSession(t, a, src, si, agent.SessionKey{Agent: src.Spec().ID, Session: agent.SessionID(w.SessionID)})
			in := move.Input{Source: move.Side{Machine: a.m, Module: src, Install: si}, Session: s, Target: move.Side{Machine: b.m, Module: dst, Install: di}}
			p, err := move.Build(ctx, in, move.Options{TargetDir: b.repo})
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Blockers) > 0 || p.Continue.Report.Summarised == 0 {
				t.Fatalf("bounded plan: blockers=%v report=%+v", p.Blockers, p.Continue.Report)
			}
			env := move.Env{StateDir: t.TempDir()}
			result, err := move.Apply(ctx, p, in, env)
			if err != nil {
				t.Fatal(err)
			}
			copy := findRouteSession(t, b, dst, di, p.Placement.Key)
			seg := readAll(t, b, dst, di, copy)
			if !strings.Contains(seg.Nodes[0].Text, "Transfer context") || !strings.Contains(seg.Nodes[0].Text, "never count forfeited alpha as paid") {
				t.Fatal("first message lost the substantive earlier summary")
			}
			requests := 0
			for _, n := range seg.Nodes {
				if n.Actor == ir.User && strings.Contains(n.Text, current) {
					requests++
					if n.Text != current {
						t.Fatal("context was merged into the current user request")
					}
				}
			}
			if requests != 1 || copy.LastPrompt != current || p.Continue.Report.Used > p.Continue.Report.Budget {
				t.Fatal("request identity, last prompt or capacity changed")
			}
			graph, err := lineage.Read(host.LocalFS(), copy.Path)
			if err != nil || graph == nil || graph.Validate() != nil {
				t.Fatalf("invalid conversion lineage: %v", err)
			}
			archive, err := os.ReadFile(p.Continue.Report.Archive)
			if err != nil || !strings.Contains(string(archive), "never count forfeited alpha") {
				t.Fatal("portable summary archive missing")
			}
			after, _ := os.ReadFile(w.Path)
			if string(before) != string(after) {
				t.Fatal("conversion changed the source transcript")
			}
			j, err := journal.Load(env.StateDir, result.Journal)
			if err != nil {
				t.Fatal(err)
			}
			if err = j.Undo(ctx, journal.Files(func(string) (host.FS, error) { return host.LocalFS(), nil }), false); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(copy.Path); !os.IsNotExist(err) {
				t.Fatal("undo retained the converted transcript")
			}
		})
	}
}

func TestContextOversizedImportAndPortableRecovery(t *testing.T) {
	ctx := context.Background()
	l := newLocation(t, "here", t.TempDir())
	seed(t, l)
	cl, cx := claude.New(), codex.New()
	ci := codexInstall(l)
	source := list(t, l)[sid]
	appendTurn(t, source.Path, strings.Repeat("synthetic context ", 100000))
	before, _ := os.ReadFile(source.Path)
	in := move.Input{Source: move.Side{Machine: l.m, Module: cl, Install: l.in}, Session: source, Target: move.Side{Machine: l.m, Module: cx, Install: ci}}
	p, err := move.Build(ctx, in, move.Options{Via: move.ViaImport})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(p.Blockers, " "), "vendor import is too large") {
		t.Fatalf("import wasn't blocked: %+v", p.Blockers)
	}
	env := move.Env{StateDir: t.TempDir()}
	if _, err = move.Apply(ctx, p, in, env); err == nil {
		t.Fatal("blocked import applied")
	}
	p, err = move.Build(ctx, in, move.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blockers) > 0 || p.Continue.Report.Used > p.Continue.Report.Budget {
		t.Fatalf("portable plan: %+v", p)
	}
	res, err := move.Apply(ctx, p, in, env)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := os.ReadFile(p.Continue.Report.Archive)
	if err != nil || !strings.Contains(string(archive), "synthetic context") {
		t.Fatalf("archive: %v", err)
	}
	after, _ := os.ReadFile(source.Path)
	if string(before) != string(after) {
		t.Fatal("source changed")
	}
	if len(listAgent(t, l, cx, ci)) != 1 {
		t.Fatal("prepared copy disappeared")
	}
	j, err := journal.Load(env.StateDir, res.Journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(j.Entries) == 0 {
		t.Fatal("archive/session were not journaled")
	}
	if err = j.Undo(ctx, journal.Files(func(string) (host.FS, error) { return host.LocalFS(), nil }), false); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(p.Continue.Report.Archive); !os.IsNotExist(err) {
		t.Fatalf("undo retained archive: %v", err)
	}
	if len(listAgent(t, l, cx, ci)) != 0 {
		t.Fatal("undo retained generated session")
	}

}

func TestContextWriterRejectsFullAppend(t *testing.T) {
	for _, name := range []string{"claude", "codex"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			l := newLocation(t, "here", t.TempDir())
			var m agent.Module = claude.New()
			in := l.in
			if name == "codex" {
				m = codex.New()
				in = codexInstall(l)
			}
			j, _ := journal.New(t.TempDir(), journal.KindContinue, "capacity")
			h, _ := l.m.For(ctx, m.Spec(), in, j)
			writer := m.(agent.Writer)
			var w ir.WriteResult
			var err error
			for i := 0; i < 6; i++ {
				req := ir.WriteRequest{Mode: ir.WriteNew, Header: ir.Header{CWD: l.repo}, Items: []ir.Item{{Role: ir.RoleUser, Text: strings.Repeat("x", 16000)}, {Role: ir.RoleAgent, Text: "ack"}}}
				if i > 0 {
					req.Mode = ir.WriteAppend
					req.SessionID = w.SessionID
					req.Expect = w.Cursor
				}
				var before []byte
				if w.Path != "" {
					before, _ = os.ReadFile(w.Path)
				}
				next, e := writer.Write(ctx, h, in, req)
				err = e
				if e != nil {
					after, _ := os.ReadFile(w.Path)
					if string(after) != string(before) {
						t.Fatal("rejected append changed file")
					}
					break
				}
				w = next
			}
			if err == nil || !strings.Contains(err.Error(), "context capacity") {
				t.Fatalf("unbounded append: %v", err)
			}
		})
	}
}

func TestContextBoundedRecoveryAndChangedConfiguration(t *testing.T) {
	ctx := context.Background()
	l := newLocation(t, "here", t.TempDir())
	seed(t, l)
	cl := claude.New()
	source := list(t, l)[sid]
	before, _ := os.ReadFile(source.Path)
	in := move.Input{Source: move.Side{Machine: l.m, Module: cl, Install: l.in}, Session: source, Target: move.Side{Machine: l.m, Module: cl, Install: l.in}}
	p, err := move.Build(ctx, in, move.Options{Bounded: true, Via: move.ViaImport, Native: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = move.Apply(ctx, p, in, move.Env{StateDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if p.Placement.Key == source.Key {
		t.Fatal("recovery overwrote original")
	}
	copies := listAgent(t, l, cl, l.in)
	for _, copy := range copies {
		if copy.Key == p.Placement.Key {
			graph, e := lineage.Read(host.LocalFS(), copy.Path)
			if e != nil {
				t.Fatal(e)
			}
			if graph.Journey().Fork || graph.Journey().Transfers != 0 {
				t.Fatalf("rollover counted as fork/travel: %+v", graph.Journey())
			}
		}
	}
	after, _ := os.ReadFile(source.Path)
	if string(before) != string(after) {
		t.Fatal("recovery changed source")
	}
	cx := codex.New()
	ci := codexInstall(l)
	in.Target = move.Side{Machine: l.m, Module: cx, Install: ci}
	p, err = move.Build(ctx, in, move.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(ci.Root("home"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(ci.Root("home"), "config.toml"), []byte("model_context_window = 2000\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = move.Apply(ctx, p, in, move.Env{StateDir: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "capacity changed") {
		t.Fatalf("stale config accepted: %v", err)
	}
}

func TestContextEditedCloudBrief(t *testing.T) {
	w := newHandoffWorld(t)
	a := w.app()
	opt := a.HandoffDefaults("claude-cloud")
	opt.Brief = strings.Repeat("synthetic briefing ", 10000)
	inv, p := w.planHandoff(a, opt)
	defer inv.Close()
	if !strings.Contains(strings.Join(p.Blockers, " "), "edited briefing exceeds") {
		t.Fatal("oversized edit accepted")
	}
}

func TestContextBoundedForkRemainsASeparateBranch(t *testing.T) {
	ctx := context.Background()
	l := newLocation(t, "here", t.TempDir())
	seed(t, l)
	cl := claude.New()
	source := list(t, l)[sid]
	side := move.Side{Machine: l.m, Module: cl, Install: l.in}
	in := move.Input{Source: side, Session: source, Target: side}
	p, err := move.Build(ctx, in, move.Options{Bounded: true, Fork: true})
	if err != nil {
		t.Fatal(err)
	}
	if p.Continue.Rollover != nil || !strings.Contains(strings.Join(p.Warnings, " "), "separate fork") {
		t.Fatal("explicit fork treated as rollover")
	}
	if _, err = move.Apply(ctx, p, in, move.Env{StateDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	s := findRouteSession(t, l, cl, l.in, p.Placement.Key)
	graph, err := lineage.Read(host.LocalFS(), s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Journey().Fork || graph.Journey().Transfers != 0 {
		t.Fatalf("incorrect fork journey: %+v", graph.Journey())
	}
	if _, err = os.Stat(source.Path); err != nil {
		t.Fatal("fork removed the original")
	}
}

func TestContextRetainedOriginalWithNewWorkIsNotHidden(t *testing.T) {
	ctx := context.Background()
	l := newLocation(t, "here", t.TempDir())
	seed(t, l)
	cl := claude.New()
	source := list(t, l)[sid]
	side := move.Side{Machine: l.m, Module: cl, Install: l.in}
	in := move.Input{Source: side, Session: source, Target: side}
	p, err := move.Build(ctx, in, move.Options{Bounded: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = move.Apply(ctx, p, in, move.Env{StateDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	replacement := findRouteSession(t, l, cl, l.in, p.Placement.Key)
	graph, err := lineage.Read(host.LocalFS(), replacement.Path)
	if err != nil {
		t.Fatal(err)
	}
	appendTurn(t, source.Path, "Independent work in the retained original")
	originalGraph, err := lineage.Read(host.LocalFS(), source.Path)
	if err != nil {
		t.Fatal(err)
	}
	in = move.Input{Source: side, Session: replacement, Lineage: graph, Target: side, Copies: []move.Copy{{Summary: source, Lineage: originalGraph}}}
	p, err = move.Build(ctx, in, move.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blockers) == 0 {
		t.Fatal("retained original's independent work was hidden")
	}
}

func TestContextRolloverInterruptedRetry(t *testing.T) {
	ctx := context.Background()
	l := newLocation(t, "here", t.TempDir())
	seed(t, l)
	cl := claude.New()
	source := list(t, l)[sid]
	side := move.Side{Machine: l.m, Module: cl, Install: l.in}
	in := move.Input{Source: side, Session: source, Target: side}
	opt := move.Options{Bounded: true, OperationID: "bounded-retry"}
	p, err := move.Build(ctx, in, opt)
	if err != nil {
		t.Fatal(err)
	}
	env := move.Env{StateDir: t.TempDir(), Failpoint: func(string) error { return fmt.Errorf("interrupted") }}
	if _, err = move.Apply(ctx, p, in, env); err == nil {
		t.Fatal("expected interrupted write")
	}
	key := p.Placement.Key
	p, err = move.Build(ctx, in, opt)
	if err != nil {
		t.Fatal(err)
	}
	env.Failpoint = nil
	if _, err = move.Apply(ctx, p, in, env); err != nil {
		t.Fatal(err)
	}
	s := findRouteSession(t, l, cl, l.in, key)
	graph, err := lineage.Read(host.LocalFS(), s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Hops) != 1 || graph.Hops[0].Rollover == nil || graph.Hops[0].Rollover.Cursor.Offset == 0 {
		t.Fatalf("rollover checkpoint lost during retry: %+v", graph.Hops)
	}
	if len(listAgent(t, l, cl, l.in)) != 3 {
		t.Fatal("retry duplicated native replica")
	}
}

// Simulate the vendor writing outside Hopsesh's journal, as app-server does. The
// core must adopt that output before any later validation or archive write fails.
type contextImporter struct {
	*codex.Module
	run func(agent.Install, string) (agent.SessionID, error)
}

func (m *contextImporter) Import(_ context.Context, _ agent.Host, in agent.Install, _ agent.ID, _, cwd, _ string) (agent.SessionID, error) {
	return m.run(in, cwd)
}

func TestContextImportOutputAndFailureRecovery(t *testing.T) {
	for _, mode := range []string{"small", "oversized-output", "source-grew", "cancelled", "archive-failed", "import-archive-failed", "vendor-rejected-output", "rejected-copy-used"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			l := newLocation(t, "here", t.TempDir())
			seed(t, l)
			source := list(t, l)[sid]
			ci := codexInstall(l)
			const id agent.SessionID = "01a0fe1c-0000-7000-8000-000000000009"
			var importedPath string
			called := false
			calls := 0
			cx := &contextImporter{Module: codex.New(), run: func(in agent.Install, cwd string) (agent.SessionID, error) {
				called = true
				calls++
				if mode == "cancelled" {
					return "", context.Canceled
				}
				importedPath = filepath.Join(in.Root("home"), "sessions", "2026", "01", "01", "rollout-2026-01-01T00-00-00-"+string(id)+".jsonl")
				if err := os.MkdirAll(filepath.Dir(importedPath), 0700); err != nil {
					return "", err
				}
				meta, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]any{"id": id, "cwd": cwd, "timestamp": "2026-01-01T00:00:00Z", "originator": "codex_cli_rs", "cli_version": "0.153.2"}})
				if err := os.WriteFile(importedPath, append(meta, '\n'), 0600); err != nil {
					return "", err
				}
				text := "Imported fictional work"
				if mode == "oversized-output" || mode == "rejected-copy-used" {
					text = strings.Repeat(text, 5000)
				}
				appendCodexTurn(t, importedPath, string(id), text)
				if mode == "source-grew" {
					appendTurn(t, source.Path, "Work added during import")
				}
				if mode == "import-archive-failed" {
					// A directory cannot be replaced as the archive file for this ID.
					if err := os.MkdirAll(filepath.Join(in.Root("home"), "hopsesh", "archives", string(id)+".jsonl"), 0700); err != nil {
						return "", err
					}
				}
				if mode == "vendor-rejected-output" {
					return id, fmt.Errorf("source changed during vendor import")
				}
				return id, nil
			}}
			in := move.Input{Source: move.Side{Machine: l.m, Module: claude.New(), Install: l.in}, Session: source, Target: move.Side{Machine: l.m, Module: cx, Install: ci}}
			p, err := move.Build(ctx, in, move.Options{Via: move.ViaImport})
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Blockers) > 0 {
				t.Fatalf("small import blocked: %v", p.Blockers)
			}
			if mode == "archive-failed" {
				if err := os.MkdirAll(ci.Root("home"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(ci.Root("home"), "hopsesh"), []byte("cannot create child"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			env := move.Env{StateDir: t.TempDir()}
			res, err := move.Apply(ctx, p, in, env)
			if mode == "small" {
				if err != nil || res.Command == "" {
					t.Fatalf("safe import failed: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("unsafe import succeeded")
				}
				if res != nil && (res.Command != "" || len(res.Run.Argv) > 0) {
					t.Fatal("failed import returned a launch command")
				}
			}
			if mode == "archive-failed" && called {
				t.Fatal("vendor invoked without preserving archive")
			}
			if mode != "small" {
				before := calls
				if _, retryErr := move.Apply(ctx, p, in, env); retryErr == nil || calls != before {
					t.Fatal("failed import retry duplicated the vendor operation instead of requiring undo")
				}
			}
			if importedPath != "" {
				j, e := journal.Load(env.StateDir, res.Journal)
				if e != nil {
					t.Fatal(e)
				}
				found := false
				for _, key := range j.Keys {
					if key.Session == id {
						found = true
					}
				}
				if !found {
					t.Fatal("imported session absent from undo's live-session keys")
				}
				if mode == "rejected-copy-used" {
					appendCodexTurn(t, importedPath, string(id), "Independent work after failed import")
					if e = j.Undo(ctx, journal.Files(func(string) (host.FS, error) { return host.LocalFS(), nil }), false); e == nil {
						t.Fatal("undo deleted work added to rejected imported copy")
					}
					return
				}
				if e = j.Undo(ctx, journal.Files(func(string) (host.FS, error) { return host.LocalFS(), nil }), false); e != nil {
					t.Fatal(e)
				}
				if _, e := os.Stat(importedPath); !os.IsNotExist(e) {
					t.Fatalf("imported output survived undo: %v", e)
				}
			}
		})
	}
}
