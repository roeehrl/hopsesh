package codex

import (
	"testing"
	"time"
)

func TestObservedWindowReadsCodexTokenCount(t *testing.T) {
	h := newHost(t)
	p := "/home/u/.codex/sessions/2026/10/10/rollout.jsonl"
	h.Put(p, []byte(`{"type":"session_meta"}`+"\n"+`{"type":"event_msg","payload":{"type":"token_count","info":{"model_context_window":200000}}}`+"\n"+
		`{"type":"event_msg","payload":{"type":"token_count","info":{"model_context_window":258400}}}`+"\n"), time.Now())
	if w := observedWindow(h.FS(), p); w != 258400 {
		t.Fatalf("got %d, want the last reported window", w)
	}
	if w := observedWindow(h.FS(), "/home/u/.codex/sessions/missing.jsonl"); w != 0 {
		t.Fatal("a missing rollout reported a window")
	}
}
