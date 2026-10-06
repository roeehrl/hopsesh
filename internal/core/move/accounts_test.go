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
