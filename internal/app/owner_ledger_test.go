package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// The hook's record adds runs the agent did not record, without repeating ones it did.
func TestOwnerLedgerAddsUnrecordedRuns(t *testing.T) {
	a := &App{StateDir: t.TempDir()}
	write := func(runs string) {
		p := a.ledgerPath("codex", "s1")
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, []byte(`{"runs":`+runs+`}`), 0o600)
	}
	s := agent.Summary{Key: agent.SessionKey{Agent: "codex", Session: "s1"}, Owners: []agent.OwnerRun{{Key: "a", Person: "u"}}}
	write(`[{"key":"a","person":"u"},{"key":"b"}]`)
	if got := a.withLedger(true, s).Owners; len(got) != 2 || got[1].Key != "b" {
		t.Fatalf("%+v", got)
	}
	if got := a.withLedger(false, s).Owners; len(got) != 1 {
		t.Fatalf("a remote session reads no local ledger: %+v", got)
	}
	s.Key.Session = "../escape"
	if got := a.withLedger(true, s).Owners; len(got) != 1 {
		t.Fatal("an unsafe session ID reads nothing")
	}
	a.RecordOwner(context.Background(), "codex", "", "../escape") // ignored, no panic
}
