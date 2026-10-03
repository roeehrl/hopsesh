package claude

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/ir"
)

func TestReadActiveBranch(t *testing.T) {
	_, h, in, byID := setup(t)
	seg, err := New().Read(context.Background(), h, in, byID[s1], ir.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, n := range seg.Nodes {
		kinds = append(kinds, string(n.Kind))
	}
	want := "message reasoning tool_call tool_result message"
	if strings.Join(kinds, " ") != want {
		t.Fatalf("kinds %v, want %s", kinds, want)
	}
	call := seg.Nodes[2].Tool
	if call.Kind != ir.ToolExecute || call.Shell.Command != "cat /home/u/git/demo/notes.txt" || call.CallID != "toolu_1" {
		t.Fatalf("tool call %+v", call)
	}
	if r := seg.Nodes[3].Result; r.CallID != "toolu_1" || r.Output != "The codeword is PLUM-7." {
		t.Fatalf("result %+v", r)
	}
	if seg.Header.Model != "claude-sonnet-5" || seg.Header.Version != "2.1.284" || seg.Header.Title != "Find the codeword" {
		t.Fatalf("header %+v", seg.Header)
	}
}

func TestReadIncrementalAndDiverged(t *testing.T) {
	fh, h, in, byID := setup(t)
	m := New()
	s := byID[s1]
	first, _ := m.Read(context.Background(), h, in, s, ir.Cursor{})
	// A new turn on the same branch, chained to the last assistant record; Claude Code
	// resumes from the leaf its newest last-prompt record names.
	more := `{"parentUuid":"a2","isSidechain":false,"type":"user","message":{"role":"user","content":"and now?"},"uuid":"u3","timestamp":"2026-10-01T11:00:00.000Z","sessionId":"` + s1 + `","cwd":"/home/u/git/demo"}` + "\n" +
		`{"type":"last-prompt","lastPrompt":"and now?","leafUuid":"u3","sessionId":"` + s1 + `"}` + "\n"
	b, _ := fh.Get(s.Path)
	fh.Put(s.Path, append(b, more...), time.Now())
	rest, err := m.Read(context.Background(), h, in, s, first.Cursor)
	if err != nil || len(rest.Nodes) != 1 || rest.Nodes[0].Text != "and now?" || rest.Nodes[0].Parent != first.Cursor.Head {
		t.Fatalf("incremental read: %+v %v", rest.Nodes, err)
	}
	// Rewind: the user went back and branched from the first message.
	fork := `{"parentUuid":"u1","isSidechain":false,"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"other answer"}]},"uuid":"x1","timestamp":"2026-10-01T12:00:00.000Z","sessionId":"` + s1 + `","cwd":"/home/u/git/demo"}` + "\n" +
		`{"type":"last-prompt","lastPrompt":"x","leafUuid":"x1","sessionId":"` + s1 + `"}` + "\n"
	b, _ = fh.Get(s.Path)
	fh.Put(s.Path, append(b, fork...), time.Now())
	if _, err := m.Read(context.Background(), h, in, s, first.Cursor); err == nil {
		t.Fatal("a branch that no longer contains the head must be reported as diverged")
	}
}
