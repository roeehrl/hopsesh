package codex

import (
	"reflect"
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// A thread resumed in a tab hopsesh owns runs with its own server, unless an endpoint is
// set where the tab would inherit it.
func TestOwnedTabCommandNoDaemon(t *testing.T) {
	m := New()
	in := agent.Install{Agent: "codex", Version: "0.160.1", Binary: "/bin/codex"}
	c := agent.Command{Argv: []string{"/bin/codex", "resume", "id"}}
	none := func(string) string { return "" }
	got := m.OwnedTabCommand(in, c, none)
	if want := []string{"/bin/codex", "resume", "--no-daemon", "id"}; !reflect.DeepEqual(got.Argv, want) {
		t.Fatalf("argv %v", got.Argv)
	}
	if again := m.OwnedTabCommand(in, got, none); !reflect.DeepEqual(again, got) {
		t.Fatal("applied twice")
	}
	endpoint := func(k string) string {
		if k == "CODEX_APP_SERVER_ADDRESS" {
			return "unix:///x"
		}
		return ""
	}
	if got := m.OwnedTabCommand(in, c, endpoint); !reflect.DeepEqual(got.Argv, c.Argv) {
		t.Fatalf("an inherited endpoint is kept: %v", got.Argv)
	}
	unset := c
	unset.Unset = []string{"CODEX_APP_SERVER_ADDRESS"}
	if got := m.OwnedTabCommand(in, unset, endpoint); len(got.Argv) != 4 {
		t.Fatalf("an endpoint the command unsets does not count: %v", got.Argv)
	}
	old := in
	old.Version = "0.155.0"
	if got := m.OwnedTabCommand(old, c, none); !reflect.DeepEqual(got.Argv, c.Argv) {
		t.Fatalf("older Codex: %v", got.Argv)
	}
}
