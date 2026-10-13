package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// This executable models the observed vendor distinction: native lookup reads
// ~/.claude.json, but any explicit config root reads <root>/.claude.json.
// It runs only against temporary public auth-status fixtures, never real auth.
func nativeClaudeFixture(t *testing.T) (home, native, custom string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("executable fixture uses a POSIX shell; Windows scoping is covered in SDK tests")
	}
	home = t.TempDir()
	var err error
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	native, custom = filepath.Join(home, ".claude"), filepath.Join(home, "work-claude")
	bin := filepath.Join(home, "bin")
	for _, dir := range []string{native, custom, bin} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for file, email := range map[string]string{filepath.Join(home, ".claude.json"): "native@example.com", filepath.Join(native, ".claude.json"): "", filepath.Join(custom, ".claude.json"): "work@example.com"} {
		root := native
		if email == "work@example.com" {
			root = custom
		}
		b, err := json.Marshal(map[string]any{"loggedIn": email != "", "email": email, "authMethod": "claude.ai", "apiProvider": "firstParty", "orgId": email, "configDirectory": root})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	const program = `#!/bin/sh
printf '%s|%s\n' "$*" "${CLAUDE_CONFIG_DIR-<unset>}" >> "$HOME/fixture-calls"
case "$*" in
  '--version') echo '2.1.284 (Claude Code)'; exit 0 ;;
  'auth status --json')
    if [ -n "${ANTHROPIC_API_KEY-}${ANTHROPIC_AUTH_TOKEN-}${ANTHROPIC_BASE_URL-}${CLAUDE_CODE_OAUTH_TOKEN-}${CLAUDE_CODE_USE_BEDROCK-}${CLAUDE_CODE_USE_VERTEX-}${CLAUDE_CODE_USE_FOUNDRY-}" ]; then exit 2; fi
    if [ -n "$CLAUDE_CONFIG_DIR" ]; then
      /bin/cat "$CLAUDE_CONFIG_DIR/.claude.json"
    else
      /bin/cat "$HOME/.claude.json"
    fi
    ;;
  'auth login')
    if [ -n "${ANTHROPIC_API_KEY-}${ANTHROPIC_AUTH_TOKEN-}${ANTHROPIC_BASE_URL-}${CLAUDE_CODE_OAUTH_TOKEN-}${CLAUDE_CODE_USE_BEDROCK-}${CLAUDE_CODE_USE_VERTEX-}${CLAUDE_CODE_USE_FOUNDRY-}" ]; then exit 2; fi
    printf '%s' "${CLAUDE_CONFIG_DIR-<unset>}" ;;
  *) exit 2 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(program), 0700); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"HOME": home, "USERPROFILE": home, "PATH": bin, "CLAUDE_CONFIG_DIR": "",
		"HOPSESH_CONFIG_DIR": filepath.Join(home, "config"), "HOPSESH_STATE_DIR": filepath.Join(home, "state"), "HOPSESH_MACHINE": "here"} {
		t.Setenv(k, v)
	}
	for _, k := range claude.New().Spec().Accounts.Unset {
		t.Setenv(k, "fixture-override")
	}
	return home, native, custom
}

func TestClaudeNativeDefaultAccountProbeRefreshAndLogin(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "native lookup", true: "explicit native-path override"}[explicit], func(t *testing.T) {
			home, native, custom := nativeClaudeFixture(t)
			if explicit {
				t.Setenv("CLAUDE_CONFIG_DIR", native)
			}
			mod := claude.New()
			reg, err := registry.New(mod)
			if err != nil {
				t.Fatal(err)
			}
			a := cloudApp(t, reg)
			ctx := context.Background()
			m, err := a.accountMachine(ctx, "local")
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			h, err := m.For(ctx, mod.Spec(), agent.Install{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			base, err := mod.Detect(ctx, h)
			if err != nil {
				t.Fatal(err)
			}
			if !explicit {
				// The launcher can inherit a sibling root after the machine was
				// probed. Native scoping must actively unset it, not just omit it.
				t.Setenv("CLAUDE_CONFIG_DIR", custom)
			}
			installs, err := a.profileInstalls(ctx, m, mod, base, true)
			if err != nil || len(installs) != 1 {
				t.Fatalf("default discovery: %+v %v", installs, err)
			}
			in := installs[0]
			p := in.Profile
			if p == nil || !p.Default || p.Account == nil || p.Account.LoggedIn == explicit || (in.NativeDefaultRoot != "") == explicit {
				t.Fatalf("wrong default account or detection: %+v profile=%+v", in, p)
			}
			wantEmail, wantRoot := "native@example.com", "<unset>"
			if explicit {
				wantEmail, wantRoot = "", native
			}
			if p.Account.Email != wantEmail {
				t.Fatalf("probed wrong identity: %+v", p.Account)
			}
			if !explicit {
				t.Setenv("CLAUDE_CONFIG_DIR", "")
			}
			fresh, err := a.RefreshAccount(ctx, p.ID)
			if err != nil || fresh.Account == nil || fresh.Account.Email != wantEmail || fresh.Account.LoggedIn == explicit || fresh.Binding != p.Binding {
				t.Fatalf("refresh lost native identity/binding: %+v %v", fresh, err)
			}
			// Login only prepares a command, then executes our read-only fixture.
			checkLogin := func(id, wantRoot string) {
				t.Helper()
				c, err := a.AccountLogin(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				for _, k := range append(mod.Spec().Accounts.Unset, "CLAUDE_CONFIG_DIR") {
					if !slices.Contains(c.Unset, k) {
						t.Fatalf("login retained %s: %+v", k, c)
					}
				}
				r, err := m.Exec().Run(ctx, c.Argv, agent.RunOptions{Dir: c.Dir, Env: c.Env, Unset: c.Unset})
				if err != nil || r.Code != 0 || string(r.Stdout) != wantRoot {
					t.Fatalf("login used wrong config: %+v %v", r, err)
				}
			}
			checkLogin(p.ID, wantRoot)
			work, err := a.RegisterAccount(ctx, "local", "claude", "Work", custom, nil)
			if err != nil {
				t.Fatal(err)
			}
			work, err = a.RefreshAccount(ctx, work.ID)
			if err != nil || work.Account == nil || work.Account.Email != "work@example.com" || !work.Account.LoggedIn || work.Binding == fresh.Binding {
				t.Fatalf("custom profile leaked default identity: %+v %v", work, err)
			}
			checkLogin(work.ID, custom)
			installs, err = a.profileInstalls(ctx, m, mod, base, true)
			if err != nil || len(installs) != 2 {
				t.Fatalf("profile rediscovery: %+v %v", installs, err)
			}
			for _, in := range installs {
				want := wantEmail
				if in.Profile.ID == work.ID {
					want = "work@example.com"
				}
				if in.Profile.Account == nil || in.Profile.Account.Email != want {
					t.Fatalf("profile rediscovery mixed account scopes: %+v", in.Profile)
				}
			}
			log, err := os.ReadFile(filepath.Join(home, "fixture-calls"))
			if err != nil || !strings.Contains(string(log), "auth status --json|"+wantRoot+"\n") || !strings.Contains(string(log), "auth status --json|"+custom+"\n") {
				t.Fatalf("auth fixture was not invoked with pinned scope: %q %v", log, err)
			}
		})
	}
}
