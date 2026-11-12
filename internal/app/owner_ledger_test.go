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
	root := t.TempDir()
	write := func(runs string) {
		p := a.ledgerPath("codex", root, "s1")
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, []byte(`{"runs":`+runs+`}`), 0o600)
	}
	s := agent.Summary{Key: agent.SessionKey{Agent: "codex", Session: "s1"}, Owners: []agent.OwnerRun{{Key: "a", Person: "u"}}}
	write(`[{"key":"a","person":"u"},{"key":"b"}]`)
	if got := a.withLedger(true, root, s).Owners; len(got) != 2 || got[1].Key != "b" {
		t.Fatalf("%+v", got)
	}
	if got := a.withLedger(false, root, s).Owners; len(got) != 1 {
		t.Fatalf("a remote session reads no local ledger: %+v", got)
	}
	if got := a.withLedger(true, t.TempDir(), s).Owners; len(got) != 1 {
		t.Fatalf("another folder's record was read: %+v", got)
	}
	s.Key.Session = "../escape"
	if got := a.withLedger(true, root, s).Owners; len(got) != 1 {
		t.Fatal("an unsafe session ID reads nothing")
	}
	a.RecordOwner(context.Background(), "codex", "", "../escape") // ignored, no panic
}

// A Codex login hopsesh knows only by email (its keyring) takes the account of a verified
// login with that email, so its sessions' recorded creator matches it.
func TestEmailOnlyLoginMatchesVerifiedAccount(t *testing.T) {
	verified := &agent.Account{Key: "chatgpt-account:aa", Person: "p1", Email: "Me@Example.com", Confidence: "verified", LoggedIn: true}
	keyring := &agent.Account{Key: "chatgpt:bb", Email: "me@example.com", Confidence: "limited", LoggedIn: true}
	other := &agent.Account{Key: "chatgpt:cc", Email: "someone@example.com", Confidence: "limited", LoggedIn: true}
	ps := matchByEmail([]agent.RuntimeProfile{{Agent: "codex", Account: verified}, {Agent: "codex", Account: keyring}, {Agent: "codex", Account: other}})
	if got := ps[1].Account; got.Key != "chatgpt-account:aa" || got.Person != "p1" || got.Confidence != "email" {
		t.Fatalf("keyring login not matched: %+v", got)
	}
	if ps[2].Account.Key != "chatgpt:cc" || keyring.Key != "chatgpt:bb" {
		t.Fatal("matched another email, or changed the stored record")
	}
}

// Records travel: a session's runs are kept with its folder, read back for another machine
// (hopsesh serve), and added to that machine's listing of the session.
func TestOwnerRecordsAreSharedAndCarried(t *testing.T) {
	a := &App{StateDir: t.TempDir()}
	root := t.TempDir()
	a.addRuns("claude", root, "s1", agent.OwnerRun{Key: "a"}, agent.OwnerRun{Key: "b", Person: "u"})
	recs := a.LocalLedger()
	if len(recs) != 1 || recs[0].Session != "s1" || len(recs[0].Runs) != 2 {
		t.Fatalf("%+v", recs)
	}
	real, _ := filepath.EvalSymlinks(root)
	if recs[0].Root != root && recs[0].Root != real {
		t.Fatalf("root %q", recs[0].Root)
	}
	m := map[string][]agent.OwnerRun{}
	for _, r := range recs {
		m[string(r.Agent)+"\x00"+r.Root+"\x00"+r.Session] = r.Runs
	}
	s := withRecords(m, recs[0].Root, agent.Summary{Key: agent.SessionKey{Agent: "claude", Session: "s1"}})
	if len(s.Owners) != 2 || s.Owners[1].Key != "b" {
		t.Fatalf("not merged: %+v", s.Owners)
	}
	if s := withRecords(m, "/elsewhere", agent.Summary{Key: agent.SessionKey{Agent: "claude", Session: "s1"}}); len(s.Owners) != 0 {
		t.Fatal("another folder's record was merged")
	}
}
