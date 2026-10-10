package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Selected by the mandatory TestMovement suite on Linux, macOS and Windows.
// Native readers and writers perform both legs in temporary fixture homes.
func TestMovementConversationConflictReview(t *testing.T) {
	for _, route := range [][2]string{{"claude", "codex"}, {"codex", "claude"}} {
		t.Run(route[0]+"-"+route[1]+"-"+route[0], func(t *testing.T) {
			a, b, in := movementInput(t, route[0], route[1])
			ctx := context.Background()
			env := move.Env{StateDir: t.TempDir()}
			const shared = "CONFLICT-REVIEW-SHARED-WORK"
			appendComparisonWork(t, route[0], in.Session.Path, shared, shared+"-REPLY")
			in.Session = findRouteSession(t, a, in.Source.Module, in.Source.Install, in.Session.Key)
			outbound, _ := applyMovement(t, in, move.Options{TargetDir: b.repo, Mark: true, Notify: true}, env)
			original := findRouteSession(t, a, in.Source.Module, in.Source.Install, in.Session.Key)
			incoming := findRouteSession(t, b, in.Target.Module, in.Target.Install, outbound.Placement.Key)
			originalGraph, incomingGraph := movementGraph(t, original), movementGraph(t, incoming)
			hop := incomingGraph.ActiveHops()[0]
			sharedState, ok := incomingGraph.LatestState(hop.To)
			if !ok {
				t.Fatal("outbound transfer did not record destination coverage")
			}
			sharedRevisions := len(incomingGraph.Covered(sharedState.Heads))
			if sharedRevisions == 0 {
				t.Fatal("outbound transfer has no shared causal revisions")
			}

			// Continue the original independently, then continue its other-agent copy.
			const originalOnly = "CONFLICT-REVIEW-ORIGINAL-ONLY"
			const incomingOnly = "CONFLICT-REVIEW-INCOMING-ONLY"
			appendComparisonWork(t, route[0], original.Path, originalOnly, originalOnly+"-REPLY")
			appendComparisonWork(t, route[1], incoming.Path, incomingOnly, incomingOnly+"-REPLY")
			original = findRouteSession(t, a, in.Source.Module, in.Source.Install, original.Key)
			incoming = findRouteSession(t, b, in.Target.Module, in.Target.Install, incoming.Key)
			back := move.Input{Source: in.Target, Session: incoming, Lineage: incomingGraph, Target: in.Source,
				Copies: []move.Copy{{Summary: original, Lineage: originalGraph}}}
			opt := move.Options{TargetDir: a.repo, TargetSession: original.Key.String(), Mark: true, Notify: true}
			originalBytes, incomingBytes := movementBytes(t, original.Path), movementBytes(t, incoming.Path)
			before := comparisonFixtureBytes(t, a.m.Facts.Home, b.m.Facts.Home, env.StateDir)
			sourceLineage, targetLineage := incomingGraph.Encode(), originalGraph.Encode()
			assertReadOnly := func() {
				t.Helper()
				if !reflect.DeepEqual(before, comparisonFixtureBytes(t, a.m.Facts.Home, b.m.Facts.Home, env.StateDir)) {
					t.Fatal("conflict review changed fixture files, native work or persisted lineage")
				}
				if !bytes.Equal(sourceLineage, incomingGraph.Encode()) || !bytes.Equal(targetLineage, originalGraph.Encode()) {
					t.Fatal("conflict review mutated input lineage")
				}
			}
			assertComparison := func(p *move.Plan) {
				t.Helper()
				if p.Continue == nil || p.Continue.Comparison == nil {
					t.Fatal("return review omitted conversation comparison")
				}
				c := p.Continue.Comparison
				if !c.Verified || c.Classification != "diverged" || c.SharedRevisions != sharedRevisions {
					t.Fatalf("comparison must verify shared ancestry and exclusive work: %+v", c)
				}
				assertComparisonSide(t, c.Source, back.Source, incoming, route[1], incomingOnly)
				assertComparisonSide(t, c.Destination, back.Target, original, route[0], originalOnly)
			}

			blocked, err := move.Build(ctx, back, opt)
			if err != nil {
				t.Fatal(err)
			}
			assertReadOnly()
			if len(blocked.Blockers) == 0 || blocked.Conflict == "" || blocked.NoWork || blocked.Continue == nil || blocked.Continue.Relation != move.RelationDiverged {
				t.Fatalf("independent work must block return to the original: %+v", blocked)
			}
			assertComparison(blocked)

			opt.Conflict = move.ConflictKeepBoth
			separate, err := move.Build(ctx, back, opt)
			if err != nil {
				t.Fatal(err)
			}
			assertReadOnly()
			assertComparison(separate)
			if len(separate.Blockers) != 0 || separate.NoWork || !separate.Options.Fork || separate.Continue.Relation != move.RelationNew || separate.Continue.AppendTo != nil {
				t.Fatalf("keep-both must plan a separate branch: %+v", separate)
			}
			key := separate.Placement.Key
			if key.Agent != original.Key.Agent || key.Profile != original.Key.Profile || key.Session == "" || key.Session == original.Key.Session || key.Session == incoming.Key.Session {
				t.Fatalf("keep-both must allocate a new native key in the target profile: %s", key)
			}
			if _, err := move.Apply(ctx, separate, back, env); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(originalBytes, movementBytes(t, original.Path)) || !bytes.Equal(incomingBytes, movementBytes(t, incoming.Path)) {
				t.Fatal("keep-both changed an existing native conversation")
			}
			home := findRouteSession(t, a, back.Target.Module, back.Target.Install, key)
			if home.Path == original.Path || home.Path == incoming.Path {
				t.Fatal("keep-both reused an existing native path")
			}
			seg := readAll(t, a, back.Target.Module, back.Target.Install, home)
			if !mentions(seg, shared) || !mentions(seg, incomingOnly) || mentions(seg, originalOnly) {
				t.Fatal("separate return must carry shared and incoming work without original-only work")
			}
			if route[1] == "codex" && !mentions(seg, incomingOnly+"-REPLY") {
				t.Fatal("separate Claude return lost the Codex assistant reply")
			}
			g := movementGraph(t, home)
			if g.Family != incomingGraph.Family || g.Branch == incomingGraph.Branch || g.Branch == originalGraph.Branch {
				t.Fatal("keep-both did not persist a new conversation branch in the same family")
			}
			if movementGraph(t, original).Branch != originalGraph.Branch || movementGraph(t, incoming).Branch != incomingGraph.Branch {
				t.Fatal("keep-both moved an existing conversation to another branch")
			}
			var fork *lineage.Hop
			for _, h := range g.ActiveHops() {
				if h.ID == separate.OperationID {
					copy := h
					fork = &copy
				}
			}
			if fork == nil || !fork.Fork || g.Replica(fork.To).Key != key || g.Replica(fork.From).Key != incoming.Key {
				t.Fatalf("separate return has no fork receipt for the new native key: %+v", fork)
			}
		})
	}
}

