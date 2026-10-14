package e2e

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// A session's own root, now signed in to another account than the one its record says
// ran it: continuing it there makes a new portable session for the signed-in account
// instead of refusing ("already here") or replaying the other account's native data.
func TestContinueInOwnRootUnderAnotherAccount(t *testing.T) {
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
	if len(p.Blockers) > 0 {
		t.Fatalf("blocked: %v", p.Blockers)
	}
	if p.Kind != move.KindContinue || !p.Options.OtherAccount || !p.Placement.OtherAccount || p.Placement.Key.Session == sid {
		t.Fatalf("want a new portable session for the signed-in account: kind=%s other=%v key=%v", p.Kind, p.Options.OtherAccount, p.Placement.Key)
	}
	if !strings.Contains(strings.Join(p.Warnings, "\n"), "run by another") {
		t.Fatalf("the plan must say the session was run by another account: %v", p.Warnings)
	}
	res, err := move.Apply(context.Background(), p, in, move.Env{StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	after := list(t, here)
	fresh, ok := after[string(p.Placement.Key.Session)]
	if !ok || fresh.CWD != here.repo {
		t.Fatalf("new session missing: %+v", fresh)
	}
	b, _ := os.ReadFile(fresh.Path)
	if strings.Contains(string(b), `"signature"`) || strings.Contains(string(b), "bridge-session") {
		t.Fatalf("account-bound content carried into the new session:\n%s", b)
	}
	if now, _ := os.ReadFile(orig); !strings.HasPrefix(string(now), string(before)) {
		t.Fatal("the original session's own history changed")
	}
	if !resumes(res.Command, string(p.Placement.Key.Session)) {
		t.Fatalf("command: %s", res.Command)
	}
}
