package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Native readers/writers use only the temporary fixture root. Process controls
// and hook discovery are synthetic, so these tests cannot close a real client.
type returnTestModule struct {
	*claude.Module
	install        agent.Install
	live           map[agent.SessionID]bool
	stopped        []agent.SessionKey
	original       agent.SessionKey
	writes         []ir.WriteRequest
	onStop         func()
	failAfterWrite bool
	hookReady      bool
}

func (m *returnTestModule) Spec() agent.Spec {
	s := m.Module.Spec()
	s.Binaries = []agent.Binary{{Name: "claude"}} // no candidates; fixture PATH contains no agent
	return s
}
func (m *returnTestModule) Detect(context.Context, agent.Host) (agent.Install, error) {
	return m.install, nil
}
func (m *returnTestModule) MovementHooks(_ agent.Host, in agent.Install, _ string, _ ...string) agent.HookIntegration {
	identity := func(b []byte) ([]byte, error) { return b, nil }
	return agent.HookIntegration{File: &agent.HookFile{
		Path: filepath.Join(in.Root("home"), "settings.json"), Merge: identity, Remove: identity,
		Has: func([]byte) bool { return m.hookReady },
	}}
}
func (m *returnTestModule) Live(_ context.Context, _ agent.Host, _ agent.Install, ids []agent.SessionID) (map[agent.SessionID]agent.LiveInfo, error) {
	out := make(map[agent.SessionID]agent.LiveInfo, len(ids))
	for _, id := range ids {
		state := agent.Ended
		if m.live[id] {
			state = agent.Live
		}
		out[id] = agent.LiveInfo{State: state}
	}
	return out, nil
}
func (m *returnTestModule) Stop(_ context.Context, _ agent.Host, _ agent.Install, s agent.Summary, _ time.Duration) error {
	m.stopped = append(m.stopped, s.Key)
	if m.onStop != nil {
		m.onStop()
	}
	m.live[s.Key.Session] = false
	return nil
}
func (m *returnTestModule) Write(ctx context.Context, h agent.Host, in agent.Install, req ir.WriteRequest) (ir.WriteResult, error) {
	w, err := m.Module.Write(ctx, h, in, req)
	if req.SessionID == string(m.original.Session) && req.Mode == ir.WriteAppend {
		m.writes = append(m.writes, req)
		if err == nil && m.failAfterWrite {
			m.failAfterWrite = false
			return w, errors.New("interrupted after native append")
		}
	}
	return w, err
}

type protectedReturnFixture struct {
	a        *App
	m        *returnTestModule
	p        *move.Plan
	in       move.Input
	original agent.Summary
	h        agent.Host
	prefix   []byte
	flush    []byte
}

