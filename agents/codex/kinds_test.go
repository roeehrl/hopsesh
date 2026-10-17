package codex

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// The Codex app's voice conversations and Work threads are marked so hopsesh can hide
// them unless the user shows them.
func TestListMarksVoiceAndWorkThreads(t *testing.T) {
	fh := newHost(t)
	put := func(id, originator, source string) {
		meta := `{"timestamp":"2026-10-02T06:00:00.000Z","type":"session_meta","payload":{"id":"` + id + `","timestamp":"2026-10-02T06:00:00.000Z","cwd":"/home/u/git/demo","originator":"` + originator + `","cli_version":"0.162.0-alpha.17.2","source":"vscode","thread_source":"` + source + `","model_provider":"openai"}}`
		msg := `{"timestamp":"2026-10-02T06:00:01.000Z","type":"event_msg","payload":{"type":"user_message","message":"hello there","images":[]}}`
		fh.Put("/home/u/.codex/sessions/2026/10/02/rollout-2026-10-02T09-00-00-"+id+".jsonl", []byte(meta+"\n"+msg+"\n"), time.Now())
	}
	put("01a0fe1c-0000-7000-8000-0000000000a1", "Codex Desktop", "realtime_voice")
	put("01a0fe1c-0000-7000-8000-0000000000a2", "codex_work_desktop", "user")
	put("01a0fe1c-0000-7000-8000-0000000000a3", "codex_work_desktop", "realtime_voice")
	put("01a0fe1c-0000-7000-8000-0000000000a4", "Codex Desktop", "user")
	m := New()
	in, _ := m.Detect(context.Background(), fh)
	l, err := m.List(context.Background(), fh, in)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, s := range l.Sessions {
		got[string(s.Key.Session)] = s.Kinds
	}
	want := map[string][]string{
		"01a0fe1c-0000-7000-8000-0000000000a1": {"voice"},
		"01a0fe1c-0000-7000-8000-0000000000a2": {"work"},
		"01a0fe1c-0000-7000-8000-0000000000a3": {"voice", "work"},
		"01a0fe1c-0000-7000-8000-0000000000a4": nil,
		t1:                                     nil,
	}
	for id, k := range want {
		if g, ok := got[id]; !ok || !reflect.DeepEqual(g, k) {
			t.Errorf("%s: kinds %v (listed %v), want %v", id, g, ok, k)
		}
	}
	if len(m.Spec().Kinds) != 2 {
		t.Fatal("the setting needs both kinds declared")
	}
}
