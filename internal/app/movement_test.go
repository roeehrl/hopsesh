package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func movementFixture(t *testing.T) (*App, *lineage.Manifest, lineage.ReplicaID, lineage.ReplicaID, ir.Segment, ir.Segment) {
	t.Helper()
	t.Setenv("HOPSESH_CONFIG_DIR", t.TempDir())
	a := New(config.Defaults(), all.Registry(), t.TempDir(), nil)
	t.Cleanup(func() { _ = a.Catalog.Close() })
	g := lineage.New("movement-test")
	from := g.Upsert(lineage.Replica{Location: LocalName(), Key: agent.SessionKey{Agent: "claude", Session: "alice-session"}})
	to := g.Upsert(lineage.Replica{Location: "bob-laptop", Key: agent.SessionKey{Agent: "codex", Session: "bob-session"}})
	source := ir.Segment{Nodes: []ir.Node{{Kind: ir.KindMessage, Actor: ir.User, Text: "Fix the tests"}}, Cursor: ir.Cursor{Head: "source-one", Offset: 20}}
	st, err := g.Observe(from, &source)
	if err != nil {
		t.Fatal(err)
	}
	target := source
	target.Nodes = append([]ir.Node(nil), source.Nodes...)
	target.Cursor = ir.Cursor{Head: "target-one", Offset: 25}
	dst := g.Deliver(to, target.Cursor, st.Projection, st.Heads, nil)
	if err = g.AppendHop(lineage.Hop{Kind: lineage.HopContinue, ID: "move-one", From: from, To: to, Source: st.ID, Target: dst.ID, Notify: true}); err != nil {
		t.Fatal(err)
	}
	return a, g, from, to, source, target
}
func TestMovementEvidenceDoesNotTreatPreparationAsContinued(t *testing.T) {
	a, g, from, to, source, target := movementFixture(t)
	baseline, _ := g.LatestState(to)
	n := a.movementNotice(g, from, map[lineage.ReplicaID]movementObservation{to: {state: baseline, valid: true}})
	if n == nil || n.Status != "prepared" {
		t.Fatalf("prepared: %+v", n)
	}
	// Metadata offsets and a user's submitted message are not an agent response.
	target.Nodes = append(target.Nodes, ir.Node{Kind: ir.KindMessage, Actor: ir.User, Text: "Continue"})
	target.Cursor = ir.Cursor{Head: "user-input", Offset: 40}
	userState, err := g.Observe(to, &target)
	if err != nil {
		t.Fatal(err)
	}
	if authoredAfter(g, to, baseline, userState, map[string]bool{}) {
		t.Fatal("user input claimed model continuation")
	}
	target.Nodes = append(target.Nodes, ir.Node{Kind: ir.KindMessage, Actor: ir.Agent, Text: "I fixed the tests"})
	target.Cursor = ir.Cursor{Head: "agent-response", Offset: 60}
	continued, err := g.Observe(to, &target)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	observations := map[lineage.ReplicaID]movementObservation{to: {state: continued, valid: true, checked: now, agentAnchors: map[string]bool{"node:2": true}}}
	n = a.movementNotice(g, from, observations)
	if n.Status != "continued" || n.CheckedAt != now {
		t.Fatalf("continued: %+v", n)
	}
	source.Nodes = append(source.Nodes, ir.Node{Kind: ir.KindMessage, Actor: ir.Agent, Text: "An independent fix"})
	source.Cursor = ir.Cursor{Head: "independent", Offset: 50}
	independent, err := g.Observe(from, &source)
	if err != nil {
		t.Fatal(err)
	}
	observations[from] = movementObservation{state: independent, valid: true}
	if n = a.movementNotice(g, from, observations); n.Status != "diverged" {
		t.Fatalf("diverged: %+v", n)
	}
}
func TestMovementHookCacheIsRevokedByReturnUndoAndPreference(t *testing.T) {
	t.Setenv("HOPSESH_MACHINE", "alice-desktop")
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	a, g, from, to, _, _ := movementFixture(t)
	path := filepath.Join(root, "projects", "test", "alice-session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("untouched native history\n"), 0600); err != nil {
		t.Fatal(err)
	}
	save := func() {
		t.Helper()
		if err := os.WriteFile(lineage.PathFor(path), g.Encode(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	save()
	e := Entry{Session: agent.Summary{Key: g.Replica(from).Key, Path: path}, Lineage: g, Movement: a.movementNotice(g, from, nil)}
	a.cacheMovement(e)
	ctx := context.Background()
	read := func() string {
		t.Helper()
		s, err := a.SessionMovementNoticeForPath(ctx, "claude", "", "alice-session", path)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if s := read(); !strings.Contains(s, "Prepared in Codex on bob-laptop") {
		t.Fatalf("notice missing %q", s)
	}
	off := false
	a.Cfg.MovementNotices = &off
	if read() != "" {
		t.Fatal("disabled notice delivered")
	}
	a.Cfg.MovementNotices = nil
	original := g.Clone()
	if err := g.AppendHop(lineage.Hop{Kind: lineage.HopContinue, ID: "return", From: to, To: from, Notify: true}); err != nil {
		t.Fatal(err)
	}
	save()
	if read() != "" {
		t.Fatal("return delivered obsolete notice")
	}
	g = original
	g.UndoOperation("move-one")
	save()
	if read() != "" {
		t.Fatal("undo delivered cached notice")
	}
	native, _ := os.ReadFile(path)
	if string(native) != "untouched native history\n" {
		t.Fatal("notice changed native history")
	}
}
func TestMovementHookRejectsForeignRootAndSymlink(t *testing.T) {
	t.Setenv("HOPSESH_MACHINE", "alice-desktop")
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	a, g, _, _, _, _ := movementFixture(t)
	foreign := filepath.Join(t.TempDir(), "alice-session.jsonl")
	if err := os.WriteFile(lineage.PathFor(foreign), g.Encode(), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SessionMovementNoticeForPath(context.Background(), "claude", "", "alice-session", foreign); err == nil {
		t.Fatal("foreign root accepted")
	}
	link := filepath.Join(root, "alias.jsonl")
	if err := os.Symlink(lineage.PathFor(foreign), lineage.PathFor(link)); err != nil {
		t.Skip(err)
	}
	if _, err := a.SessionMovementNoticeForPath(context.Background(), "claude", "", "alice-session", link); err == nil {
		t.Fatal("symlink escape accepted")
	}
}
func TestMovementPreferenceDefaults(t *testing.T) {
	c := config.Defaults()
	if !c.MovementNoticesOn() {
		t.Fatal("not on by default")
	}
	a := New(c, all.Registry(), t.TempDir(), nil)
	t.Cleanup(func() { _ = a.Catalog.Close() })
	if !a.DefaultOptions().Notify {
		t.Fatal("move did not inherit default")
	}
	off := false
	a.Cfg.MovementNotices = &off
	if a.DefaultOptions().Notify {
		t.Fatal("move ignored off preference")
	}
}

func TestMovementEnrichmentReadsNativeWorkAndReturnRelations(t *testing.T) {
	ctx := context.Background()
	mod := claude.New()
	root := t.TempDir()
	a := New(config.Defaults(), all.Registry(), t.TempDir(), nil)
	t.Cleanup(func() { _ = a.Catalog.Close() })
	g := lineage.New("native-enrichment")
	var entries []Entry
	var machines []*Machine
	var ids []lineage.ReplicaID
	for i, name := range []string{"alice-desktop", "bob-laptop"} {
		home := filepath.Join(root, name)
		agentRoot := filepath.Join(home, ".claude")
		path := filepath.Join(agentRoot, "projects", "demo", "session.jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		native := `{"type":"user","uuid":"u1","sessionId":"session","message":{"role":"user","content":"Fix tests"}}` + "\n"
		if err := os.WriteFile(path, []byte(native), 0600); err != nil {
			t.Fatal(err)
		}
		in := agent.Install{Agent: "claude", Present: true, Roots: map[string]string{"home": agentRoot}}
		hm := &host.Machine{Name: name, Local: true, Facts: host.Facts{OS: runtime.GOOS, Home: home, Endpoint: name}}
		h, err := hm.For(ctx, mod.Spec(), in, nil)
		if err != nil {
			t.Fatal(err)
		}
		key := agent.SessionKey{Agent: "claude", Session: "session"}
		e := Entry{Machine: name, Agent: "claude", Session: agent.Summary{Key: key, Path: path}, ObservedAt: time.Now().UTC()}
		id := g.Upsert(lineage.Replica{Location: name, Endpoint: name, Key: key})
		ids = append(ids, id)
		seg, err := mod.Read(ctx, h, in, e.Session, ir.Cursor{})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if _, err = g.Observe(id, &seg); err != nil {
				t.Fatal(err)
			}
		} else {
			s, _ := g.LatestState(ids[0])
			g.Deliver(id, seg.Cursor, s.Projection, s.Heads, nil)
		}
		entries = append(entries, e)
		machines = append(machines, &Machine{Name: name, Local: i == 0, Status: StatusOK, host: hm, Agents: []AgentState{{Agent: "claude", Install: in}}})
	}
	if err := g.AppendHop(lineage.Hop{ID: "native-move", Kind: lineage.HopMove, From: ids[0], To: ids[1], Notify: true}); err != nil {
		t.Fatal(err)
	}
	for i := range entries {
		entries[i].Lineage = g.Clone()
		if err := os.WriteFile(lineage.PathFor(entries[i].Session.Path), g.Encode(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	inv := &Inventory{Entries: entries, Machines: machines}
	a.EnrichMovement(ctx, inv)
	if got := inv.Entries[1].Returns; len(got) != 1 || got[0].Status != "same" {
		t.Fatalf("synchronized return: %+v", got)
	}
	if n := inv.Entries[0].Movement; n == nil || n.Status != "prepared" {
		t.Fatalf("prepared: %+v", n)
	}
	f, err := os.OpenFile(entries[1].Session.Path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"type":"assistant","uuid":"a1","parentUuid":"u1","sessionId":"session","message":{"role":"assistant","content":[{"type":"text","text":"Tests fixed"}]}}` + "\n")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	a.EnrichMovement(ctx, inv)
	if got := inv.Entries[1].Returns; len(got) != 1 || got[0].Status != "available" {
		t.Fatalf("new-work return: %+v", got)
	}
	if n := inv.Entries[0].Movement; n == nil || n.Status != "continued" {
		t.Fatalf("continued: %+v", n)
	}
	// A first account observation changes only the binding. The exact native
	// original is still measurable; scanning must not label it missing/unverified.
	machines[0].Agents[0].Install.Profile = &agent.RuntimeProfile{Root: machines[0].Agents[0].Install.Root("home"), Default: true, Binding: "first-observation"}
	a.EnrichMovement(ctx, inv)
	if got := inv.Entries[1].Returns; len(got) != 1 || got[0].Status != "available" {
		t.Fatalf("binding observation hid original: %+v", got)
	}
	if n := inv.Entries[0].Movement; n == nil || n.Status != "continued" {
		t.Fatalf("binding observation erased movement: %+v", n)
	}
	// Renaming either machine changes display/routing labels, never endpoint identity.
	inv.Machines[0].Name, inv.Entries[0].Machine = "renamed-desktop", "renamed-desktop"
	inv.Machines[1].Name, inv.Entries[1].Machine = "renamed-laptop", "renamed-laptop"
	a.EnrichMovement(ctx, inv)
	if got := inv.Entries[1].Returns; len(got) != 1 || got[0].Machine != "renamed-desktop" || !got[0].Local {
		t.Fatalf("renamed return: %+v", got)
	}
	if n := inv.Entries[0].Movement; n == nil || n.Machine != "renamed-laptop" || !strings.Contains(n.Text, "renamed-laptop") {
		t.Fatalf("renamed notice: %+v", n)
	}
	// A local-only refresh retains the last successful remote observation and timestamp.
	checked := inv.Entries[0].Movement.CheckedAt
	inv.Entries = inv.Entries[:1]
	inv.Machines = inv.Machines[:1]
	a.EnrichMovement(ctx, inv)
	if n := inv.Entries[0].Movement; n == nil || n.Status != "continued" || n.CheckedAt != checked {
		t.Fatalf("offline erased observed work: %+v", n)
	}
	after, err := lineage.Read(host.LocalFS(), entries[0].Session.Path)
	if err != nil || len(after.States) != len(g.States) {
		t.Fatal("scan rewrote journaled lineage", err)
	}
}

func TestReturnRetiresBoundedOriginalOnlyWhileReplacementExists(t *testing.T) {
	_, g, from, to, _, _ := movementFixture(t)
	st, _ := g.LatestState(from)
	g.Hops[0].Rollover = &lineage.Rollover{Replica: from, Cursor: ir.Cursor{Head: st.Head, Offset: st.Offset}}
	original := Entry{Live: agent.LiveInfo{State: agent.Ended}}
	observed := map[lineage.ReplicaID]movementObservation{from: {valid: true, state: st, entry: &original}, to: {valid: true}}
	if !retiredReturn(g, from, observed) {
		t.Fatal("unchanged bounded original not retired")
	}
	delete(observed, to)
	if retiredReturn(g, from, observed) {
		t.Fatal("original hidden after replacement disappeared")
	}
	observed[to] = movementObservation{valid: true}
	for _, unknown := range []agent.Liveness{agent.Unknown, ""} {
		original.Live.State = unknown
		if retiredReturn(g, from, observed) {
			t.Fatalf("original hidden without confirmed end: %q", unknown)
		}
	}
	original.Live.State = agent.Live
	if retiredReturn(g, from, observed) {
		t.Fatal("open original hidden")
	}
	original.Live.State = agent.Ended
	st.Head = "new-work"
	observed[from] = movementObservation{valid: true, state: st, entry: &original}
	if retiredReturn(g, from, observed) {
		t.Fatal("edited original hidden")
	}
}

func TestMovementHookReadsEvidenceWithoutWriting(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	a, g, from, _, _, _ := movementFixture(t)
	path := filepath.Join(root, "alice-session.jsonl")
	if err := os.WriteFile(lineage.PathFor(path), g.Encode(), 0600); err != nil {
		t.Fatal(err)
	}
	read := func() string {
		t.Helper()
		s, err := a.SessionMovementNoticeForPath(context.Background(), "claude", "", "alice-session", path)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if s := read(); !strings.Contains(s, "Prepared in") {
		t.Fatalf("cold lookup: %q", s)
	}
	cachePath := a.movementCachePath(g.Replica(from).Key)
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Fatalf("hook wrote scan evidence: %v", err)
	}
	// A lookup may read an earlier generation while a scan commits a newer one.
	// Keeping both cold and warm lookups read-only removes that lost-update path.
	n := a.movementNotice(g, from, nil)
	n.Status, n.Text, n.CheckedAt = "continued", "Verified later work", time.Now().UTC()
	a.cacheMovement(Entry{Session: agent.Summary{Key: g.Replica(from).Key, Path: path}, Lineage: g, Movement: n})
	stamp := time.Unix(100, 0)
	if err := os.Chtimes(cachePath, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if s := read(); !strings.Contains(s, n.Text) {
		t.Fatalf("lost verified evidence: %q", s)
	}
	details, err := a.MovementNoticeDetailsForPath(context.Background(), "claude", "", "alice-session", path)
	if err != nil || details.Key != g.Replica(from).Key || details.Operation != n.Operation || details.Status != n.Status || details.Text != hookNoticeText(n) {
		t.Fatalf("details disagree with verified cache: %+v %v", details, err)
	}
	after, err := os.Stat(cachePath)
	if err != nil || !before.ModTime().Equal(after.ModTime()) || !os.SameFile(before, after) {
		t.Fatalf("warm lookup rewrote evidence: %v", err)
	}
}

func TestMovementHookHonorsCachedSuppressionUntilSourceChanges(t *testing.T) {
	for _, reason := range []string{"return", "opt out"} {
		t.Run(reason, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", root)
			a, g, from, to, _, _ := movementFixture(t)
			path := filepath.Join(root, "alice-session.jsonl")
			if err := os.WriteFile(lineage.PathFor(path), g.Encode(), 0600); err != nil {
				t.Fatal(err)
			}
			// A scan merges remote evidence without writing the source sidecar.
			merged := g.Clone()
			dst := from
			if reason == "opt out" {
				dst = merged.Upsert(lineage.Replica{Location: "third", Key: agent.SessionKey{Agent: "codex", Session: "third-session"}})
			}
			if err := merged.AppendHop(lineage.Hop{ID: "onward", Kind: lineage.HopContinue, From: to, To: dst, Parents: []string{"move-one"}, Notify: reason != "opt out"}); err != nil {
				t.Fatal(err)
			}
			n := a.movementNotice(merged, from, nil)
			if n != nil {
				t.Fatalf("fixture did not suppress notice: %+v", n)
			}
			a.cacheMovement(Entry{Session: agent.Summary{Key: g.Replica(from).Key, Path: path}, Lineage: g, Movement: n})
			read := func() string {
				t.Helper()
				s, err := a.SessionMovementNoticeForPath(context.Background(), "claude", "", "alice-session", path)
				if err != nil {
					t.Fatal(err)
				}
				return s
			}
			if got := read(); got != "" {
				t.Fatalf("resurrected %s notice: %q", reason, got)
			}
			if d, err := a.MovementNoticeDetailsForPath(context.Background(), "claude", "", "alice-session", path); err != nil || d.Key != g.Replica(from).Key || d.Text != "" || d.Operation != "" || d.Status != "" {
				t.Fatalf("suppressed details must clear exact source receipt: %+v %v", d, err)
			}
			// Once the source changes, the old cached decision is no longer evidence.
			g.Hops[0].ID = "new-departure"
			if err := os.WriteFile(lineage.PathFor(path), g.Encode(), 0600); err != nil {
				t.Fatal(err)
			}
			if got := read(); !strings.Contains(got, "Prepared in") {
				t.Fatalf("stale suppression survived source change: %q", got)
			}
		})
	}
}

func TestMovementForkPreservesObservedContinuation(t *testing.T) {
	a, g, _, source, _, segment := movementFixture(t)
	st, _ := g.LatestState(source)
	branch := g.Fork("child", st.Heads)
	target := g.Upsert(lineage.Replica{Line: branch, Location: "fork-laptop", Key: agent.SessionKey{Agent: "codex", Session: "fork-session"}})
	baseline := g.Deliver(target, segment.Cursor, st.Projection, st.Heads, nil)
	if err := g.AppendHop(lineage.Hop{ID: "fork", Kind: lineage.HopContinue, From: source, To: target, Source: st.ID, Target: baseline.ID, Fork: true, Notify: true}); err != nil {
		t.Fatal(err)
	}
	if n := a.movementNotice(g, source, nil); n == nil || n.Status != "forked" || !strings.Contains(n.Text, "prepared") {
		t.Fatalf("unobserved fork: %+v", n)
	}
	segment.Nodes = append(segment.Nodes, ir.Node{Kind: ir.KindMessage, Actor: ir.Agent, Text: "Work on the separate fork"})
	segment.Cursor = ir.Cursor{Head: "fork-response", Offset: 100}
	continued, err := g.Observe(target, &segment)
	if err != nil {
		t.Fatal(err)
	}
	checked := time.Now().UTC()
	observed := map[lineage.ReplicaID]movementObservation{target: {valid: true, checked: checked, state: continued, agentAnchors: map[string]bool{"node:1": true}}}
	n := a.movementNotice(g, source, observed)
	if n == nil || n.Status != "forked" || !strings.Contains(n.Text, "continued") || !strings.Contains(n.Text, "original branch remains available") || n.CheckedAt != checked {
		t.Fatalf("fork lost continuation evidence: %+v", n)
	}
	if strings.Contains(n.Text, "prepared") {
		t.Fatalf("observed fork still only prepared: %q", n.Text)
	}
}

func TestMovementHookFollowsDefaultRootRegistration(t *testing.T) {
	a, _, _, _, _, _ := movementFixture(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	endpoint := strings.Repeat("a", 64)
	if err := os.WriteFile(filepath.Join(os.Getenv("HOPSESH_CONFIG_DIR"), "endpoint-id"), []byte(endpoint), 0600); err != nil {
		t.Fatal(err)
	}
	for _, other := range []agent.RuntimeProfile{
		{ID: "remote-same-root", Agent: "claude", Endpoint: strings.Repeat("b", 64), Root: root, Name: "Default", Default: true},
		{ID: "other-agent", Agent: "codex", Endpoint: endpoint, Root: root, Name: "Default", Default: true},
		{ID: "other-root", Agent: "claude", Endpoint: endpoint, Root: t.TempDir(), Name: "Default", Default: true},
	} {
		if _, err := a.accountStore().Register(other); err != nil {
			t.Fatal(err)
		}
	}
	// A default hook can predate the scan that registers its canonical root.
	p, err := a.accountStore().Register(agent.RuntimeProfile{ID: "registered-default", Agent: "claude", Endpoint: endpoint, Root: root, Name: "Default", Binding: "account-binding"})
	if err != nil {
		t.Fatal(err)
	}
	g := lineage.New("registered-movement")
	key := agent.SessionKey{Agent: "claude", Profile: p.ID, Session: "same-session"}
	from := g.Upsert(lineage.Replica{Location: LocalName(), Endpoint: endpoint, Binding: p.Binding, Key: key})
	to := g.Upsert(lineage.Replica{Location: "destination", Key: agent.SessionKey{Agent: "codex", Session: "target-session"}})
	if err := g.AppendHop(lineage.Hop{ID: "registered-hop", Kind: lineage.HopContinue, From: from, To: to, Notify: true}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "same-session.jsonl")
	if err := os.WriteFile(lineage.PathFor(path), g.Encode(), 0600); err != nil {
		t.Fatal(err)
	}
	a.cacheMovement(Entry{Session: agent.Summary{Key: key, Path: path}, Lineage: g, Movement: a.movementNotice(g, from, nil)})
	if details, err := a.MovementNoticeDetailsForPath(context.Background(), "claude", "", "same-session", path); err != nil || details.Key != key || details.Operation != "registered-hop" {
		t.Fatalf("details lost resolved source scope: %+v %v", details, err)
	}
	// An obsolete unscoped cache must never shadow the now-exact profile cache.
	oldKey := key
	oldKey.Profile = ""
	b, _ := json.Marshal(cachedMovement{Key: oldKey, Path: path, Digest: digest(g.Encode()), Notice: &MovementNotice{Text: "WRONG PROFILE"}})
	if err := os.WriteFile(a.movementCachePath(oldKey), b, 0600); err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"", p.ID} {
		for _, transcript := range []string{"", path} {
			got, err := a.SessionMovementNoticeForPath(context.Background(), "claude", profile, "same-session", transcript)
			if err != nil || !strings.Contains(got, "Prepared in Codex on destination") || strings.Contains(got, "WRONG PROFILE") {
				t.Fatalf("profile=%q path=%q: %q %v", profile, transcript, got, err)
			}
		}
	}
	if got, err := a.MovementNoticeDetailsForPath(context.Background(), "claude", "", "same-session", path); err != nil || got.Key.Profile != p.ID {
		t.Fatalf("delivery scope differs from notice scope: %+v %v", got, err)
	}
	for _, foreign := range []string{"remote-same-root", "other-agent"} {
		if got, err := a.SessionMovementNoticeForPath(context.Background(), "claude", foreign, "same-session", path); err == nil || got != "" {
			t.Fatalf("accepted foreign profile %q: %q %v", foreign, got, err)
		}
	}
	alias := filepath.Join(t.TempDir(), "default-alias")
	if err := os.Symlink(root, alias); err == nil {
		t.Setenv("CLAUDE_CONFIG_DIR", alias)
		if got, err := a.SessionMovementNoticeForPath(context.Background(), "claude", "", "same-session", path); err != nil || !strings.Contains(got, "Prepared in") {
			t.Fatalf("canonical default root: %q %v", got, err)
		}
	}
	// Default selection is by canonical root, never another profile's Default flag.
	other := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", other)
	if got, err := a.SessionMovementNoticeForPath(context.Background(), "claude", "", "same-session", path); err == nil || got != "" {
		t.Fatalf("crossed default roots: %q %v", got, err)
	}
	// A registered path replaced by a symlink no longer denotes its adopted root.
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, root); err == nil {
		if got, err := a.SessionMovementNoticeForPath(context.Background(), "claude", p.ID, "same-session", path); err == nil || got != "" {
			t.Fatalf("accepted replaced registered root: %q %v", got, err)
		}
	}
}

func TestMovementNoticeDetailsDistinguishRepeatedRoutesWithoutScan(t *testing.T) {
	for _, staleCache := range []bool{false, true} {
		name := "no cache"
		if staleCache {
			name = "stale cache"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", root)
			a, g, from, to, _, _ := movementFixture(t)
			path := filepath.Join(root, "alice-session.jsonl")
			key := g.Replica(from).Key
			save := func() {
				t.Helper()
				if err := os.WriteFile(lineage.PathFor(path), g.Encode(), 0600); err != nil {
					t.Fatal(err)
				}
			}
			read := func() MovementNoticeDetails {
				t.Helper()
				d, err := a.MovementNoticeDetailsForPath(context.Background(), key.Agent, key.Profile, string(key.Session), path)
				if err != nil || d.Key != key {
					t.Fatalf("lookup: %+v %v", d, err)
				}
				return d
			}
			save()
			if staleCache {
				a.cacheMovement(Entry{Session: agent.Summary{Key: key, Path: path}, Lineage: g, Movement: a.movementNotice(g, from, nil)})
			}
			originalCache, _ := os.ReadFile(a.movementCachePath(key))
			first := read()
			if first.Text == "" || first.Operation != "move-one" || first.Status != "prepared" {
				t.Fatalf("first departure: %+v", first)
			}
			if err := g.AppendHop(lineage.Hop{ID: "return", Kind: lineage.HopContinue, From: to, To: from, Parents: []string{"move-one"}, Notify: true}); err != nil {
				t.Fatal(err)
			}
			save()
			if returned := read(); returned.Text != "" || returned.Operation != "" || returned.Status != "" {
				t.Fatalf("return retained departure details: %+v", returned)
			}
			if err := g.AppendHop(lineage.Hop{ID: "move-two", Kind: lineage.HopContinue, From: from, To: to, Parents: []string{"return"}, Notify: true}); err != nil {
				t.Fatal(err)
			}
			save()
			second := read()
			if second.Text != first.Text || second.Operation != "move-two" || second.Status != "prepared" {
				t.Fatalf("identical words lost new operation: first=%+v second=%+v", first, second)
			}
			if wrapper, err := a.SessionMovementNoticeForPath(context.Background(), key.Agent, key.Profile, string(key.Session), path); err != nil || wrapper != second.Text {
				t.Fatalf("string wrapper differs: %q %v", wrapper, err)
			}
			g.UndoOperation("move-two")
			save()
			if undone := read(); undone.Text != "" || undone.Operation != "" || undone.Status != "" {
				t.Fatalf("undo retained departure details: %+v", undone)
			}
			currentCache, err := os.ReadFile(a.movementCachePath(key))
			if staleCache {
				if err != nil || string(currentCache) != string(originalCache) {
					t.Fatalf("lookup rewrote stale scan cache: %v", err)
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("lookup created scan cache: %v", err)
			}
		})
	}
}
