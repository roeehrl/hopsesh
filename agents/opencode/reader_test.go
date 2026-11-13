package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// noSQL is a machine whose database hopsesh cannot read.
type noSQL struct{ agent.Host }

// placed is a machine where nothing runs and no database is read (a peer's package).
type placed struct{ *agenttest.FakeHost }

func (placed) SQL() agent.SQL   { return nil }
func (placed) Exec() agent.Exec { return noExec{} }

type noExec struct{}

func (noExec) Run(context.Context, []string, agent.RunOptions) (agent.Result, error) {
	return agent.Result{}, fmt.Errorf("%w: nothing runs here", agent.ErrUnsupported)
}

func readAll(t *testing.T, m *Module, h agent.Host, in agent.Install, s agent.Summary) ir.Segment {
	t.Helper()
	seg, err := m.Read(ctx, h, in, s, ir.Cursor{})
	if err != nil {
		t.Fatalf("Read %s: %v", s.Key.Session, err)
	}
	return seg
}

func sameNodes(t *testing.T, what string, a, b ir.Segment) {
	t.Helper()
	if len(a.Nodes) != len(b.Nodes) || a.Cursor != b.Cursor {
		t.Fatalf("%s: %d nodes to %+v, %d to %+v", what, len(a.Nodes), a.Cursor, len(b.Nodes), b.Cursor)
	}
	for i := range a.Nodes {
		if a.Nodes[i].ID != b.Nodes[i].ID || a.Nodes[i].Text != b.Nodes[i].Text || a.Nodes[i].Native.Anchor != b.Nodes[i].Native.Anchor {
			t.Fatalf("%s: node %d differs: %q (%s), %q (%s)", what, i, a.Nodes[i].Text, a.Nodes[i].Native.Anchor, b.Nodes[i].Text, b.Nodes[i].Native.Anchor)
		}
	}
}

// The session's file (JSON lines), OpenCode's export and the database read the same.
func TestReaderModesAgree(t *testing.T) {
	fh, _ := newMachine(t, setup{synthetic: true})
	m, h, in := open(t, fh)
	l, err := m.List(ctx, h, in)
	if err != nil {
		t.Fatal(err)
	}
	idx := exportIndex(t)
	for _, s := range l.Sessions {
		fromDB := readAll(t, m, h, in, s)
		b, err := m.Bundle(ctx, h, in, s)
		if err != nil {
			t.Fatal(err)
		}
		main, _ := b.Main()
		data, err := m.Materialize(ctx, h, in, s, main)
		if err != nil {
			t.Fatal(err)
		}
		fh.Put(s.Path, data, time.Now())
		fromFile := readAll(t, m, agent.Confine(placed{fh}, m.Spec(), in), in, s)
		sameNodes(t, "file "+string(s.Key.Session), fromDB, fromFile)
		if fromFile.Header != fromDB.Header {
			t.Errorf("headers differ: %+v, %+v", fromFile.Header, fromDB.Header)
		}
		if file, ok := idx[string(s.Key.Session)]; ok {
			raw, err := os.ReadFile(filepath.Join(fixture, file))
			if err != nil {
				t.Fatal(err)
			}
			sess, msgs, err := fromTransfer(raw)
			if err != nil {
				t.Fatal(err)
			}
			fromExport, err := segment(sess, msgs, ir.Cursor{})
			if err != nil {
				t.Fatal(err)
			}
			sameNodes(t, "export "+file, fromDB, fromExport)
			if s.Size != fromExport.Cursor.Offset {
				t.Errorf("%s: listed size %d, export has %d settled messages", s.Key.Session, s.Size, fromExport.Cursor.Offset)
			}
		}
	}
}

