package codex

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

func TestCapacityConfigurationAndCompaction(t *testing.T) {
	h, in, sessions := setup(t)
	m := New()
	ctx := context.Background()
	if err := h.FS().WriteFile(in.Root(home)+"/config.toml", []byte("model_context_window = 12000\nprofile = 'small'\n[profiles.small]\nmodel_context_window = 8000\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := m.ContextCapacity(ctx, h, in, nil)
	if err != nil || c.Window != 8000 {
		t.Fatalf("profile capacity: %+v %v", c, err)
	}
	s := sessions[t1]
	raw, _ := h.FS().ReadFile(s.Path, 1<<20)
	var b strings.Builder
	b.WriteString(strings.ReplaceAll(string(raw), `"history_mode":"paginated"`, `"history_mode":"legacy"`))
	writeLine(&b, parseTime("2026-10-01T10:00:00Z"), "response_item", map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": strings.Repeat("x", 100000)}}})
	writeLine(&b, parseTime("2026-10-01T10:00:01Z"), "compacted", map[string]any{"replacement_history": []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "summary"}}}}})
	if err = h.FS().WriteFile(s.Path, []byte(b.String()), 0600); err != nil {
		t.Fatal(err)
	}
	c, err = m.ContextCapacity(ctx, h, in, &s)
	if err != nil || c.Unknown || c.Existing > 1000 {
		t.Fatalf("compacted archive counted as live: %+v %v", c, err)
	}
	writeLine(&b, parseTime("2026-10-01T10:00:02Z"), "compacted", map[string]any{"message": "opaque replacement unavailable"})
	if err = h.FS().WriteFile(s.Path, []byte(b.String()), 0600); err != nil {
		t.Fatal(err)
	}
	c, err = m.ContextCapacity(ctx, h, in, &s)
	if err != nil || !c.Unknown || c.Check([]ir.Item{{Text: "new"}}) == nil {
		t.Fatalf("unknown compaction must block append: %+v %v", c, err)
	}
}

func TestLargeFirstMessageRemainsListed(t *testing.T) {
	h, in, sessions := setup(t)
	s := sessions[t1]
	raw, _ := h.FS().ReadFile(s.Path, 1<<20)
	first := strings.ReplaceAll(strings.SplitN(string(raw), "\n", 2)[0], `"history_mode":"paginated"`, `"history_mode":"legacy"`)
	var b strings.Builder
	b.WriteString(first + "\n")
	for _, typ := range []string{"event_msg", "response_item"} {
		payload := map[string]any{"type": "user_message", "message": strings.Repeat("x", 300000)}
		if typ == "response_item" {
			payload = map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": strings.Repeat("x", 300000)}}}
		}
		writeLine(&b, parseTime("2026-10-01T10:00:00Z"), typ, payload)
	}
	if err := h.FS().WriteFile(s.Path, []byte(b.String()), 0600); err != nil {
		t.Fatal(err)
	}
	ls, err := New().List(context.Background(), h, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range ls.Sessions {
		if got.Key == s.Key {
			return
		}
	}
	t.Fatal("large first prompt disappeared")
}

func TestImportRejectsOversizeBeforeVendorExec(t *testing.T) {
	h, in, _ := setup(t)
	path := in.Root(home) + "/large-source.jsonl"
	_ = h.FS().WriteFile(path, []byte(strings.Repeat("x", 100000)), 0600)
	_, err := New().Import(context.Background(), h, in, agent.ID("claude"), path, "/home/u/git/demo", "large")
	if err == nil || !strings.Contains(err.Error(), "portable history") {
		t.Fatalf("unsafe import: %v", err)
	}
}

func TestImportPinsInputWithoutRedirectingVendorDiscovery(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged", true: "grew"}[changed], func(t *testing.T) {
			fh := agenttest.NewFakeHost("/home/alice")
			fh.AddBinary("codex", "codex-cli 0.153.2")
			m := New()
			in, err := m.Detect(context.Background(), fh)
			if err != nil {
				t.Fatal(err)
			}
			h := agent.Confine(fh, m.Spec(), in)
			path := "/home/alice/.claude/projects/demo/session.jsonl"
			fh.Put(path, []byte("original\n"), time.Now())
			fh.Programs["codex"] = func(_ []string, o agent.RunOptions) agent.Result {
				if !strings.Contains(string(o.Stdin), path) {
					t.Fatal("vendor received an undetectable snapshot path")
				}
				if changed {
					fh.Put(path, []byte("original\nnew work\n"), time.Now())
				}
				return agent.Result{Stdout: []byte(`{"method":"externalAgentConfig/import/completed","params":{"itemTypeResults":[{"successes":[{"target":"imported"}]}]}}` + "\n")}
			}
			id, err := m.Import(context.Background(), h, in, "claude", path, "/home/alice/git/demo", "fictional")
			if id != "imported" || changed != (err != nil) {
				t.Fatalf("id %s err %v", id, err)
			}
			found := false
			for _, written := range fh.Writes {
				if strings.Contains(written, "/hopsesh/imports/") {
					raw, _ := fh.Get(strings.TrimPrefix(written, "write "))
					if string(raw) == "original\n" {
						found = true
					}
				}
			}
			if !found {
				t.Fatal("checked input snapshot missing")
			}
		})
	}
}

// No native counter or duplicate event may be mistaken for authoritative token usage.
func TestCapacityIgnoresUIEventDuplicates(t *testing.T) {
	h, in, sessions := setup(t)
	s := sessions[t1]
	raw, _ := h.FS().ReadFile(s.Path, 1<<20)
	first := strings.ReplaceAll(strings.SplitN(string(raw), "\n", 2)[0], `"history_mode":"paginated"`, `"history_mode":"legacy"`)
	event, _ := json.Marshal(map[string]any{"type": "event_msg", "payload": map[string]any{"type": "user_message", "message": strings.Repeat("x", 100000)}})
	_ = h.FS().WriteFile(s.Path, []byte(first+"\n"+string(event)+"\n"), 0600)
	c, err := New().ContextCapacity(context.Background(), h, in, &s)
	if err != nil || c.Existing != 0 {
		t.Fatalf("UI event counted: %+v %v", c, err)
	}
}

func TestContextErrorIsAnAgentEventNotQuotedHistory(t *testing.T) {
	h, in, sessions := setup(t)
	s := sessions[t1]
	m := New()
	ctx := context.Background()
	appendEvent := func(typ string, payload any) {
		t.Helper()
		var b strings.Builder
		writeLine(&b, parseTime("2026-10-01T10:00:00Z"), typ, payload)
		if err := h.FS().Append(s.Path, []byte(b.String()), agent.AppendOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	overflow := func() bool {
		t.Helper()
		ls, err := m.List(ctx, h, in)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range ls.Sessions {
			if v.Key == s.Key {
				return v.ContextOverflow
			}
		}
		t.Fatal("missing session")
		return false
	}
	appendEvent("event_msg", map[string]any{"type": "user_message", "message": "context_window_exceeded"})
	if overflow() {
		t.Fatal("quoted message treated as agent failure")
	}
	appendEvent("event_msg", map[string]any{"type": "error", "message": "context_window_exceeded"})
	if !overflow() {
		t.Fatal("agent context error wasn't surfaced")
	}
	appendEvent("event_msg", map[string]any{"type": "task_complete"})
	if overflow() {
		t.Fatal("successful completion didn't clear old failure")
	}
}
