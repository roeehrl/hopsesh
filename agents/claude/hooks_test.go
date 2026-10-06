package claude

import (
	"context"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"strings"
	"testing"
	"time"
)

func TestMovementHooksContractAndGate(t *testing.T) {
	h := newHost(t)
	m := New()
	in, _ := m.Detect(context.Background(), h)
	in.Profile = &agent.RuntimeProfile{ID: "exact-profile"}
	hi := m.MovementHooks(h, in, "/opt/hopsesh")
	if hi.Reason != "" || hi.File.Path != "/home/u/.claude/settings.json" {
		t.Fatalf("%+v", hi)
	}
	b, err := hi.File.Merge(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "--profile 'exact-profile'") || !strings.Contains(string(b), "UserPromptSubmit") || !strings.Contains(string(b), "SessionStart") {
		t.Fatalf("%s", b)
	}
	for _, v := range []string{"", "2.1.283", "2.1.284-beta", "3.0.0"} {
		in.Version = v
		hi = m.MovementHooks(h, in, "/opt/hopsesh")
		if hi.Reason == "" || hi.File == nil {
			t.Fatalf("gate %s: %+v", v, hi)
		}
	}
}

type windowsHookHost struct{ agent.Host }

func (h windowsHookHost) Facts() agent.Facts { f := h.Host.Facts(); f.OS = "windows"; return f }
func (h windowsHookHost) Path() agent.Path   { return agent.WindowsPath{} }

func TestMovementHooksWindowsPowerShellAndDisabledPolicy(t *testing.T) {
	fh := newHost(t)
	m := New()
	in, _ := m.Detect(context.Background(), fh)
	fh.Put(in.Root(home)+"/settings.json", []byte(`{"disableAllHooks":true}`), time.Time{})
	hi := m.MovementHooks(fh, in, "/opt/hopsesh")
	if hi.Reason != "" || !strings.Contains(hi.DisabledReason, "disableAllHooks") {
		t.Fatalf("%+v", hi)
	}
	in.Roots[home] = `C:\Users\alice\.claude`
	hi = m.MovementHooks(windowsHookHost{fh}, in, `C:\Program Files\hop's $test\hopsesh.exe`)
	if hi.Reason != "" {
		t.Fatal(hi.Reason)
	}
	b, err := hi.File.Merge(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"shell": "powershell"`) || !strings.Contains(string(b), "hop''s $test") {
		t.Fatalf("Windows handler: %s", b)
	}
}
