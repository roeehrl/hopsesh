package codex

import (
	"context"
	"encoding/json"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func TestNativeForkUsesDeclaredParentAndVerifiedRecords(t *testing.T) {
	ctx := context.Background()
	fh := newHost(t)
	m := New()
	in, _ := m.Detect(ctx, fh)
	h := agent.Confine(fh, m.Spec(), in)
	listing, _ := m.List(ctx, h, in)
	var parent agent.Summary
	for _, s := range listing.Sessions {
		if string(s.Key.Session) == t1 {
			parent = s
		}
	}
	raw, _ := fh.FS().ReadFile(parent.Path, 1<<20)
	lines := strings.Split(string(raw), "\n")
	var first map[string]any
	json.Unmarshal([]byte(lines[0]), &first)
	meta := first["payload"].(map[string]any)
	meta["history_mode"] = "legacy"
	meta["id"] = t3
	meta["forked_from_id"] = t1
	body, _ := json.Marshal(first)
	lines[0] = string(body)
	child := agent.Summary{Key: agent.SessionKey{Agent: "codex", Session: t3}, Path: path.Join(path.Dir(parent.Path), "rollout-2026-10-01T13-00-00-"+t3+".jsonl")}
	fh.Put(child.Path, []byte(strings.Join(lines, "\n")), time.Now())
	proof, err := m.VerifyNativeFork(ctx, h, in, parent, child)
	if err != nil || len(proof) == 0 {
		t.Fatalf("proof %v %v", proof, err)
	}
	ps, _ := m.Read(ctx, h, in, parent, ir.Cursor{})
	if len(proof) != len(ps.Nodes) {
		t.Fatalf("inherited %d of %d", len(proof), len(ps.Nodes))
	}
	meta["forked_from_id"] = "unrelated"
	body, _ = json.Marshal(first)
	lines[0] = string(body)
	fh.Put(child.Path, []byte(strings.Join(lines, "\n")), time.Now())
	if _, err = m.VerifyNativeFork(ctx, h, in, parent, child); err == nil {
		t.Fatal("matching text without declared ancestry must not link")
	}
}
