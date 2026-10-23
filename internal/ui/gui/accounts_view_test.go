package gui

import (
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// The Accounts screen names each account by email wherever a tool is signed in to it,
// matches Claude desktop's own sign-in to it by its ids, keeps an account only seen in
// records apart, and says when two accounts write to one folder.
func TestAccountsViewJoinsSignInsAcrossToolsAndMachines(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOPSESH_STATE_DIR", t.TempDir())
	t.Setenv("HOPSESH_CONFIG_DIR", t.TempDir())
	personal := &agent.Account{LoggedIn: true, Email: "me@gmail.com", Key: "org-p", Person: "user-p", Label: "max"}
	work := &agent.Account{LoggedIn: true, Email: "roy@conduct.security", Key: "org-w", Person: "user-w", Label: "team"}
	here := &app.Machine{Name: app.LocalName(), Local: true, Status: app.StatusOK, Agents: []app.AgentState{{Agent: "claude", Install: agent.Install{Agent: "claude", Binary: "/x/claude", Version: "2.1.299", Present: true,
		Desktop: "/Applications/Claude.app", DesktopVersion: "2.31226.1", DesktopEngine: "2.1.295", DesktopAccount: &agent.OwnerRun{Key: "org-p", Person: "user-p"},
		Roots: map[string]string{"home": "/h/.claude"}, Profile: &agent.RuntimeProfile{ID: "here-default", Default: true, Root: "/h/.claude", Account: work}}}}}
	studio := &app.Machine{Name: "studio", Status: app.StatusOK, Presence: app.PresenceSSH, Agents: []app.AgentState{{Agent: "claude", Install: agent.Install{Agent: "claude", Binary: "/x/claude", Version: "2.1.299", Present: true,
		Roots: map[string]string{"home": "/u/.claude"}, Profile: &agent.RuntimeProfile{ID: "studio-default", Default: true, Root: "/u/.claude", Account: personal}}}}}
	entry := func(machine, profile string, runs ...agent.OwnerRun) app.Entry {
		return app.Entry{Machine: machine, Agent: "claude", Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Profile: profile, Session: agent.SessionID(machine + profile + string(rune('a'+len(runs))))}, Owners: runs}}
	}
	inv := &app.Inventory{Machines: []*app.Machine{studio, here}, Entries: []app.Entry{
		entry(app.LocalName(), "here-default", agent.OwnerRun{Key: "org-p", Person: "user-p"}),
		entry(app.LocalName(), "here-default", agent.OwnerRun{Key: "org-w", Person: "user-w"}, agent.OwnerRun{Key: "org-w", Person: "user-w"}),
		entry(app.LocalName(), "here-default", agent.OwnerRun{Key: "org-x"}),
		entry(app.LocalName(), "here-default"),
		entry("studio", "studio-default", agent.OwnerRun{Key: "org-p", Person: "user-p"}),
	}}
	a := &App{core: app.New(config.Defaults(), all.Registry(), t.TempDir(), nil), inv: inv}
	v, err := a.AccountsView()
	if err != nil {
		t.Fatal(err)
	}
	byEmail := map[string]PersonDTO{}
	var unknown []PersonDTO
	for _, p := range v.People {
		if p.Unknown {
			unknown = append(unknown, p)
		} else {
			byEmail[p.Email] = p
		}
	}
	me, rc := byEmail["me@gmail.com"], byEmail["roy@conduct.security"]
	if me.Sessions != 2 || me.Machines != 2 || me.Initials != "M" || rc.Initials != "RC" || rc.Sessions != 1 {
		t.Fatalf("people: %+v", v.People)
	}
	if len(unknown) != 1 || unknown[0].Sessions != 1 || unknown[0].Initials != "?" {
		t.Fatalf("unknown account: %+v", unknown)
	}
	if !strings.Contains(strings.Join(me.Where, "\n"), "This machine: Claude desktop") {
		t.Fatalf("desktop's sign-in not matched to its email: %v", me.Where)
	}
	var desk CellDTO
	for _, r := range v.Machines {
		if r.Local {
			desk = r.Cells[1]
		}
	}
	if !desk.Installed || desk.Version != "Claude desktop 2.31226.1, running its own Claude Code 2.1.295" || desk.SignIns[0].Email != "me@gmail.com" {
		t.Fatalf("desktop cell: %+v", desk)
	}
	if len(v.Notes) != 1 || !strings.Contains(v.Notes[0].Title, "two accounts write to ~/.claude") {
		t.Fatalf("notes: %+v", v.Notes)
	}
	var folder FolderDTO
	for _, f := range v.Folders {
		if f.Profile == "here-default" {
			folder = f
		}
	}
	if folder.Unrecorded != 1 || len(folder.Counts) != 3 || !strings.Contains(folder.Tools, "Claude desktop (me@gmail.com)") {
		t.Fatalf("folder: %+v", folder)
	}
}
