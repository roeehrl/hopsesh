package move

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

type observedAccountModule struct {
	agent.Module
	observed agent.Account
}

func (m *observedAccountModule) Account(context.Context, agent.Host, agent.Install) (agent.Account, error) {
	return m.observed, nil
}

func TestAccountBindingRecheckedBeforeWriting(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	initial := agent.Account{LoggedIn: true, Provider: "firstParty", Observation: "login-one", Confidence: "limited"}
	mod := &observedAccountModule{Module: claude.New(), observed: initial}
	m := &host.Machine{Local: true, Facts: host.Facts{Home: root}}
	p := &agent.RuntimeProfile{ID: "one", Root: root, Name: "Research", Account: &initial}
	side := Side{Machine: m, Module: mod, Install: agent.Install{Agent: "claude", Binary: "vendor", Profile: p, Roots: map[string]string{"home": root}}}
	in := Input{Source: side}
	if err := ValidateProfiles(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	for _, changed := range []agent.Account{
		{LoggedIn: true, Provider: "firstParty", Observation: "login-two"},
		{LoggedIn: false, Provider: "firstParty", Observation: "login-one"},
		{LoggedIn: true, Provider: "other", Observation: "login-one"},
	} {
		mod.observed = changed
		if err := ValidateProfiles(context.Background(), in); err == nil || !strings.Contains(err.Error(), "login changed") {
			t.Fatalf("accepted changed login: %v", err)
		}
	}
	mod.observed = initial
	mod.observed.IsolationWhy = "This login cannot be isolated"
	if err := ValidateProfiles(context.Background(), in); err == nil || !strings.Contains(err.Error(), "cannot be isolated") {
		t.Fatalf("accepted unsupported isolation: %v", err)
	}
	mod.observed = initial
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := ValidateProfiles(context.Background(), in); err == nil || !strings.Contains(err.Error(), "root changed") {
		t.Fatalf("accepted missing root: %v", err)
	}
}

func TestUnknownDefaultLoginAllowsOnlyPortableProfileBoundary(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mod := &observedAccountModule{Module: claude.New(), observed: agent.Account{Observation: "signed-out"}}
	m := &host.Machine{Local: true, Facts: host.Facts{Home: root}}
	p := &agent.RuntimeProfile{ID: "source", Root: root, Name: "Default", Default: true}
	side := Side{Machine: m, Module: mod, Install: agent.Install{Agent: "claude", Binary: "vendor", Profile: p, Roots: map[string]string{"home": root}}}
	in := Input{Source: side, Target: Side{Install: agent.Install{Profile: &agent.RuntimeProfile{ID: "target"}}}}
	if err := ValidateProfiles(context.Background(), in); err != nil {
		t.Fatalf("unknown sign-in blocked portable preparation: %v", err)
	}
	for _, change := range []string{"visible login", "known login", "named profile", "same profile", "empty observation"} {
		t.Run(change, func(t *testing.T) {
			profile := *p
			input := in
			input.Source.Install.Profile = &profile
			mod.observed = agent.Account{Observation: "signed-out"}
			switch change {
			case "visible login":
				mod.observed = agent.Account{LoggedIn: true, Observation: "new-login"}
			case "known login":
				profile.Account = &agent.Account{LoggedIn: true, Observation: "old-login"}
			case "named profile":
				profile.Default = false
			case "same profile":
				input.Target.Install.Profile = &profile
			case "empty observation":
				mod.observed = agent.Account{}
			}
			if err := ValidateProfiles(context.Background(), input); err == nil {
				t.Fatal("unverified binding accepted beyond portable default-profile preparation")
			}
		})
	}
}
