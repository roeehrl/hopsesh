package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Matrix turns must extend the vendor's active branch, including a last-prompt
// checkpoint. A parentless fixture would simulate a rewind instead of new work.
func TestAppendTurnExtendsClaudeActiveBranch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	cwd := filepath.Join(dir, "repo")
	p, err := claudeSession(SeedReq{ID: "matrix-session", Text: "original sentinel", Title: "matrix"}, cwd)
	if err != nil {
		t.Fatal(err)
	}
	m := claude.New()
	in := agent.Install{Agent: "claude", Roots: map[string]string{"home": dir}, Present: true}
	machine := &host.Machine{Local: true, Facts: host.Facts{OS: runtime.GOOS, Home: dir}}
	h, err := machine.For(context.Background(), m.Spec(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := agent.Summary{Key: agent.SessionKey{Agent: "claude", Session: "matrix-session"}, Path: p, CWD: cwd}
	before, err := m.Read(context.Background(), h, in, s, ir.Cursor{})
	if err != nil || len(before.Nodes) == 0 {
		t.Fatal(err)
	}
	// Vendor writers leave this metadata checkpoint at the active leaf.
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("{\"type\":\"last-prompt\",\"leafUuid\":\"a1\"}\n")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"first added work", "second added work"} {
		if err := appendTurn(AppendReq{Agent: "claude", Path: p, ID: "matrix-session", Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	after, err := m.Read(context.Background(), h, in, s, before.Cursor)
	if err != nil || len(after.Nodes) != 2 || after.Nodes[0].Text != "first added work" || after.Nodes[1].Text != "second added work" {
		t.Fatalf("lost prior history: %+v %v", after, err)
	}
}
