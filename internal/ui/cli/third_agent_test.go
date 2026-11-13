package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/internal/testkit/thirdagent"
)

// The help names every compiled-in local agent, a third one included.
func TestHelpNamesThirdAgent(t *testing.T) {
	reg, err := registry.New(claude.New(), codex.New(), thirdagent.New())
	if err != nil {
		t.Fatal(err)
	}
	prev := modules
	t.Cleanup(func() { modules = prev })
	root := NewRoot(io.Discard, reg)
	for _, want := range []string{"(Claude Code, Codex, Third Agent)", "Anthropic, OpenAI or Example."} {
		if !strings.Contains(root.Long, want) {
			t.Errorf("root help lacks %q", want)
		}
	}
	accounts, _, err := root.Find([]string{"accounts"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(accounts.Short, "Claude Code, Codex and Third Agent") {
		t.Errorf("accounts: %q", accounts.Short)
	}
	add, _, err := root.Find([]string{"accounts", "add"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(add.Use, "add <claude|codex|third>") {
		t.Errorf("accounts add: %q", add.Use)
	}
}
