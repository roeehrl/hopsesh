package gui

import (
	"context"
	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAccountPlanInvalidatedByEditAndRemoval(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	ctx := context.Background()
	p, err := a.core.RegisterAccount(ctx, "", "claude", "Second personal", "", []string{" Personal ", "personal"})
	if err != nil {
		t.Fatal(err)
	}
	scan, err := a.Scan()
	if err != nil {
		t.Fatal(err)
	}
	e := findEntry(t, scan, "claude/"+sid)
	plan, err := a.Plan(e.Machine, e.Key, "claude", OptsDTO{TargetProfile: p.ID})
	if err != nil || len(plan.Blockers) > 0 {
		t.Fatal(plan, err)
	}
	ps, _ := a.core.Accounts()
	for _, v := range ps {
		if v.ID == p.ID {
			p = v
		}
	}
	if err = a.core.EditAccount(p.ID, "Renamed", p.Tags, p.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Apply(); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatal("stale account plan accepted", err)
	}
	files, _ := filepath.Glob(filepath.Join(p.Root, "projects", "*", "*.jsonl"))
	if len(files) > 0 {
		t.Fatal("stale plan wrote native files")
	}
	scan, err = a.Scan()
	if err != nil {
		t.Fatal(err)
	}
	e = findEntry(t, scan, "claude/"+sid)
	if _, err = a.Plan(e.Machine, e.Key, "claude", OptsDTO{TargetProfile: p.ID}); err != nil {
		t.Fatal(err)
	}
	ps, _ = a.core.Accounts()
	for _, v := range ps {
		if v.ID == p.ID {
			p = v
		}
	}
	if err = a.core.ForgetAccount(p.ID, p.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Apply(); err == nil || !strings.Contains(err.Error(), "removed") {
		t.Fatal("removed account plan accepted", err)
	}
	if _, err = os.Stat(p.Root); err != nil {
		t.Fatal("forget removed vendor data", err)
	}
}

func TestCodexDesktopAvailabilityAndExactThreadThroughService(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	scan, _ := a.Scan()
	e := findEntry(t, scan, "claude/"+sid)
	if _, err := a.Plan(e.Machine, e.Key, "codex", OptsDTO{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Scan(); err != nil {
		t.Fatal(err)
	}
	local := a.inv.Local()
	for i := range local.Agents {
		if local.Agents[i].Agent == "codex" {
			local.Agents[i].Install.Desktop = "/Applications/Codex.app"
			local.Agents[i].Install.OS = "darwin"
		}
	}
	var entry app.Entry
	for _, v := range a.inv.Entries {
		if v.Agent == "codex" {
			entry = v
		}
	}
	d := entryDTO(a.core, a.inv, app.Item{Entry: entry}, nil)
	if !d.CanApp {
		t.Fatal("desktop option hidden", d.AppWhy)
	}
	c, err := a.core.Resume(a.inv, entry, agent.ResumeOptions{App: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Argv) != 4 || c.Argv[3] != "codex://threads/"+string(entry.Session.Key.Session) || !c.Wait {
		t.Fatalf("wrong launch: %+v", c)
	}
	for i := range local.Agents {
		if local.Agents[i].Agent == "codex" {
			local.Agents[i].Install.Desktop = ""
			local.Agents[i].Install.DesktopWhy = "Install the desktop app"
		}
	}
	d = entryDTO(a.core, a.inv, app.Item{Entry: entry}, nil)
	if d.CanApp || d.AppWhy == "" {
		t.Fatal("missing installation offered")
	}
	if _, err = a.core.Resume(a.inv, entry, agent.ResumeOptions{App: true}); err == nil {
		t.Fatal("missing installation fell back to terminal")
	}
}

func TestRegisterDefaultRootBeforeFirstScanStillAllowsUnqualifiedSelection(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	root := filepath.Join(os.Getenv("HOME"), ".claude")
	p, err := a.core.RegisterAccount(context.Background(), "local", "claude", "My personal login", root, []string{"Personal"})
	if err != nil {
		t.Fatal(err)
	}
	scan, err := a.Scan()
	if err != nil {
		t.Fatal(err)
	}
	in, ok := a.inv.Local().Install("claude")
	if !ok || in.ProfileID() != p.ID || !in.Profile.Default {
		t.Fatalf("registered default disappeared: %+v", in)
	}
	e := findEntry(t, scan, "claude/"+sid)
	if _, err = a.core.Resume(a.inv, a.inv.Entries[0], agent.ResumeOptions{}); err != nil {
		t.Fatal("default root could not resume", err)
	}
	if e.Profile == nil || e.Profile.Name != "My personal login" {
		t.Fatal("scan overwrote custom label")
	}
}

// Account discovery is gated by endpoint identity, not merely the presence of a
// hopsesh executable or a working SSH connection. Rendering must not create it.
func TestRemoteAccountSetupNoticeUsesEndpointIdentity(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	if _, err := a.Scan(); err != nil {
		t.Fatal(err)
	}
	m := a.inv.Local()
	endpoint := m.Host().Facts.Endpoint
	if endpoint == "" {
		t.Fatal("local scan did not initialize identity")
	}
	m.Local = false
	m.Hopsesh = "test-version"
	for _, tc := range []struct {
		name, endpoint, status string
		want                   bool
	}{
		{"connected without identity", "", app.StatusOK, true},
		{"initialized", endpoint, app.StatusOK, false},
		{"unreachable", "", "offline", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m.Host().Facts.Endpoint, m.Status = tc.endpoint, tc.status
			dto := scanDTO(a.core, a.inv, time.Now(), time.Now())
			found := false
			for _, d := range dto.Machines {
				if d.Name == m.Name {
					found = true
					if d.AccountSetupRequired != tc.want {
						t.Fatalf("setup required=%v, want %v", d.AccountSetupRequired, tc.want)
					}
				}
			}
			if !found {
				t.Fatal("machine omitted")
			}
			if m.Host().Facts.Endpoint != tc.endpoint {
				t.Fatal("render initialized remote identity")
			}
		})
	}
}

func TestAccountsIdentifyLocalProfilesWithoutLiveInventory(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	defer a.Shutdown()
	p, err := a.core.RegisterAccount(context.Background(), "", "claude", "Cached personal", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	a.inv = nil
	accounts, err := a.Accounts()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range accounts {
		if v.ID == p.ID {
			if !v.Local || !v.Stale {
				t.Fatal("locality incorrectly depends on a live scan", v)
			}
			return
		}
	}
	t.Fatal("profile missing")
}

func TestAccountSignInCompletionRefreshesTheSelectedProfile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake vendor CLI; portable account refresh is covered by scenario matrix")
	}
	home(t)
	bin := t.TempDir()
	script := `#!/bin/sh
case "$1" in
 --version) echo '2.1.284'; exit 0;;
esac
if [ "$1" = auth ] && [ "$2" = login ]; then touch "$CLAUDE_CONFIG_DIR/signed-in"; exit 0; fi
if [ "$1" = auth ] && [ "$2" = status ]; then
 if [ -f "$CLAUDE_CONFIG_DIR/signed-in" ]; then
  printf '{"loggedIn":true,"email":"signed-in@example.com","authMethod":"claude.ai","configDirectory":"%s"}\n' "$CLAUDE_CONFIG_DIR"
 else
  printf '{"loggedIn":false,"configDirectory":"%s"}\n' "$CLAUDE_CONFIG_DIR"; exit 1
 fi
 exit 0
fi
exit 1
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+testPath())
	a := NewApp(all.Registry())
	defer a.Shutdown()
	if _, err := a.Scan(); err != nil {
		t.Fatal(err)
	}
	ps, err := a.core.Accounts()
	if err != nil {
		t.Fatal(err)
	}
	var profile agent.RuntimeProfile
	for _, p := range ps {
		if p.Agent == "claude" {
			profile = p
			break
		}
	}
	if profile.ID == "" || profile.Account == nil || profile.Account.LoggedIn {
		t.Fatalf("initial account: %+v", profile)
	}
	before := a.inv
	signedIn := make(chan agent.RuntimeProfile, 1)
	a.Emitter = func(name string, data any) {
		if name == AccountEvent {
			if p, ok := data.(agent.RuntimeProfile); ok && p.ID == profile.ID && p.Account != nil && p.Account.LoggedIn {
				signedIn <- p
			}
		}
	}
	a.Terms = NewTerminals("test")
	opened, err := a.LoginAccount(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Tab == "" {
		t.Fatal("sign-in did not use tracked terminal")
	}
	select {
	case p := <-signedIn:
		if p.Account.Email != "signed-in@example.com" || p.IdentitySource != "local" {
			t.Fatalf("refresh: %+v", p)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("sign-in exited without refreshing account")
	}
	a.mu.Lock()
	after := a.inv
	a.mu.Unlock()
	if after == before {
		t.Fatal("account refresh reused a published inventory")
	}
	for _, entry := range before.Entries {
		if entry.Profile != nil && entry.Profile.ID == profile.ID && entry.Profile.Account.LoggedIn {
			t.Fatal("account refresh mutated an old publication")
		}
	}
	for _, entry := range after.Entries {
		if entry.Profile != nil && entry.Profile.ID == profile.ID && !entry.Profile.Account.LoggedIn {
			t.Fatal("new publication missed refreshed sign-in")
		}
	}
}

// Account grouping keeps physical source copies visible after lineage selects the
// destination as the representative in the ordinary session list.
func TestAccountGroupingRetainsSourceAndDestinationCopies(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	defer a.Shutdown()
	profile, err := a.core.RegisterAccount(context.Background(), "", "claude", "Second personal", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	scan, err := a.Scan()
	if err != nil {
		t.Fatal(err)
	}
	source := findEntry(t, scan, "claude/"+sid)
	if source.Profile == nil {
		t.Fatal("source profile missing")
	}
	if _, err = a.Plan(source.Machine, source.Key, "claude", OptsDTO{TargetProfile: profile.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Apply(); err != nil {
		t.Fatal(err)
	}
	scan, err = a.Scan()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, group := range append(append([]GroupDTO{}, scan.Groups...), scan.ProfileCopies...) {
		for _, entry := range group.Entries {
			if entry.Profile != nil && (entry.Session == sid || entry.Profile.ID == profile.ID) {
				seen[entry.Profile.ID]++
			}
		}
	}
	if seen[source.Profile.ID] != 1 || seen[profile.ID] != 1 {
		t.Fatalf("lost or duplicated account copies: %v", seen)
	}
	if len(scan.ProfileCopies) == 0 {
		t.Fatal("source copy missing from account grouping")
	}
	for _, group := range scan.ProfileCopies {
		for _, entry := range group.Entries {
			if entry.Profile == nil {
				t.Fatal("copy has no profile")
			}
			for _, copy := range entry.Copies {
				if copy.Profile == nil {
					t.Fatal("inspector copy has no profile")
				}
			}
		}
	}
}