// What hopsesh makes from the database is what OpenCode's own export writes: the same
// messages, and the same info but for what the export cannot have.
func TestMaterializeMatchesExport(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	m, h, in := open(t, fh)
	for sid, file := range exportIndex(t) {
		raw, err := os.ReadFile(filepath.Join(fixture, file))
		if err != nil {
			t.Fatal(err)
		}
		var want struct {
			Info     map[string]any `json:"info"`
			Messages []any          `json:"messages"`
		}
		if err := json.Unmarshal(raw, &want); err != nil {
			t.Fatal(err)
		}
		f := agent.BundleFile{Root: rootData, Rel: agent.AnchorRel(id, agent.SessionID(sid)), Role: agent.RoleMain, Virtual: true}
		data, err := m.Materialize(ctx, h, in, agent.Summary{Key: sessionKey(in, sid)}, f)
		if err != nil {
			t.Fatalf("%s: %v", sid, err)
		}
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		var got map[string]any
		if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
			t.Fatal(err)
		}
		if got["kind"] != "info" || got["version"] != "2.3.0" {
			t.Errorf("%s: info line %v", file, lines[0])
		}
		delete(got, "kind")
		delete(got, "version")
		if !reflect.DeepEqual(got, want.Info) {
			t.Errorf("%s: info\n got %v\nwant %v", file, got, want.Info)
		}
		if len(lines)-1 != len(want.Messages) {
			t.Fatalf("%s: %d messages, export has %d", file, len(lines)-1, len(want.Messages))
		}
		for i, line := range lines[1:] {
			var msg any
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(msg, want.Messages[i]) {
				t.Errorf("%s: message %d differs from the export", file, i)
			}
		}
	}
}

func nodesOf(seg ir.Segment, anchorPrefix string) []ir.Node {
	var out []ir.Node
	for _, n := range seg.Nodes {
		if strings.HasPrefix(n.Native.Anchor, anchorPrefix) {
			out = append(out, n)
		}
	}
	return out
}

// An assistant whose content is partly in session_part rows reads parts first, then the
// content still inline (F5); unsettled messages are not read; nor is a type the module
// does not know.
func TestReaderMergesPartsAndSkipsUnsettled(t *testing.T) {
	fh, _ := newMachine(t, setup{synthetic: true})
	m, h, in := open(t, fh)
	seg := readAll(t, m, h, in, agent.Summary{Key: sessionKey(in, sesMixed)})
	if seg.Cursor.Offset != 6 {
		t.Errorf("cursor offset %d, want 6 settled messages", seg.Cursor.Offset)
	}
	msg2 := nodesOf(seg, "msg_1d65e0000002SyntheticMsg02/")
	var kinds []string
	for _, n := range msg2 {
		kinds = append(kinds, string(n.Kind)+":"+n.Native.Anchor[strings.LastIndex(n.Native.Anchor, "Msg02/")+6:])
	}
	want := []string{"reasoning:0", "tool_call:1", "tool_result:1/result", "message:2", "message:3"}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("merged order %v, want %v", kinds, want)
	}
	if msg2[3].Text != "Text from the part rows." || msg2[4].Text != "Inline text after the part rows." {
		t.Errorf("texts %q, %q", msg2[3].Text, msg2[4].Text)
	}
	shell := msg2[1].Tool
	if shell.Kind != ir.ToolExecute || shell.Shell.Command != "go build ./..." || shell.Shell.Dir != "/home/u/git/demo" {
		t.Errorf("shell call %+v", shell)
	}
	if r := msg2[2].Result; r.Status != ir.StatusFailed || r.ExitCode == nil || *r.ExitCode != 1 || !strings.Contains(r.Output, "undefined: fmt") {
		t.Errorf("shell result %+v", r)
	}
	for _, n := range seg.Nodes {
		if strings.Contains(n.Text, "Still thinking") || strings.Contains(n.Text, "newer than the module") {
			t.Errorf("read an unsettled or unknown message: %q", n.Text)
		}
	}
}

