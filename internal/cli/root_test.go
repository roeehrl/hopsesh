package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := NewRoot(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestVersion(t *testing.T) {
	out, err := run(t, "version")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "hopsesh ") {
		t.Fatalf("unexpected version output %q", out)
	}
}

func TestCommandSurface(t *testing.T) {
	root := NewRoot(&bytes.Buffer{})
	for _, name := range []string{"hosts", "trust", "doctor", "ls", "show", "pull", "import", "undo", "agent", "version", "update", "plan"} {
		if c, _, err := root.Find([]string{name}); err != nil || c.Name() != name {
			t.Errorf("missing command %s", name)
		}
	}
	_ = errors.Is
}

func TestHelpMentionsDisclaimer(t *testing.T) {
	out, err := run(t, "--help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "not affiliated") {
		t.Fatal("help text must carry the unofficial / not-affiliated disclaimer")
	}
}