// A checkpoint emitted before the completed response must not truncate a future
// handoff. Ending Claude afterward changes the physical cursor, not its coverage.
func TestMovementStaleClaudeCheckpointReturn(t *testing.T) {
	a, b, in := movementInput(t, "claude", "codex")
	ctx := context.Background()
	env := move.Env{StateDir: t.TempDir()}
	// Keep the native fixture's signed reasoning, tool call/result and final reply,
	// but put its checkpoint at the prompt attachment before that response.
	var transcript []byte
	for _, line := range bytes.Split(movementBytes(t, in.Session.Path), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		if record["type"] == "last-prompt" {
			continue
		}
		if record["uuid"] == "a1" {
			record["parentUuid"] = "checkpoint-attachment"
		}
		transcript = append(transcript, checkpointRecords(t, record)...)
		if record["uuid"] == "u1" {
			transcript = append(transcript, checkpointRecords(t,
				map[string]any{"type": "attachment", "uuid": "checkpoint-attachment", "parentUuid": "u1", "sessionId": sid},
				map[string]any{"type": "last-prompt", "leafUuid": "checkpoint-attachment", "lastPrompt": "What is the codeword in notes.txt?", "sessionId": sid})...)
		}
	}
	if err := os.WriteFile(in.Session.Path, transcript, 0o600); err != nil {
		t.Fatal(err)
	}
	in.Session = findRouteSession(t, a, in.Source.Module, in.Source.Install, in.Session.Key)
	before := readAll(t, a, in.Source.Module, in.Source.Install, in.Session)
	wantKinds := []ir.Kind{ir.KindMessage, ir.KindReasoning, ir.KindToolCall, ir.KindToolResult, ir.KindMessage}
	var kinds []ir.Kind
	for _, n := range before.Nodes {
		kinds = append(kinds, n.Kind)
	}
	if !reflect.DeepEqual(kinds, wantKinds) || before.Nodes[3].Result == nil || before.Nodes[3].Result.Output != "The codeword is PLUM-7." || !strings.Contains(before.Nodes[4].Text, "PLUM-7 (from") || before.Nodes[4].Native.Anchor != "a2/0" {
		t.Fatalf("stale checkpoint omitted the completed assistant/tool response: %+v", before.Nodes)
	}
	if before.Cursor.Offset != int64(len(transcript)) {
		t.Fatalf("reader cursor %d did not include the complete native file (%d)", before.Cursor.Offset, len(transcript))
	}
	outbound, _ := applyMovement(t, in, move.Options{TargetDir: b.repo, Mark: true, Notify: true}, env)
	original := findRouteSession(t, a, in.Source.Module, in.Source.Install, in.Session.Key)
	incoming := findRouteSession(t, b, in.Target.Module, in.Target.Install, outbound.Placement.Key)
	carried := readAll(t, b, in.Target.Module, in.Target.Install, incoming)
	if !mentions(carried, "PLUM-7 (from") || !mentions(carried, "[prior agent · Claude Code · execute]") || !mentions(carried, "The codeword is PLUM-7.") {
		t.Fatal("Codex did not receive the final Claude reply and completed tool response")
	}
	g := movementGraph(t, incoming)
	hops := g.ActiveHops()
	if len(hops) != 1 {
		t.Fatalf("outbound movement receipts: %+v", hops)
	}
	source, sourceOK := g.LatestState(hops[0].From)
	target, targetOK := g.LatestState(hops[0].To)
	shared := g.Covered(source.Heads)
	if !sourceOK || !targetOK || len(shared) != 5 || !reflect.DeepEqual(shared, g.Covered(target.Heads)) {
		t.Fatal("handoff did not preserve all five logical revisions, including the final response")
	}
	afterHandoff := readAll(t, a, in.Source.Module, in.Source.Install, original)
	if !reflect.DeepEqual(before.Nodes, afterHandoff.Nodes) {
		t.Fatal("handoff changed the original Claude conversation")
	}
	appendCheckpointMetadata(t, original.Path, "a2", "checkpoint-stop")
	ended := readAll(t, a, in.Source.Module, in.Source.Install, original)
	if !reflect.DeepEqual(before.Nodes, ended.Nodes) || ended.Cursor.Head != before.Cursor.Head || ended.Cursor.Offset <= afterHandoff.Cursor.Offset || ended.Cursor.Offset != int64(len(movementBytes(t, original.Path))) {
		t.Fatalf("end metadata changed logical work or failed to advance the physical cursor: %+v", ended.Cursor)
	}
	h, err := a.m.For(ctx, in.Source.Module.Spec(), in.Source.Install, nil)
	if err != nil {
		t.Fatal(err)
	}
	delta, err := in.Source.Module.(agent.Reader).Read(ctx, h, in.Source.Install, original, afterHandoff.Cursor)
	if err != nil || len(delta.Nodes) != 0 || delta.Cursor != ended.Cursor {
		t.Fatalf("metadata-only ending became conversation work: %+v %v", delta, err)
	}
	const returnedWork = "STALE-CHECKPOINT-CODEX-WORK"
	appendCodexTurn(t, incoming.Path, returnedWork, returnedWork+"-REPLY")
	incoming = findRouteSession(t, b, in.Target.Module, in.Target.Install, incoming.Key)
	back := move.Input{Source: in.Target, Session: incoming, Lineage: movementGraph(t, incoming), Target: in.Source,
		Copies: []move.Copy{{Summary: original, Lineage: movementGraph(t, original)}}}
	opt := move.Options{TargetDir: a.repo, TargetSession: original.Key.String(), Mark: true, Notify: true}
	buildReturn := func(cursor ir.Cursor) *move.Plan {
		t.Helper()
		files := comparisonFixtureBytes(t, a.m.Facts.Home, b.m.Facts.Home, env.StateDir)
		p, err := move.Build(ctx, back, opt)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(files, comparisonFixtureBytes(t, a.m.Facts.Home, b.m.Facts.Home, env.StateDir)) {
			t.Fatal("checkpoint return planning changed native files or persisted lineage")
		}
		if len(p.Blockers) != 0 || p.Conflict != "" || p.NoWork || p.Options.Fork || p.Continue == nil || p.Continue.Relation != move.RelationAppend || p.Continue.AppendTo == nil || p.Continue.AppendTo.Key != original.Key || p.Placement.Key != original.Key {
			t.Fatalf("metadata-only ending must still append to the original: %+v", p)
		}
		if p.ExpectedDestination[original.Path] != cursor {
			t.Fatalf("return did not pin the fresh physical destination cursor: %+v; want %+v", p.ExpectedDestination, cursor)
		}
		c := p.Continue.Comparison
		if c == nil || !c.Verified || c.Classification != "source-only" || c.SharedRevisions != 5 {
			t.Fatalf("checkpoint return has inconsistent shared coverage: %+v", c)
		}
		assertComparisonSide(t, c.Source, back.Source, incoming, "codex", returnedWork)
		if c.Destination.Identity.Key != original.Key || c.Destination.Status != "verified" || !c.Destination.ExclusiveKnown || c.Destination.Revisions != 0 || c.Destination.Counts != (move.ComparisonCounts{}) || len(c.Destination.Preview) != 0 {
			t.Fatalf("end metadata was counted as destination-only work: %+v", c.Destination)
		}
		return p
	}
	stale := buildReturn(ended.Cursor)
	// Even metadata arriving after review invalidates the physical append guard.
	appendCheckpointMetadata(t, original.Path, "checkpoint-stop", "checkpoint-late-stop")
	fresh := readAll(t, a, in.Source.Module, in.Source.Install, original)
	if fresh.Cursor.Head != ended.Cursor.Head || fresh.Cursor.Offset <= ended.Cursor.Offset || !reflect.DeepEqual(fresh.Nodes, ended.Nodes) {
		t.Fatal("late metadata changed logical coverage instead of only the physical cursor")
	}
	originalBytes, incomingBytes := movementBytes(t, original.Path), movementBytes(t, incoming.Path)
	originalReceipt, incomingReceipt := movementBytes(t, lineage.PathFor(original.Path)), movementBytes(t, lineage.PathFor(incoming.Path))
	if _, err := move.Apply(ctx, stale, back, env); err == nil || !strings.Contains(err.Error(), "destination changed since planning") {
		t.Fatalf("stale metadata cursor was not guarded: %v", err)
	}
	if !bytes.Equal(originalBytes, movementBytes(t, original.Path)) || !bytes.Equal(incomingBytes, movementBytes(t, incoming.Path)) || !bytes.Equal(originalReceipt, movementBytes(t, lineage.PathFor(original.Path))) || !bytes.Equal(incomingReceipt, movementBytes(t, lineage.PathFor(incoming.Path))) {
		t.Fatal("rejected stale return changed conversation files or lineage receipts")
	}
	returned := buildReturn(fresh.Cursor)
	if _, err := move.Apply(ctx, returned, back, env); err != nil {
		t.Fatal(err)
	}
	home := findRouteSession(t, a, in.Source.Module, in.Source.Install, original.Key)
	now := movementBytes(t, home.Path)
	if home.Path != original.Path || !bytes.HasPrefix(now, originalBytes) || !bytes.Equal(incomingBytes, movementBytes(t, incoming.Path)) {
		t.Fatal("return did not preserve the original native records and Codex source")
	}
	var first struct {
		Parent string `json:"parentUuid"`
	}
	if err := json.Unmarshal(bytes.SplitN(now[len(originalBytes):], []byte("\n"), 2)[0], &first); err != nil || first.Parent != "checkpoint-late-stop" {
		t.Fatalf("return appended to an outdated native checkpoint: parent=%q err=%v", first.Parent, err)
	}
	final := readAll(t, a, in.Source.Module, in.Source.Install, home)
	if !mentions(final, "PLUM-7 (from") || !mentions(final, returnedWork) || !mentions(final, returnedWork+"-REPLY") {
		t.Fatal("return lost the completed Claude response or new Codex work")
	}
	for _, anchor := range []string{"u1/0", "a1/0", "a1/1", "u2/0", "a2/0"} {
		count := 0
		for _, n := range final.Nodes {
			if n.Native != nil && n.Native.Anchor == anchor {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("return lost or replayed shared native record %s: %d occurrences", anchor, count)
		}
	}
	finalGraph := movementGraph(t, home)
	finalHops := finalGraph.ActiveHops()
	if finalGraph.Branch != g.Branch || finalGraph.Family != g.Family || len(finalHops) != 2 || finalHops[1].Fork || finalGraph.Journey().RoundTrips != 1 {
		t.Fatal("checkpoint return created divergence instead of a same-branch round trip")
	}
	finalSource, okSource := finalGraph.LatestState(finalHops[1].From)
	finalTarget, okTarget := finalGraph.LatestState(finalHops[1].To)
	if !okSource || !okTarget || len(finalGraph.Covered(finalTarget.Heads)) != 8 || !reflect.DeepEqual(finalGraph.Covered(finalSource.Heads), finalGraph.Covered(finalTarget.Heads)) {
		t.Fatal("return coverage must contain exactly five shared and three Codex revisions")
	}
}

func checkpointRecords(t *testing.T, records ...map[string]any) []byte {
	t.Helper()
	var out []byte
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, line...)
		out = append(out, '\n')
	}
	return out
}

