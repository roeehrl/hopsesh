package opencode

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/testkit/fakeopencode"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

const writtenSID = "ses_0123456789abHopseshWrite01"

// writeRequest is a continuation as the converter renders one: prompts, replies, shell
// calls replayed (one that exited, one that failed with an exit status in another folder,
// one that failed without), an empty reply, and hopsesh's briefing and notice, which have no time.
func writeRequest(sid string) ir.WriteRequest {
	t0 := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	exit0, exit2 := 0, 2
	shell := func(id, cmd string, res ir.ToolResult, dir ...string) *ir.ToolPair {
		res.CallID = id
		sh := &ir.Shell{Command: cmd}
		if len(dir) > 0 {
			sh.Dir = dir[0]
		}
		return &ir.ToolPair{Call: ir.ToolCall{CallID: id, Name: "Bash", Kind: ir.ToolExecute, Input: json.RawMessage(`{"command":"` + cmd + `"}`), Shell: sh}, Result: res}
	}
	return ir.WriteRequest{OperationID: "op-write", Mode: ir.WriteNew, SessionID: sid,
		Header: ir.Header{CWD: home + "/git/demo", Title: "Find the codeword", Model: "claude-opus-4-5"},
		Items: []ir.Item{
			{Node: "n1", Role: ir.RoleUser, Time: t0, Text: "What is the codeword in notes.txt?", Coverage: []ir.NodeID{"n1"}, Fidelity: "text"},
			{Node: "n2", Role: ir.RoleAgent, Time: t0.Add(time.Second), Text: "Let me look.", Fidelity: "text"},
			{Node: "n3", Role: ir.RoleAgent, Time: t0.Add(2 * time.Second), Fidelity: "native",
				Tool: shell("c1", "cat notes.txt", ir.ToolResult{Status: ir.StatusCompleted, ExitCode: &exit0, Output: "PLUM-7\n"})},
			{Node: "n4", Role: ir.RoleAgent, Time: t0.Add(3 * time.Second), Fidelity: "native",
				Tool: shell("c2", "ls missing", ir.ToolResult{Status: ir.StatusFailed, ExitCode: &exit2, Output: "ls: missing: No such file"}, home+"/git/demo/sub")},
			{Node: "n5", Role: ir.RoleAgent, Time: t0.Add(3 * time.Second), Fidelity: "native", // the same second
				Tool: shell("c3", "false", ir.ToolResult{Status: ir.StatusFailed})},
			{Node: "n6", Role: ir.RoleAgent, Time: t0.Add(4 * time.Second), Text: "The codeword is PLUM-7."},
			{Node: "n7", Role: ir.RoleAgent, Time: t0.Add(5 * time.Second), Text: "  "},
			{Node: "n8", Role: ir.RoleUser, Time: t0.Add(time.Minute), Text: "Thanks. And the second file?"},
			{Node: "n9", Role: ir.RoleAgent, Time: t0.Add(time.Minute + time.Second), Text: "FIG-3."},
			{Node: "hopsesh/briefing", Role: ir.RoleUser, Text: "[hopsesh] This conversation was moved from Claude Code.", Generated: true},
			{Node: "hopsesh/import-ready", Role: ir.RoleAgent, Text: "[hopsesh] Import ready.", Generated: true},
		}}
}

