package app

import (
	"testing"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestOriginalGuardModesAndRelease(t *testing.T) {
	a := &App{Cfg: config.Defaults(), StateDir: t.TempDir()}
	key := agent.SessionKey{Agent: "claude", Session: "s1"}
	for status, want := range map[string]string{"prepared": GuardBlock, "continued": GuardBlock, "diverged": GuardBlock, "forked": GuardAdvise} {
		if got := a.OriginalGuard(key, "op1", status); got != want {
			t.Fatalf("default %s: got %s, want %s", status, got, want)
		}
	}
	if err := a.ReleaseOriginal(key, "op1"); err != nil {
		t.Fatal(err)
	}
	if a.OriginalGuard(key, "op1", "continued") != GuardAdvise || !a.Released(key, "op1") {
		t.Fatal("a released original must not be blocked")
	}
	if a.OriginalGuard(key, "op2", "continued") != GuardBlock {
		t.Fatal("a release must not carry over to a later move")
	}
	if err := a.RestoreBlock(key); err != nil || a.Released(key, "op1") {
		t.Fatalf("restore: %v", err)
	}
	a.Cfg.Original = config.OriginalAdvise
	if a.OriginalGuard(key, "op1", "prepared") != GuardAdvise {
		t.Fatal("advise mode blocked")
	}
	a.Cfg.Original = config.OriginalOff
	if a.OriginalGuard(key, "op1", "prepared") != GuardOff {
		t.Fatal("off mode protected")
	}
}

func TestRoleWords(t *testing.T) {
	a := &App{Cfg: config.Defaults(), StateDir: t.TempDir()}
	dep := &MovementNotice{Operation: "op1", Status: "prepared", Agent: "codex", AgentName: "Codex", Machine: "mbp"}
	e := Entry{Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Session: "s1"}}, Departure: dep}
	if r := a.Role(e); r == nil || r.Kind != "blocked" || r.Glyph != "◆" || r.Line != "moved to Codex on mbp; blocked until you move back" {
		t.Fatalf("blocked original: %+v", r)
	}
	_ = a.ReleaseOriginal(e.Session.Key, "op1")
	if r := a.Role(e); r.Word != "Unblocked" {
		t.Fatalf("released original: %+v", r)
	}
	e.Departure = nil
	e.Arrival = &Arrival{Kind: "moved", AgentName: "Claude Code", Machine: "studio"}
	if r := a.Role(e); r.Kind != "moved" || r.Glyph != "●" {
		t.Fatalf("moved copy: %+v", r)
	}
	e.Arrival.Kind = "returned"
	if r := a.Role(e); r.Kind != "returned" || r.Line != "moved back from Claude Code on studio" {
		t.Fatalf("returned: %+v", r)
	}
	e.Arrival.Kind = "continuation"
	if r := a.Role(e); r.Kind != "continuation" || r.Word != "Continuation" {
		t.Fatalf("bounded continuation shown as a move: %+v", r)
	}
	if a.Role(Entry{}) != nil {
		t.Fatal("a copy that never moved has no role")
	}
}
