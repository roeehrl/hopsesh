package host

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/agents/opencode"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// The Codex app's bundled codex is a candidate after every CLI location, on local and
// remote Macs alike; HOPSESH_APP_RUNTIMES=off (test machines) leaves it out.
func TestCandidatesIncludeAppRuntimeLast(t *testing.T) {
	b := codex.New().Spec().Binaries[0]
	t.Setenv("HOPSESH_APP_RUNTIMES", "on")
	c := candidates(b, "darwin")
	i := slices.IndexFunc(c, func(p string) bool { return strings.Contains(p, ".app/") })
	if i < 0 || slices.ContainsFunc(c[i:], func(p string) bool { return !strings.Contains(p, ".app/") }) {
		t.Fatalf("bundled runtime must follow every CLI candidate: %v", c)
	}
	if !strings.Contains(posixProbe(probeWants{bins: []agent.Binary{b}}), "/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex") {
		t.Fatal("remote probe misses the bundled runtime")
	}
	t.Setenv("HOPSESH_APP_RUNTIMES", "off")
	if slices.ContainsFunc(candidates(b, "darwin"), func(p string) bool { return strings.Contains(p, ".app/") || !strings.HasPrefix(p, "~/") }) {
		t.Fatal("app runtime or a system folder probed while off")
	}
	if len(b.Candidates["*"]) != len(c) {
		t.Fatal("filtering changed the Spec")
	}
}

// A test never finds this machine's real agents: in a test binary, with nothing set, only
// the candidates under the home folder are looked at (and the remote probe looks at no
// others), unless the test runs against the installed agents.
func TestCandidatesInATestStayInHome(t *testing.T) {
	t.Setenv("HOPSESH_APP_RUNTIMES", "")
	t.Setenv("HOPSESH_REAL_AGENTS", "")
	for _, m := range []agent.Module{codex.New(), opencode.New()} {
		b := m.Spec().Binaries[0]
		c := candidates(b, "darwin")
		if len(c) == 0 || slices.ContainsFunc(c, func(p string) bool { return !strings.HasPrefix(p, "~/") }) {
			t.Fatalf("%s: %v", b.Name, c)
		}
		if p := posixProbe(probeWants{bins: []agent.Binary{b}}); strings.Contains(p, "/opt/homebrew/bin/") || strings.Contains(p, ".app/") {
			t.Fatalf("%s: the remote probe looks in system folders: %s", b.Name, p)
		}
	}
	t.Setenv("HOPSESH_REAL_AGENTS", "1")
	if c := candidates(opencode.New().Spec().Binaries[0], "darwin"); !slices.Contains(c, "/opt/homebrew/bin/opencode") {
		t.Fatalf("a real-agents test must find the installed agents: %v", c)
	}
}

// Every test binary marks itself a test machine, so a hopsesh it starts never finds this
// machine's real agents either.
func TestTestBinaryMarksItsChildren(t *testing.T) {
	if os.Getenv("HOPSESH_REAL_AGENTS") != "1" && os.Getenv("HOPSESH_APP_RUNTIMES") != "off" {
		t.Fatalf("HOPSESH_APP_RUNTIMES = %q in a test binary", os.Getenv("HOPSESH_APP_RUNTIMES"))
	}
}