// The transfer a write places is one OpenCode 2.3.0 imports as it is (its import schemas,
// ported in the stand-in, drop nothing): settled messages with ascending ids and times,
// replies and shell calls in OpenCode's shape, a turn's end marked idle, no model on the
// session, and the same bytes from the same request.
func TestWriterTransfer(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	_, h, _ := open(t, fh)
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	req := writeRequest(writtenSID)
	w, err := render(h, req, writtenSID, base)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := render(h, req, writtenSID, base)
	if string(again.body) != string(w.body) {
		t.Fatal("the same request must make the same session")
	}
	p, err := parsePlaced("x.jsonl", w.body)
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.transfer()
	if err != nil {
		t.Fatal(err)
	}
	if dropped, err := fakeopencode.CheckTransfer(b); err != nil || len(dropped) > 0 {
		t.Fatalf("OpenCode's import would refuse it (%v) or drop %q", err, dropped)
	}
	var tr struct {
		Info     map[string]json.RawMessage `json:"info"`
		Messages []map[string]json.RawMessage
	}
	if err := json.Unmarshal(b, &tr); err != nil {
		t.Fatal(err)
	}
	if _, ok := tr.Info["model"]; ok {
		t.Fatalf("the session names a model: %s", tr.Info["model"])
	}
	if _, ok := tr.Info["agent"]; ok {
		t.Fatalf("the session names an agent: %s", tr.Info["agent"])
	}
	var types []string
	var lastID string
	var lastTime int64
	for _, m := range tr.Messages {
		var id, typ string
		var tm msgTime
		_ = json.Unmarshal(m["id"], &id)
		_ = json.Unmarshal(m["type"], &typ)
		_ = json.Unmarshal(m["time"], &tm)
		if id <= lastID || tm.Created <= lastTime {
			t.Fatalf("ids and times must ascend: %s at %d after %s at %d", id, tm.Created, lastID, lastTime)
		}
		if o, ok := idOrder(id); !ok || o>>12 != uint64(tm.Created)&(1<<36-1) { // ms·4096 mod 2^48
			t.Fatalf("%s does not carry its time %d", id, tm.Created)
		}
		lastID, lastTime = id, tm.Created
		types = append(types, typ)
		if typ == "assistant" {
			var model modelRef
			_ = json.Unmarshal(m["model"], &model)
			if model.ProviderID != writtenProvider || model.ID != "claude-opus-4-5" || tm.Completed == 0 {
				t.Fatalf("a written reply: %v", m)
			}
		}
	}
	want := []string{"user", "assistant", "assistant", "assistant", "assistant", "assistant", "idle", "user", "assistant", "idle", "user", "assistant", "idle"}
	if !slices.Equal(types, want) {
		t.Fatalf("messages %q, want %q", types, want)
	}
	if tr.Info["time"] == nil || !strings.Contains(string(tr.Info["time"]), `"created":1793491200000`) {
		t.Fatalf("the session is made at the write's time: %s", tr.Info["time"])
	}
	// The shell calls, as OpenCode records them: a non-zero exit status also as a line the
	// model reads, and the folder a call ran in.
	var calls []item
	for _, m := range tr.Messages {
		var msg message
		raw, _ := json.Marshal(m)
		msg, _ = decodeMessage(raw)
		for _, it := range msg.items() {
			if it.Type == "tool" {
				calls = append(calls, it)
			}
		}
	}
	if len(calls) != 3 || calls[0].Name != "shell" || calls[0].State.Status != "completed" || string(calls[0].State.Input) != `{"command":"cat notes.txt"}` ||
		calls[0].Executed == nil || *calls[0].Executed || string(calls[0].State.Metadata["exit"]) != "0" || calls[0].State.Content[0].Text != "PLUM-7\n" ||
		len(calls[0].State.Content) != 1 ||
		calls[1].State.Status != "completed" || string(calls[1].State.Metadata["exit"]) != "2" ||
		string(calls[1].State.Input) != `{"command":"ls missing","workdir":"`+home+`/git/demo/sub"}` ||
		len(calls[1].State.Content) != 2 || calls[1].State.Content[0].Text != "ls: missing: No such file" || calls[1].State.Content[1].Text != "Exit code: 2" ||
		calls[2].State.Status != "error" || calls[2].State.Error == nil || calls[2].State.Error.Message != "The command failed." {
		t.Fatalf("shell calls: %+v", calls)
	}
}

