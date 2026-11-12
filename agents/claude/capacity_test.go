package claude

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func TestCapacityNativeUsageAndOriginalAppend(t *testing.T) {
	fh := agenttest.NewFakeHost("/home/alice")
	in := agent.Install{Agent: id, Version: "2.1.288", Roots: map[string]string{"home": "/home/alice/.claude"}}
	h := agent.Confine(fh, New().Spec(), in)
	path := in.Root(home) + "/projects/-home-alice-git-demo/original.jsonl"
	var data []byte
	add := func(uuid, parent, role, model, text string, usage any, compact bool) {
		r := map[string]any{"type": role, "uuid": uuid, "parentUuid": parent, "sessionId": "original", "cwd": "/home/alice/git/demo", "isCompactSummary": compact,
			"message": map[string]any{"role": role, "model": model, "content": text, "usage": usage}}
		b, _ := json.Marshal(r)
		data = append(data, append(b, '\n')...)
	}
	add("seed", "", "user", "", strings.Repeat("large old history ", 100000), nil, false)
	usage := map[string]int{"input_tokens": 2, "cache_read_input_tokens": 423033, "cache_creation_input_tokens": 297, "output_tokens": 440}
	add("request", "seed", "assistant", "claude-opus-5-5", "reply", usage, false)
	add("stream", "request", "assistant", "claude-opus-5-5", "reply block", usage, false)
	add("next", "stream", "user", "", "new prompt", nil, false)
	fh.Put(path, data, time.Now())
	s := agent.Summary{Key: agent.SessionKey{Agent: id, Session: "original"}, Path: path, CWD: "/home/alice/git/demo"}
	c, err := New().ContextCapacity(context.Background(), h, in, &s)
	if err != nil || c.Window != 1_000_000 || c.Existing != 423772+len(`"new prompt"`)+32 {
		t.Fatalf("usage/cache/repeated blocks: %+v %v", c, err)
	}
	seg, err := New().Read(context.Background(), h, in, s, ir.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	// Exceeds the new-session 64K fallback budget but fits this actual original.
	before := append([]byte(nil), data...)
	w, err := New().Write(context.Background(), h, in, ir.WriteRequest{Mode: ir.WriteAppend, SessionID: "original", Expect: seg.Cursor, Header: ir.Header{CWD: s.CWD}, Items: []ir.Item{{Role: ir.RoleUser, Text: strings.Repeat("return work ", 2500)}}})
	if err != nil || w.SessionID != "original" {
		t.Fatalf("original append: %+v %v", w, err)
	}
	after, _ := fh.Get(path)
	if string(after[:len(before)]) != string(before) {
		t.Fatal("existing records changed")
	}
	for _, tc := range []struct {
		key, value string
		window     int
	}{{"CLAUDE_CODE_DISABLE_1M_CONTEXT", "1", 200_000}, {"CLAUDE_CODE_AUTO_COMPACT_WINDOW", "150000", 150_000}} {
		fh.SetEnv(tc.key, tc.value)
		capped, err := New().ContextCapacity(context.Background(), h, in, &s)
		if err != nil || capped.Window != tc.window || capped.Allowance() != 0 || capped.ConfigHash == c.ConfigHash {
			t.Fatalf("lower cap ignored: %+v %v", capped, err)
		}
		fh.SetEnv(tc.key, "")
	}
	// A per-model project override must participate in capacity and its fingerprint.
	project := s.CWD + "/.claude/settings.local.json"
	fh.Put(project, []byte(`{"modelSettings":{"claude-opus-5-5":{"autoCompactWindow":120000}}}`), time.Now())
	capped, err := New().ContextCapacity(context.Background(), h, in, &s)
	if err != nil || capped.Window != 120000 || capped.Allowance() != 0 {
		t.Fatalf("project/model limit ignored: %+v %v", capped, err)
	}
	fh.Put(project, []byte(`{}`), time.Now())
	// A compact summary discards the old token baseline, not the archived history.
	add("summary", "next", "user", "", "short summary", nil, true)
	fh.Put(path, data, time.Now())
	c, err = New().ContextCapacity(context.Background(), h, in, &s)
	if err != nil || c.Existing != len(`"short summary"`)+32 {
		t.Fatalf("compaction: %+v %v", c, err)
	}
	// A configured unknown model must never inherit the original model's 1M limit.
	fh.Put(in.Root(home)+"/settings.json", []byte(`{"model":"custom-model"}`), time.Now())
	c, err = New().ContextCapacity(context.Background(), h, in, &s)
	if err != nil || c.Window != ir.FallbackWindow {
		t.Fatalf("unknown model: %+v %v", c, err)
	}
}
