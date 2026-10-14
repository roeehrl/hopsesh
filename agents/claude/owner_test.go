package claude

import (
	"os"
	"path/filepath"
	"runtime"
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

// Inside Claude desktop (its own Claude Code), the run is desktop's account, whatever the
// terminal's folder is signed in to.
func TestDesktopRunIsDesktopsAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	dir := filepath.Join(home, "Library", "Application Support", "Claude")
	if runtime.GOOS == "windows" {
		dir = filepath.Join(home, "AppData", "Roaming", "Claude")
	}
	const acct, org = "eb49ab91-6e1e-4066-8188-55844f05f186", "ccd0bf80-f6e6-4528-a58b-efc868608732"
	os.MkdirAll(filepath.Join(dir, "claude-code-sessions", acct, org), 0o700)
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"lastKnownAccountUuid":"`+acct+`","oauth:tokenCache":"x"}`), 0o600)
	os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"oauthAccount":{"accountUuid":"terminal-user","organizationUuid":"terminal-org"}}`), 0o600)
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "claude-desktop")
	r, ok := New().LocalIdentity(filepath.Join(home, ".claude"), true)
	if !ok || r.Key != accountKey(org) || r.Person != personKey(acct) || r.Source != "claude-desktop" {
		t.Fatalf("%+v %v", r, ok)
	}
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	if r, ok := New().LocalIdentity(filepath.Join(home, ".claude"), true); !ok || r.Key != accountKey("terminal-org") {
		t.Fatalf("the terminal's run: %+v %v", r, ok)
	}
}