// Real OpenCode 2.3.0 imports the session the writer makes from writeRequest and exports
// the same messages: the test bundle's export/written-by-hopsesh.json is its export of
// that transfer (written at 2026-11-01, imported with --directory, the folder scrubbed).
// A change to what the writer writes makes it again with a real OpenCode.
func TestWrittenAsExported(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	_, h, _ := open(t, fh)
	w, err := render(h, writeRequest(writtenSID), writtenSID, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	p, err := parsePlaced("x.jsonl", w.body)
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.transfer()
	if err != nil {
		t.Fatal(err)
	}
	exported, err := os.ReadFile(filepath.Join("..", "..", "testbundle", "opencode", "2.3.0", "export", "written-by-hopsesh.json"))
	if err != nil {
		t.Fatal(err)
	}
	type transfer struct {
		Info struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"info"`
		Messages []any `json:"messages"`
	}
	var ours, theirs transfer
	if err := json.Unmarshal(b, &ours); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(exported, &theirs); err != nil {
		t.Fatal(err)
	}
	if ours.Info != theirs.Info || !reflect.DeepEqual(ours.Messages, theirs.Messages) {
		t.Fatalf("OpenCode 2.3.0 exported another session than the writer writes:\n%s", b)
	}
}

// Times from a source whose clock is ahead (items after the write) stay before the write's
// own time, ascending, so the ids OpenCode makes for the next turn sort after the written
// ones.
func TestWriterTimesBeforeWrite(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	_, h, _ := open(t, fh)
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	req := writeRequest(writtenSID)
	ahead := base.Add(time.Hour).Sub(req.Items[0].Time)
	for i := range req.Items {
		if !req.Items[i].Time.IsZero() {
			req.Items[i].Time = req.Items[i].Time.Add(ahead)
		}
	}
	w, err := render(h, req, writtenSID, base)
	if err != nil {
		t.Fatal(err)
	}
	p, err := parsePlaced("x.jsonl", w.body)
	if err != nil {
		t.Fatal(err)
	}
	next := "msg_" + idTime(base.UnixMilli(), 1, false)
	last := ""
	for _, id := range p.ids() {
		if id <= last || id >= next {
			t.Fatalf("%s after %s, or not before %s", id, last, next)
		}
		last = id
	}
	if s, _, err := decodeJSONL("x.jsonl", w.body); err != nil || s.Updated != base.UnixMilli() {
		t.Fatalf("the session's update time: %d, %v", s.Updated, err)
	}
}

// A write without a folder, or with more than the context holds, places and imports
// nothing.
func TestWriteRefusals(t *testing.T) {
	fh, f := newMachine(t, setup{})
	m, h, in := open(t, fh)
	noDir := writeRequest(writtenSID)
	noDir.Header.CWD = " "
	big := writeRequest(writtenSID)
	big.Items = append(big.Items, ir.Item{Node: "big", Role: ir.RoleUser, Text: strings.Repeat("too much ", 1<<20)})
	for name, req := range map[string]ir.WriteRequest{"no folder": noDir, "too big": big} {
		if _, err := m.Write(ctx, h, in, req); err == nil {
			t.Fatalf("%s: written", name)
		}
		if _, ok := fh.Get(anchorOf(h, in, writtenSID)); ok || countCommands(f, "session import") != 0 {
			t.Fatalf("%s: placed or imported", name)
		}
	}
}

// Written and read back, the session is the request: each item a node (a shell call its
// call and result), the session's cursor its settled messages, every node's item in the
// projection, its title the request's (listed as one hopsesh gave), its last prompt the
// person's, and no model from hopsesh's own replies in its header.
func TestWriteReadsBack(t *testing.T) {
	fh, f := newMachine(t, setup{})
	m, h, in := open(t, fh)
	req := writeRequest(writtenSID)
	res, err := m.Write(ctx, h, in, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionID != writtenSID || res.Path != anchorOf(h, in, writtenSID) || res.From != 0 || res.To != 13 || res.Cursor.Offset != 13 {
		t.Fatalf("result: %+v", res)
	}
	imports := 0
	for _, c := range f.commands() {
		if strings.HasPrefix(c, "session import") {
			imports++
			if !strings.Contains(c, "--directory "+home+"/git/demo --standalone") {
				t.Fatalf("import: %s", c)
			}
		}
	}
	if imports != 1 {
		t.Fatalf("%d imports", imports)
	}
	s := listing(t, m, h, in)[writtenSID]
	// hopsesh's briefing is no one's prompt.
	if s.Title != "Find the codeword" || s.TitleSource != "custom" || s.CWD != home+"/git/demo" || s.Size != 13 || s.LastPrompt != "Thanks. And the second file?" {
		t.Fatalf("listed: %+v", s)
	}
	seg, err := m.Read(ctx, h, in, s, ir.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	if seg.Cursor != res.Cursor || seg.Header.Model != "" || seg.Header.Title != "Find the codeword" {
		t.Fatalf("read back: %+v", seg.Header)
	}
	var got []string
	for _, n := range seg.Nodes {
		switch {
		case n.Kind == ir.KindToolCall:
			got = append(got, "call "+n.Tool.Shell.Command)
		case n.Kind == ir.KindToolResult:
			got = append(got, "result "+string(n.Result.Status))
		default:
			got = append(got, string(n.Actor)+" "+n.Text)
		}
	}
	want := []string{"user What is the codeword in notes.txt?", "agent Let me look.", "call cat notes.txt", "result completed",
		"call ls missing", "result failed", "call false", "result failed", "agent The codeword is PLUM-7.",
		"user Thanks. And the second file?", "agent FIG-3.", "user [hopsesh] This conversation was moved from Claude Code.", "agent [hopsesh] Import ready."}
	if !slices.Equal(got, want) {
		t.Fatalf("nodes:\n%q\nwant\n%q", got, want)
	}
	if len(res.Projection) != len(seg.Nodes) {
		t.Fatalf("projection %d, nodes %d", len(res.Projection), len(seg.Nodes))
	}
	for i, p := range res.Projection {
		if p.Anchor != seg.Nodes[i].Native.Anchor || p.Hash != ir.ContentHash(seg.Nodes[i]) {
			t.Fatalf("projection %d: %+v", i, p)
		}
	}
	if !slices.Equal(res.Projection[0].Coverage, []ir.NodeID{"n1"}) || !res.Projection[len(res.Projection)-1].Generated || res.Projection[2].Fidelity != "native" {
		t.Fatalf("the projection carries the items' provenance: %+v", res.Projection)
	}
	// An append is refused: OpenCode cannot add to a session.
	if _, err := m.Write(ctx, h, in, ir.WriteRequest{Mode: ir.WriteAppend, SessionID: writtenSID, Expect: res.Cursor, Items: req.Items[:1]}); !errors.Is(err, agent.ErrUnsupported) {
		t.Fatalf("append: %v", err)
	}
}

// A write without an id in OpenCode's form gives the session one made from the operation,
// so a recovery finds it.
func TestWriteOwnID(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	m, h, in := open(t, fh)
	req := writeRequest("")
	res, err := m.Write(ctx, h, in, req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.SessionID, "ses_") || agent.SessionID(res.SessionID) != writtenID(req) {
		t.Fatalf("own id %s, from the operation %s", res.SessionID, writtenID(req))
	}
	req.SessionID = "0b6c1f7e-uuid"
	if writtenID(req) != agent.SessionID(res.SessionID) {
		t.Fatal("an id OpenCode does not take is replaced the same way")
	}
	rec, err := m.RecoverWrite(ctx, h, in, req)
	if err != nil || rec.SessionID != res.SessionID || rec.Cursor != res.Cursor {
		t.Fatalf("recovered %+v: %v", rec, err)
	}
	if writtenID(ir.WriteRequest{}) != "" {
		t.Fatal("no id without an operation")
	}
}

// A recovery imports what an interrupted write placed but had not imported, imports nothing
// when it had, writes the file when the write stopped before it, and refuses a session used
// since, or a placed file that is not the request's.
func TestRecoverWrite(t *testing.T) {
	fh, f := newMachine(t, setup{})
	m, h, in := open(t, fh)
	req := writeRequest(writtenSID)

	// Interrupted after the file was placed: the import runs.
	w, err := render(h, req, writtenSID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	fh.Put(anchorOf(h, in, writtenSID), w.body, timeZero)
	res, err := m.RecoverWrite(ctx, h, in, req)
	if err != nil || res.Cursor.Offset != 13 || len(res.Projection) != 13 {
		t.Fatalf("recovered %+v: %v", res, err)
	}
	if countCommands(f, "session import") != 1 {
		t.Fatal("the placed session was not imported")
	}
	// Again: nothing to import.
	again, err := m.RecoverWrite(ctx, h, in, req)
	if err != nil || again.Cursor != res.Cursor || countCommands(f, "session import") != 1 {
		t.Fatalf("again %+v: %v, %d imports", again, err, countCommands(f, "session import"))
	}
	// Used since: refused.
	if err := fh.ExecSQL(dbPath, `INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data) VALUES
		('msg_ffffffffffffUsedSinceThere', ?, 'user', 99, 1, 1, '{"time":{"created":1},"text":"more"}')`, writtenSID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RecoverWrite(ctx, h, in, req); !errors.Is(err, agent.ErrDiverged) {
		t.Fatalf("used since: %v", err)
	}

	// Interrupted before the file: written, then imported.
	other := writeRequest("ses_0123456789abHopseshWrite02")
	if _, err := m.RecoverWrite(ctx, h, in, other); err != nil {
		t.Fatal(err)
	}
	if _, ok := fh.Get(anchorOf(h, in, "ses_0123456789abHopseshWrite02")); !ok || countCommands(f, "session import") != 2 {
		t.Fatal("the session was not written")
	}
	// A placed file the request does not make: refused, nothing imported.
	third := writeRequest("ses_0123456789abHopseshWrite03")
	fh.Put(anchorOf(h, in, "ses_0123456789abHopseshWrite03"), []byte(strings.Replace(string(w.body), writtenSID, "ses_0123456789abHopseshWrite03", 1)), timeZero)
	third.Items = third.Items[:3]
	if _, err := m.RecoverWrite(ctx, h, in, third); !errors.Is(err, agent.ErrDiverged) || countCommands(f, "session import") != 2 {
		t.Fatalf("another file: %v", err)
	}
}

// A write changes nothing while OpenCode's service state is unknown, as every change.
func TestWriteNeedsServiceState(t *testing.T) {
	fh, f := newMachine(t, setup{})
	m, h, in := open(t, fh)
	f.onCall = func(argv []string) (agent.Result, bool) {
		return agent.Result{Code: 1, Stderr: []byte("boom")}, argv[0] == "service"
	}
	if _, err := m.Write(ctx, h, in, writeRequest(writtenSID)); err == nil || countCommands(f, "session import") != 0 {
		t.Fatalf("unknown service: %v", err)
	}
	if _, ok := fh.Get(anchorOf(h, in, writtenSID)); ok {
		t.Fatal("the session was placed")
	}
}

// With the service running, the import goes through it (no private server).
func TestWriteThroughRunningService(t *testing.T) {
	fh, f := newMachine(t, setup{})
	f.status = "http://127.0.0.1:49374"
	m, h, in := open(t, fh)
	if _, err := m.Write(ctx, h, in, writeRequest(writtenSID)); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.commands() {
		if strings.HasPrefix(c, "session import") && strings.Contains(c, "--standalone") {
			t.Fatalf("import on a private server: %s", c)
		}
	}
}

// The usage of a session hopsesh wrote counts its messages (hopsesh's replies count no
// tokens and name no model), and a new session's model is never one of hopsesh's.
func TestCapacityIgnoresWrittenReplies(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	m, h, in := open(t, fh)
	before, err := m.ContextCapacity(ctx, h, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := m.Write(ctx, h, in, writeRequest(writtenSID))
	if err != nil {
		t.Fatal(err)
	}
	after, err := m.ContextCapacity(ctx, h, in, nil)
	if err != nil || after.Model != before.Model || strings.Contains(after.Model, writtenProvider) {
		t.Fatalf("a new session's model %q, before the write %q: %v", after.Model, before.Model, err)
	}
	s := listing(t, m, h, in)[agent.SessionID(res.SessionID)]
	c, err := m.ContextCapacity(ctx, h, in, &s)
	if err != nil || c.Existing < 1000 || c.Model != "" {
		t.Fatalf("the written session's usage: %+v %v", c, err)
	}
}

// A written session continues back into another agent as the conversation it was: read,
// rendered and written again, it reads the same.
func TestWriteRoundTrip(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	m, h, in := open(t, fh)
	src := listing(t, m, h, in)[sesShell]
	seg, err := m.Read(ctx, h, in, src, ir.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	var items []ir.Item
	for _, n := range seg.Nodes {
		switch n.Kind {
		case ir.KindMessage:
			role := ir.RoleUser
			if n.Actor == ir.Agent {
				role = ir.RoleAgent
			}
			items = append(items, ir.Item{Node: n.ID, Role: role, Time: n.Time, Text: n.Text})
		case ir.KindToolCall:
			if n.Tool.Shell != nil {
				items = append(items, ir.Item{Node: n.ID, Role: ir.RoleAgent, Time: n.Time, Tool: &ir.ToolPair{Call: *n.Tool}})
			}
		case ir.KindToolResult:
			if k := len(items) - 1; k >= 0 && items[k].Tool != nil && items[k].Tool.Call.CallID == n.Result.CallID {
				items[k].Tool.Result = *n.Result
			}
		}
	}
	req := ir.WriteRequest{OperationID: "op-round", Mode: ir.WriteNew, SessionID: "ses_0123456789abHopseshRound1", Header: ir.Header{CWD: home + "/git/other", Title: src.Title}, Items: items}
	res, err := m.Write(ctx, h, in, req)
	if err != nil {
		t.Fatal(err)
	}
	back, err := m.Read(ctx, h, in, listing(t, m, h, in)[agent.SessionID(res.SessionID)], ir.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	// What a model reads (prompts, replies, shell calls and their output) comes back in
	// order; reasoning is not carried.
	view := func(seg ir.Segment) []string {
		var out []string
		for _, n := range seg.Nodes {
			switch {
			case n.Kind == ir.KindMessage:
				out = append(out, string(n.Actor)+": "+n.Text)
			case n.Kind == ir.KindToolCall && n.Tool.Shell != nil:
				out = append(out, "$ "+n.Tool.Shell.Command+" (in "+n.Tool.Shell.Dir+")")
			case n.Kind == ir.KindToolResult:
				out = append(out, "> "+n.Result.Output)
			}
		}
		return out
	}
	if a, b := view(seg), view(back); !slices.Equal(a, b) || len(a) < 8 {
		t.Fatalf("read back:\n%q\nfrom\n%q", b, a)
	}
}

// A write is imported by the running service whatever its version (it is never
// restarted for that), and refused when that service is older than the writer's message
// types (2.1.0).
func TestWriteChecksTheImportingService(t *testing.T) {
	fh, f := newMachine(t, setup{})
	f.status, f.version = serviceURL, "2.0.30"
	m, h, in := open(t, fh)
	req := ir.WriteRequest{Mode: ir.WriteNew, SessionID: "ses_e29a00000ffeHandMadeWrite0", Header: ir.Header{CWD: home + "/git/demo", Title: "t"},
		Items: []ir.Item{{Node: "n1", Role: ir.RoleUser, Text: "hello", Fidelity: "text"}}}
	if _, err := m.Write(ctx, h, in, req); !errors.Is(err, agent.ErrUnsupported) || !strings.Contains(err.Error(), "background service") {
		t.Fatalf("a 2.0 service imported: %v", err)
	}
	for _, c := range f.commands() {
		if strings.HasPrefix(c, "session import") {
			t.Fatalf("ran %s", c)
		}
	}
	if _, ok := fh.Get(anchorOf(fh, in, agent.SessionID(req.SessionID))); ok {
		t.Error("the transfer was written before the check")
	}
	// A recovery of an interrupted write is checked the same way.
	if _, err := m.RecoverWrite(ctx, h, in, req); !errors.Is(err, agent.ErrUnsupported) || !strings.Contains(err.Error(), "background service") {
		t.Fatalf("a 2.0 service imported a recovered write: %v", err)
	}
	for _, c := range f.commands() {
		if strings.HasPrefix(c, "session import") {
			t.Fatalf("ran %s", c)
		}
	}
	if _, ok := fh.Get(anchorOf(fh, in, agent.SessionID(req.SessionID))); ok {
		t.Error("the recovery placed the transfer before the check")
	}
}
