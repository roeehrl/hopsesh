package codex

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
	in.Version = "0.160.0"
	in.Profile = &agent.RuntimeProfile{ID: "exact-profile"}
	hi := m.MovementHooks(h, in, "/opt/hopsesh")
	if hi.Reason != "" || hi.File.Path != "/home/u/.codex/hooks.json" {
		t.Fatalf("%+v", hi)
	}
	b, err := hi.File.Merge([]byte(`{"description":"my hooks","hooks":{"Stop":[{"hooks":[{"type":"command","command":"mine"}]}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "--profile 'exact-profile'") || !strings.Contains(string(b), "UserPromptSubmit") || !strings.Contains(string(b), "SessionStart") || !strings.Contains(string(b), "my hooks") {
		t.Fatalf("%s", b)
	}
	for _, v := range []string{"", "0.159.9", "0.160.0-alpha.1", "1.0.0"} {
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

func TestMovementHooksWindowsAndDisabledFeatures(t *testing.T) {
	fh := newHost(t)
	m := New()
	in, _ := m.Detect(context.Background(), fh)
	in.Version = "0.160.0"
	for _, cfg := range []string{"[features]\nhooks = false\n", "[features]\ncodex_hooks = false\n", "allow_managed_hooks_only = true\n", "profile = 'work'\n[features]\nhooks = true\n[profiles.work.features]\nhooks = false\n"} {
		fh.Put(in.Root(home)+"/config.toml", []byte(cfg), time.Time{})
		hi := m.MovementHooks(fh, in, "/opt/hopsesh")
		if hi.Reason != "" || hi.DisabledReason == "" {
			t.Fatalf("disabled policy missing: %+v", hi)
		}
		if actual, _ := fh.Get(in.Root(home) + "/config.toml"); string(actual) != cfg {
			t.Fatal("policy changed")
		}
	}
	in.Roots[home] = `C:\Users\alice\.codex`
	hi := m.MovementHooks(windowsHookHost{fh}, in, `C:\Program Files\hop's $test\hopsesh.exe`)
	if hi.Reason != "" {
		t.Fatal(hi.Reason)
	}
	b, err := hi.File.Merge(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"commandWindows": "powershell.exe -NoLogo -NoProfile -NonInteractive -EncodedCommand `) {
		t.Fatalf("Windows handler: %s", b)
	}
}
