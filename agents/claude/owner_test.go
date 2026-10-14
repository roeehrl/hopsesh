package claude

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Each launch's credential_org and a Remote Control owner become the session's runs.
func TestSummaryOwnersFromRecords(t *testing.T) {
	var head, tail []record
	for _, l := range []string{
		`{"type":"attachment","uuid":"1","attachment":{"type":"credential_org","organizationUuid":"org-a"}}`,
		`{"type":"user","uuid":"2","message":{"role":"user","content":"hi"}}`,
		`{"type":"bridge-session","sessionId":"s","bridgeSessionId":"cse_01Abc","ownerAccountUuid":"user-a","ownerOrganizationUuid":"org-a"}`,
		`{"type":"attachment","uuid":"3","attachment":{"type":"credential_org","organizationUuid":"org-b"}}`,
	} {
		head = append(head, parseLines([]byte(l), false)...)
	}
	s := &info{}
	apply(s, head, tail)
	want := []agent.OwnerRun{{Key: accountKey("org-a"), Person: personKey("user-a"), Source: "agent"}, {Key: accountKey("org-b"), Source: "agent"}}
	if len(s.Owners) != 2 || s.Owners[0] != want[0] || s.Owners[1] != want[1] {
		t.Fatalf("%+v", s.Owners)
	}
}

func TestLocalIdentityReadsOAuthAccountOnly(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, ".claude.json"), []byte(`{"oauthAccount":{"accountUuid":"user-a","organizationUuid":"org-a","emailAddress":"a@example.com"}}`), 0o600)
	run, ok := New().LocalIdentity(root, false)
	if !ok || run.Key != accountKey("org-a") || run.Person != personKey("user-a") {
		t.Fatalf("%+v %v", run, ok)
	}
	if _, ok := New().LocalIdentity(t.TempDir(), false); ok {
		t.Fatal("no identity record, no identity")
	}
}
