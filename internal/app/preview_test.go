package app

import (
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestUnderRootAcceptsAnySpecRoot(t *testing.T) {
	spec := agent.Spec{ID: "x", Roots: []agent.Root{{Name: "data"}, {Name: "state"}}}
	in := agent.Install{Roots: map[string]string{"data": "/home/u/.local/share/x", "state": "/home/u/.local/state/x"}, MainRoot: "data"}
	pa := agent.PosixPath{}
	for path, want := range map[string]bool{
		"/home/u/.local/share/x/s/1.jsonl":   true,
		"/home/u/.local/state/x/s/1.jsonl":   true,
		"/home/u/.local/share/other/1.jsonl": false,
		"/home/u/.local/share/x/../y/1.json": false,
		"/etc/passwd":                        false,
	} {
		if got := underRoot(pa, spec, in, path); got != want {
			t.Errorf("underRoot(%q) = %v, want %v", path, got, want)
		}
	}
	// A root the install does not resolve is no root.
	if underRoot(pa, spec, agent.Install{Roots: map[string]string{"state": ""}}, "/x") {
		t.Fatal("an empty root matched")
	}
}