func TestReaderMapsMessages(t *testing.T) {
	fh, _ := newMachine(t, setup{synthetic: true})
	m, h, in := open(t, fh)
	seg := readAll(t, m, h, in, agent.Summary{Key: sessionKey(in, sesMixed)})
	if seg.Header.Agent != "opencode" || seg.Header.CWD != "/home/u/git/demo" || seg.Header.Model != "openai/gpt-5.5-pro" || seg.Header.Title != "" || seg.Header.Version != "2.3.0" {
		t.Errorf("header %+v", seg.Header)
	}
	user := nodesOf(seg, "msg_1d65e0000001SyntheticMsg01")
	if len(user) != 2 || user[0].Actor != ir.User || user[0].Text != "Look at the screenshot and fix the build" ||
		user[1].Kind != ir.KindAttachment || user[1].Attachment.Name != "shot.png" || user[1].Attachment.MIME != "image/png" || len(user[1].Attachment.Data) != 0 {
		t.Fatalf("user prompt and attachment: %+v", user)
	}
	if skill := nodesOf(seg, "msg_1d65e0000003"); len(skill) != 1 || skill[0].Text != "[used the skill review]" {
		t.Errorf("skill note %+v", skill)
	}
	tools := nodesOf(seg, "msg_1d65e0000004SyntheticMsg04/")
	if tools[0].Tool.Kind != ir.ToolFetch || tools[0].Tool.URL != "https://example.com/docs" || tools[1].Result.Status != ir.StatusFailed || tools[1].Result.Output != "fetch failed" {
		t.Errorf("failed webfetch %+v %+v", tools[0].Tool, tools[1].Result)
	}
	if tools[2].Tool.Name != "execute" || tools[2].Tool.Kind != ir.ToolOther || tools[3].Result.Output != "2" {
		t.Errorf("Code Mode call %+v", tools[2].Tool)
	}
	shell := nodesOf(seg, "msg_1d65e0000005")
	if len(shell) != 2 || shell[0].Actor != ir.User || shell[0].Tool.Shell.Command != "go build ./..." || shell[1].Result.Status != ir.StatusCompleted {
		t.Errorf("the person's shell command %+v", shell)
	}
}

func TestReaderMapsCapturedTools(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	m, h, in := open(t, fh)
	byName := map[string]*ir.ToolCall{}
	var results []*ir.ToolResult
	var compactions, notes int
	for _, sid := range []string{sesFiles, sesSubagent, sesShell, sesLong} {
		for _, n := range readAll(t, m, h, in, agent.Summary{Key: sessionKey(in, sid)}).Nodes {
			switch {
			case n.Tool != nil:
				if prev := byName[n.Tool.Name]; prev == nil || n.Tool.Name != "read" || n.Tool.Path == "main.go" {
					byName[n.Tool.Name] = n.Tool
				}
			case n.Result != nil:
				results = append(results, n.Result)
			case n.Kind == ir.KindCompaction:
				compactions++
			case strings.HasPrefix(n.Text, "[OpenCode moved the session to /home/u/git/other]"):
				notes++
			case n.Kind == ir.KindMessage && (strings.Contains(n.Text, "<fork-notice>") || strings.Contains(n.Text, "The environment you are running in")):
				t.Errorf("read OpenCode's own bookkeeping: %q", n.Text)
			}
		}
	}
	if c := byName["read"]; c == nil || c.Kind != ir.ToolRead || c.Path != "main.go" {
		t.Errorf("read %+v", c)
	}
	if c := byName["edit"]; c == nil || c.Edit == nil || c.Edit.Path != "main.go" || !strings.Contains(c.Edit.New, "hi, ") {
		t.Errorf("edit %+v", c)
	}
	if c := byName["write"]; c == nil || c.Write == nil || c.Write.Path != "farewell.txt" || c.Write.Content != "bye" {
		t.Errorf("write %+v", c)
	}
	if c := byName["glob"]; c == nil || c.Kind != ir.ToolSearch || c.Search.Pattern != "**/*.go" {
		t.Errorf("glob %+v", c)
	}
	if c := byName["subagent"]; c == nil || c.Kind != ir.ToolSubagent || c.Subagent.Description != "Reply pong" {
		t.Errorf("subagent %+v", c)
	}
	failed := 0
	for _, r := range results {
		if r.Status == ir.StatusFailed && r.ExitCode != nil && *r.ExitCode == 1 {
			failed++
		}
	}
	if failed != 1 || compactions != 1 || notes != 1 {
		t.Errorf("failed shell results %d, compactions %d, folder notes %d", failed, compactions, notes)
	}
}

