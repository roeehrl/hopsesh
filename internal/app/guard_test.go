package app

import (
	"strings"
	"testing"
	"time"

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
	_ = a.RestoreBlock(e.Session.Key)
	// On another machine, its own hook blocks it there: not claimed from here.
	e.Machine = "roee-macbook"
	if r := a.Role(e); r.Kind == "blocked" || r.Glyph == "◆" || !strings.Contains(r.Line, "not checked here") {
		t.Fatalf("another machine's original claimed blocked: %+v", r)
	}
	dep.SourceChanged = true
	if r := a.Role(e); r.Kind != "diverged" || r.Word != "Used after moving" {
		t.Fatalf("original used after the move: %+v", r)
	}
	dep.Status = "continued"
	if r := a.Role(e); r.Word != "Both changed" {
		t.Fatalf("both copies used after the move: %+v", r)
	}
	e.Machine = ""
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

// A copy a move left behind is refused, saying where its session went, until the user
// continues it anyway (released) or it was a separate fork; serving it from another
// machine releases it first, only when asked to continue it anyway.
func TestOldCopyIsRefusedUnlessContinuedAnyway(t *testing.T) {
	a := &App{Cfg: config.Defaults(), StateDir: t.TempDir()}
	at := time.Date(2026, 10, 22, 12, 39, 0, 0, time.UTC)
	e := Entry{Machine: "roee-macbook", Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Profile: "9f5c", Session: "s1"}},
		Departure: &MovementNotice{Operation: "op1", Status: "continued", Agent: "claude", AgentName: "Claude Code", Machine: "tnt-studio", At: at}}
	if !a.OldCopy(e) {
		t.Fatal("an original that moved on can be continued")
	}
	err := OldCopyError(e)
	if !strings.HasPrefix(err.Error(), MovedOriginal) || !strings.Contains(err.Error(), "moved to Claude Code on tnt-studio on 22 Oct") {
		t.Fatalf("refusal: %v", err)
	}
	if _, err := a.resumeGate(e, false); err == nil || !strings.HasPrefix(err.Error(), MovedOriginal) {
		t.Fatalf("served without anyway: %v", err)
	}
	release, err := a.resumeGate(e, true)
	if err != nil || a.Released(e.Session.Key, "op1") {
		t.Fatalf("anyway releases only once it runs: %v", err)
	}
	if err := release(); err != nil || !a.Released(e.Session.Key, "op1") || a.OldCopy(e) {
		t.Fatalf("anyway: %v", err)
	}
	if release, err := a.resumeGate(e, false); err != nil || release() != nil {
		t.Fatalf("a released copy resumes: %v", err)
	}
	e.Departure.Operation = "op2" // moved again later: blocked again
	if !a.OldCopy(e) {
		t.Fatal("a release carried over to a later move")
	}
	e.Departure.Status = "forked"
	if a.OldCopy(e) {
		t.Fatal("a separate fork's original goes on")
	}
	if a.OldCopy(Entry{}) {
		t.Fatal("a copy that never moved")
	}
}