func appendCheckpointMetadata(t *testing.T, path, parent, id string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := f.Write(checkpointRecords(t,
		map[string]any{"type": "system", "uuid": id, "parentUuid": parent, "sessionId": sid, "subtype": "local_command", "content": "/exit"},
		map[string]any{"type": "last-prompt", "leafUuid": id, "sessionId": sid}))
	closeErr := f.Close()
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
}

func appendComparisonWork(t *testing.T, agentID, path, user, reply string) {
	t.Helper()
	if agentID == "claude" {
		appendTurn(t, path, user)
	} else {
		appendCodexTurn(t, path, user, reply)
	}
}

func assertComparisonSide(t *testing.T, side move.ComparisonSide, endpoint move.Side, session agent.Summary, agentID, exclusive string) {
	t.Helper()
	id := side.Identity
	if id.Key != session.Key || id.Agent != session.Key.Agent || id.AgentName != endpoint.Module.Spec().Name || id.Profile != endpoint.Install.ProfileID() || id.Machine != endpoint.Machine.Name || id.MachineID != endpoint.Machine.Facts.Endpoint || id.Title != session.Title {
		t.Fatalf("comparison identifies the wrong native conversation: %+v; want %s on %s", id, session.Key, endpoint.Machine.Name)
	}
	wantCounts := move.ComparisonCounts{Nodes: 1, Messages: 1, UserMessages: 1}
	wantRevisions := 1
	wantPreview := []move.ComparisonPreview{{Kind: ir.KindMessage, Role: "user", Text: exclusive}}
	if agentID == "codex" {
		wantCounts = move.ComparisonCounts{Nodes: 3, Messages: 2, UserMessages: 1, AssistantMessages: 1, Other: 1}
		wantRevisions = 3
		wantPreview = append(wantPreview, move.ComparisonPreview{Kind: ir.KindMessage, Role: "assistant", Text: exclusive + "-REPLY"})
	}
	if side.Status != "verified" || !side.ExclusiveKnown || side.Revisions != wantRevisions || side.Counts != wantCounts || side.Truncated || side.PreviewOmitted != 0 {
		t.Fatalf("exclusive native counts: %+v; want %+v and %d revisions", side, wantCounts, wantRevisions)
	}
	// Compare complete ordinary-message previews, allowing their native timestamps.
	preview := append([]move.ComparisonPreview(nil), side.Preview...)
	for i := range preview {
		preview[i].Time = nil
	}
	if !reflect.DeepEqual(preview, wantPreview) {
		t.Fatalf("preview must contain only this side's causal delta: %+v; want %+v", preview, wantPreview)
	}
}

func comparisonFixtureBytes(t *testing.T, roots ...string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, root := range roots {
		if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				out[path] = movementBytes(t, path)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	return out
}
