package host

import (
	"slices"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// The Codex app's bundled codex is a candidate after every CLI location, on local and
// remote Macs alike; HOPSESH_APP_RUNTIMES=off (test machines) leaves it out.
func TestCandidatesIncludeAppRuntimeLast(t *testing.T) {
	b := codex.New().Spec().Binaries[0]
	t.Setenv("HOPSESH_APP_RUNTIMES", "")
	c := candidates(b, "darwin")
	i := slices.IndexFunc(c, func(p string) bool { return strings.Contains(p, ".app/") })
	if i < 0 || slices.ContainsFunc(c[i:], func(p string) bool { return !strings.Contains(p, ".app/") }) {
		t.Fatalf("bundled runtime must follow every CLI candidate: %v", c)
	}
	if !strings.Contains(posixProbe(probeWants{bins: []agent.Binary{b}}), "/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex") {
		t.Fatal("remote probe misses the bundled runtime")
	}
	t.Setenv("HOPSESH_APP_RUNTIMES", "off")
	if slices.ContainsFunc(candidates(b, "darwin"), func(p string) bool { return strings.Contains(p, ".app/") }) {
		t.Fatal("app runtime probed while off")
	}
	if len(b.Candidates["*"]) != len(c) {
		t.Fatal("filtering changed the Spec")
	}
}