func newProtectedReturnFixture(t *testing.T) *protectedReturnFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("exact destination stopping is unavailable on Windows")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, cfgDir := filepath.Join(home, ".claude"), filepath.Join(home, "config")
	bin := filepath.Join(home, "bin")
	for _, dir := range []string{root, bin, cfgDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for k, v := range map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": root,
		"HOPSESH_CONFIG_DIR": cfgDir, "HOPSESH_STATE_DIR": filepath.Join(home, "state"), "HOPSESH_MACHINE": "here", "PATH": bin} {
		t.Setenv(k, v)
	}
	// Hook status needs an executable path, but never executes this stand-in.
	if err := os.WriteFile(filepath.Join(bin, "hopsesh"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	m := &returnTestModule{Module: claude.New(), hookReady: true, live: map[agent.SessionID]bool{},
		install: agent.Install{Agent: "claude", Present: true, Version: "2.1.284", Roots: map[string]string{"home": root}}}
	reg, err := registry.New(m)
	if err != nil {
		t.Fatal(err)
	}
	a := New(config.Defaults(), reg, filepath.Join(home, "state"), nil)
	t.Cleanup(func() { _ = a.Catalog.Close() })
	machine := &host.Machine{Local: true, Name: "here", Facts: host.Facts{Home: home, OS: runtime.GOOS,
		Env: map[string]string{"HOPSESH_CONFIG_DIR": cfgDir}, Binaries: map[string]agent.BinaryFact{}}}
	t.Cleanup(func() { machine.Close() })
	ctx := context.Background()
	seedJournal, err := journal.New(a.StateDir, journal.KindContinue, "temporary return fixture")
	if err != nil {
		t.Fatal(err)
	}
	h, err := machine.For(ctx, m.Spec(), m.install, seedJournal)
	if err != nil {
		t.Fatal(err)
	}
	w, err := m.Module.Write(ctx, h, m.install, ir.WriteRequest{OperationID: "seed-original", Mode: ir.WriteNew,
		SessionID: "11111111-1111-4111-8111-111111111111", Header: ir.Header{CWD: home, Title: "Original", Created: time.Now()},
		Items: []ir.Item{{Role: ir.RoleUser, Text: "original work"}}})
	if err != nil {
		t.Fatal(err)
	}
	original := agent.Summary{Key: agent.SessionKey{Agent: "claude", Session: agent.SessionID(w.SessionID)}, Path: w.Path, CWD: home, Title: "Original"}
	side := move.Side{Machine: machine, Module: m, Install: m.install}
	outIn := move.Input{Source: side, Target: side, Session: original}
	out, err := move.Build(ctx, outIn, move.Options{TargetDir: home, NewReplica: true, Notify: true, OperationID: "outbound"})
	if err != nil || len(out.Blockers) != 0 {
		t.Fatalf("outbound plan: %+v %v", out, err)
	}
	if _, err := move.Apply(ctx, out, outIn, move.Env{StateDir: a.StateDir}); err != nil {
		t.Fatal(err)
	}
	source := agent.Summary{Key: out.Placement.Key, CWD: home, Title: "Continuation"}
	ls, err := m.List(ctx, h, m.install)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range ls.Sessions {
		if s.Key == source.Key {
			source = s
		}
	}
	if source.Path == "" {
		t.Fatal("outbound continuation not found")
	}
	f := &protectedReturnFixture{a: a, m: m, original: original, h: h}
	f.in = move.Input{Source: side, Target: side, Session: source}
	f.appendSource(t, "reviewed return work")
	f.in.Lineage, err = lineage.Read(host.LocalFS(), source.Path)
	if err != nil {
		t.Fatal(err)
	}
	originalLineage, err := lineage.Read(host.LocalFS(), original.Path)
	if err != nil {
		t.Fatal(err)
	}
	m.original = original.Key
	m.live[original.Key.Session], m.live["unrelated"] = true, true
	f.in.Copies = []move.Copy{{Summary: original, Live: agent.LiveInfo{State: agent.Live}, Lineage: originalLineage}}
	f.p, err = move.Build(ctx, f.in, move.Options{TargetDir: home, Notify: true, TargetSession: original.Key.String(),
		OpenBlockedOriginal: original.Key.String(), OperationID: "protected-return"})
	if err != nil || len(f.p.Blockers) != 0 || f.p.Continue == nil || f.p.Continue.Relation != move.RelationAppend {
		t.Fatalf("return plan: %+v %v", f.p, err)
	}
	f.prefix = readReturnFile(t, original.Path)
	f.flush = []byte("{\"type\":\"last-prompt\",\"lastPrompt\":\"shutdown metadata\"}\n")
	m.onStop = func() {
		file, err := os.OpenFile(original.Path, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = file.Write(f.flush)
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("shutdown flush: %v %v", err, closeErr)
		}
	}
	return f
}

func readReturnFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (f *protectedReturnFixture) appendSource(t *testing.T, text string) {
	t.Helper()
	seg, err := f.m.Read(context.Background(), f.h, f.m.install, f.in.Session, ir.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.m.Module.Write(context.Background(), f.h, f.m.install, ir.WriteRequest{Mode: ir.WriteAppend,
		SessionID: string(f.in.Session.Key.Session), Expect: seg.Cursor, Header: ir.Header{CWD: f.in.Session.CWD},
		Items: []ir.Item{{Role: ir.RoleUser, Text: text}}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestProtectedReturnRejectsSourceChangedBeforeClose(t *testing.T) {
	f := newProtectedReturnFixture(t)
	f.appendSource(t, "unreviewed source work")
	_, err := f.a.Apply(context.Background(), f.p, f.in, nil)
	if err == nil || !strings.Contains(err.Error(), "source changed") {
		t.Fatalf("changed source accepted: %v", err)
	}
	if len(f.m.stopped) != 0 || len(f.m.writes) != 0 || !bytes.Equal(readReturnFile(t, f.original.Path), f.prefix) {
		t.Fatal("stale review closed or wrote the original")
	}
}

func TestProtectedReturnRejectsSourceChangedDuringClose(t *testing.T) {
	f := newProtectedReturnFixture(t)
	flush := f.m.onStop
	f.m.onStop = func() { flush(); f.appendSource(t, "source advanced during shutdown") }
	reviewed := f.p.SourceCursor()
	_, err := f.a.Apply(context.Background(), f.p, f.in, nil)
	if err == nil || !strings.Contains(err.Error(), "source changed") {
		t.Fatalf("rebuild accepted changed source: %v", err)
	}
	if len(f.m.stopped) != 1 || f.m.stopped[0] != f.original.Key || len(f.m.writes) != 0 || f.p.SourceCursor() != reviewed {
		t.Fatal("unreviewed source was written or replaced the reviewed snapshot")
	}
	if !bytes.Equal(readReturnFile(t, f.original.Path), append(bytes.Clone(f.prefix), f.flush...)) {
		t.Fatal("rejected return wrote more than shutdown metadata")
	}
}

func TestProtectedReturnRetainsRebuiltPlanAndRecoversInterruptedAppend(t *testing.T) {
	for _, interrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "interrupted"}[interrupt], func(t *testing.T) {
			f := newProtectedReturnFixture(t)
			reviewed := f.p.SourceCursor()
			sourceBefore := readReturnFile(t, f.in.Session.Path)
			f.m.failAfterWrite = interrupt
			res, err := f.a.Apply(context.Background(), f.p, f.in, nil)
			if interrupt {
				if err == nil || !strings.Contains(err.Error(), "interrupted after native append") {
					t.Fatalf("did not exercise native append interruption: %v", err)
				}
			} else if err != nil || res == nil {
				t.Fatalf("return failed: %+v %v", res, err)
			}
			if f.p.Options.OpenBlockedOriginal != "" || f.p.SourceCursor() != reviewed || f.p.Placement.Key != f.original.Key {
				t.Fatal("caller retained the pre-close plan")
			}
			if len(f.m.writes) != 1 || f.m.writes[0].Expect.Offset != int64(len(f.prefix)+len(f.flush)) {
				t.Fatal("append did not use the post-shutdown destination cursor")
			}
			written := readReturnFile(t, f.original.Path)
			if !bytes.HasPrefix(written, append(bytes.Clone(f.prefix), f.flush...)) || !bytes.Contains(written, []byte("reviewed return work")) {
				t.Fatal("return lost the prefix, shutdown metadata or reviewed work")
			}
			// Recovery must run even when the protection hook is no longer ready,
			// and an already completed operation must also be idempotent.
			f.m.hookReady = false
			res, err = f.a.Apply(context.Background(), f.p, f.in, nil)
			if err != nil || res == nil {
				t.Fatalf("retry did not reach operation recovery: %+v %v", res, err)
			}
			if len(f.m.stopped) != 1 || f.m.stopped[0] != f.original.Key || !f.m.live["unrelated"] || len(f.m.writes) != 1 {
				t.Fatal("retry stopped another client or duplicated the append")
			}
			if !bytes.Equal(readReturnFile(t, f.original.Path), written) || !bytes.Equal(readReturnFile(t, f.in.Session.Path), sourceBefore) {
				t.Fatal("retry modified native history")
			}
			g, err := lineage.Read(host.LocalFS(), f.original.Path)
			if err != nil || g == nil {
				t.Fatalf("missing recovered receipt: %v", err)
			}
			count := 0
			for _, hop := range g.ActiveHops() {
				if hop.ID == f.p.OperationID {
					count++
					if _, departed := g.Departure(hop.To); departed {
						t.Fatal("recovered original is still marked as departed")
					}
				}
			}
			if count != 1 {
				t.Fatalf("recovered return recorded %d times", count)
			}
		})
	}
}
