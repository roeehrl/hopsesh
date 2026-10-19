package e2e

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// A session's own root, now signed in to another account than the one its record says
// ran it: Claude Code continues it right there under the signed-in account, instead of
// refusing ("already here").
func TestResumeInOwnRootUnderAnotherAccount(t *testing.T) {
	here := newLocation(t, "here", t.TempDir())
	seed(t, here)
	orig := list(t, here)[sid].Path
	f, err := os.OpenFile(orig, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"bridge-session","bridgeSessionId":"cse_01OwnerAbc123","lastSequenceNum":4,"sessionId":"` + sid + `","ownerAccountUuid":"acct-a","ownerOrganizationUuid":"org-a"}` + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	before, _ := os.ReadFile(orig)
	owner := list(t, here)[sid].Mirror
	if owner == nil || owner.Account == "" {
		t.Fatalf("the fixture's recorded owner was not read: %+v", owner)
	}

	t.Run("same account stays a no-op", func(t *testing.T) {
		in := input(t, here, here)
		in.Source.Account = &agent.Account{LoggedIn: true, Key: owner.Account}
		in.Target.Account = in.Source.Account
		p, err := move.Build(context.Background(), in, move.Options{TargetDir: here.repo})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.Join(p.Blockers, "\n"), "already here") {
			t.Fatalf("a same-account, same-place move must stay blocked: %+v", p.Blockers)
		}
	})

	in := input(t, here, here)
	// The root is signed in to B now; the source side reports B too (its current login),
	// which must not be mistaken for the session's owner.
	in.Source.Account = &agent.Account{LoggedIn: true, Key: "account-b"}
	in.Target.Account = in.Source.Account
	p, err := move.Build(context.Background(), in, move.Options{TargetDir: here.repo})
	if err != nil {
		t.Fatal(err)
	}
	// Claude Code resumes across accounts (the API ignores the other account's signed
	// reasoning): the session continues where it is, nothing is copied.
	if len(p.Blockers) > 0 || !p.ResumeHere || !strings.Contains(strings.Join(p.Warnings, "\n"), "ran under another") {
		t.Fatalf("want a resume in place: blockers=%v resumeHere=%v warnings=%v", p.Blockers, p.ResumeHere, p.Warnings)
	}
	count := len(list(t, here))
	res, err := move.Apply(context.Background(), p, in, move.Env{StateDir: t.TempDir()})
	if err != nil || !resumes(res.Command, sid) {
		t.Fatalf("resume here: %v %+v", err, res)
	}
	if now, _ := os.ReadFile(orig); string(now) != string(before) {
		t.Fatal("resuming in place must not touch the session")
	}
	if len(list(t, here)) != count {
		t.Fatal("nothing may be copied")
	}
}

// Codex cannot resume a session under another account (its encrypted reasoning is bound to
// the one that produced it), so in its own root, now signed in to another account, or
// with no record of its account, it continues as a cleaned copy and the original stays.
func TestCodexInOwnRootUnderAnotherAccount(t *testing.T) {
	here := newLocation(t, "here", t.TempDir())
	in := seedCodex(t, here)
	cx := codex.New()
	var s agent.Summary
	for _, x := range listAgent(t, here, cx, in) {
		if x.Key.Session == "01a0fe1c-0000-7000-8000-000000000001" {
			s = x
		}
	}
	before, _ := os.ReadFile(s.Path)
	side := move.Side{Machine: here.m, Module: cx, Install: in, Account: &agent.Account{LoggedIn: true, Key: "chatgpt-account:b"}}
	inp := move.Input{Source: side, Target: side, Session: s}
	p, err := move.Build(context.Background(), inp, move.Options{TargetDir: here.repo})
	if err != nil || len(p.Blockers) > 0 {
		t.Fatalf("%v %v", err, p.Blockers)
	}
	if p.ResumeHere || p.Kind != move.KindContinue || !p.Options.OtherAccount || p.Placement.Key.Session == s.Key.Session {
		t.Fatalf("want a cleaned copy: resumeHere=%v kind=%s other=%v", p.ResumeHere, p.Kind, p.Options.OtherAccount)
	}
	if _, err := move.Apply(context.Background(), p, inp, move.Env{StateDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	var fresh agent.Summary
	for _, x := range listAgent(t, here, cx, in) {
		if x.Key.Session == p.Placement.Key.Session {
			fresh = x
		}
	}
	b, _ := os.ReadFile(fresh.Path)
	if fresh.Path == "" || strings.Contains(string(b), "encrypted_content") {
		t.Fatalf("cleaned copy missing or still carries encrypted reasoning: %q", fresh.Path)
	}
	if now, _ := os.ReadFile(s.Path); !strings.HasPrefix(string(now), string(before)) {
		t.Fatal("the original changed")
	}
}