func TestReaderCursors(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	m, h, in := open(t, fh)
	s := agent.Summary{Key: sessionKey(in, sesLong)}
	all := readAll(t, m, h, in, s)
	// From the end: nothing new.
	if rest, err := m.Read(ctx, h, in, s, all.Cursor); err != nil || len(rest.Nodes) != 0 {
		t.Fatalf("from the end: %d nodes, %v", len(rest.Nodes), err)
	}
	// From the middle: what follows those messages.
	_, msgs, err := mustSQL(t, h, in).transcript(ctx, s.Key.Session)
	if err != nil {
		t.Fatal(err)
	}
	half, err := segment(session{ID: sesLong}, msgs[:10], ir.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	rest, err := m.Read(ctx, h, in, s, half.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(half.Nodes)+len(rest.Nodes) != len(all.Nodes) || rest.Nodes[0].Parent != half.Cursor.Head {
		t.Fatalf("halves %d + %d of %d nodes", len(half.Nodes), len(rest.Nodes), len(all.Nodes))
	}
	// A cursor that names only its node.
	if r, err := m.Read(ctx, h, in, s, ir.Cursor{Head: half.Cursor.Head}); err != nil || len(r.Nodes) != len(rest.Nodes) {
		t.Fatalf("head-only cursor: %d nodes, %v", len(r.Nodes), err)
	}
	// A head that is not there, or more messages than the session has: diverged.
	for _, c := range []ir.Cursor{{Offset: half.Cursor.Offset, Head: "nope"}, {Offset: all.Cursor.Offset + 1, Head: all.Cursor.Head}, {Head: "nope"}} {
		if _, err := m.Read(ctx, h, in, s, c); !errors.Is(err, agent.ErrDiverged) {
			t.Errorf("cursor %+v: %v", c, err)
		}
	}
}

// A revert that drops messages OpenCode wrote after a read makes the old cursor diverge.
func TestReaderDivergesAfterRevert(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	m, h, in := open(t, fh)
	s := agent.Summary{Key: sessionKey(in, sesFiles)}
	before := readAll(t, m, h, in, s)
	if err := fh.ExecSQL(dbPath, "DELETE FROM session_message WHERE session_id = ? AND seq = (SELECT max(seq) FROM session_message WHERE session_id = ? AND type = 'assistant')", sesFiles, sesFiles); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Read(ctx, h, in, s, before.Cursor); !errors.Is(err, agent.ErrDiverged) {
		t.Fatalf("after a revert: %v", err)
	}
}

func TestReaderAnchorsAreUnique(t *testing.T) {
	fh, _ := newMachine(t, setup{synthetic: true})
	m, h, in := open(t, fh)
	l, _ := m.List(ctx, h, in)
	for _, s := range l.Sessions {
		seen := map[string]bool{}
		for _, n := range readAll(t, m, h, in, s).Nodes {
			if n.Native == nil || n.Native.Format != nativeFormat || !strings.HasPrefix(n.Native.Anchor, "msg_") || seen[n.Native.Anchor] {
				t.Fatalf("%s: anchor %+v", s.Key.Session, n.Native)
			}
			seen[n.Native.Anchor] = true
		}
	}
}

func TestReaderMissingSession(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	m, h, in := open(t, fh)
	if _, err := m.Read(ctx, h, in, agent.Summary{Key: sessionKey(in, "ses_missing")}, ir.Cursor{}); !errors.Is(err, agent.ErrNotFound) {
		t.Fatalf("a missing session: %v", err)
	}
}

func mustSQL(t *testing.T, h agent.Host, in agent.Install) sqlReader {
	t.Helper()
	r, ok := newSQLReader(h, in)
	if !ok {
		t.Fatal("no SQL")
	}
	return r
}

func TestSanitizedProjection(t *testing.T) {
	fh, _ := newMachine(t, setup{synthetic: true})
	m, h, in := open(t, fh)
	src := readAll(t, m, h, in, agent.Summary{Key: sessionKey(in, sesMixed)})
	var kept []ir.Node
	for _, n := range src.Nodes {
		if n.Kind != ir.KindReasoning {
			kept = append(kept, n)
		}
	}
	ps, err := m.ProjectSanitized(src, ir.Segment{Nodes: kept})
	if err != nil || len(ps) != len(kept) {
		t.Fatalf("%d projections, %v", len(ps), err)
	}
	if _, err := m.ProjectSanitized(src, ir.Segment{Nodes: kept[1:]}); !errors.Is(err, agent.ErrDiverged) {
		t.Fatalf("a changed shape: %v", err)
	}
}

func TestCanonicalIsStable(t *testing.T) {
	a := canonical(json.RawMessage(`{"b":1,"a":{"y":1794597439545,"x":"t"}}`))
	b := canonical(json.RawMessage(`{"a":{"x":"t","y":1794597439545},"b":1}`))
	if string(a) != string(b) || !strings.Contains(string(a), "1794597439545") {
		t.Fatalf("%s vs %s", a, b)
	}
}
