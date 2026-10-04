package gui

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/testkit/fakeagent"
)

// The test binary is the stand-in claude when it runs under that name.
func TestMain(m *testing.M) {
	if strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") == "claude" {
		os.Exit(fakeagent.Claude())
	}
	os.Exit(m.Run())
}

// The window's cloud path up to the plan: the cloud is off until allowed, a pasted link
// becomes a row of its cloud, the Machines page has its card, a test probes it read-only,
// and planning it is a fetch into a new worktree with the agent's own command.
func TestCloudInTheWindow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in claude is linked in, which needs a POSIX system here")
	}
	repo := home(t)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"remote", "add", "origin", "https://github.com/example/demo.git"},
		{"-c", "user.name=a", "-c", "user.email=a@example.com", "commit", "-q", "--allow-empty", "-m", "x"}} {
		if out, err := osexec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	bin := filepath.Join(t.TempDir(), "bin")
	os.MkdirAll(bin, 0o700)
	self, _ := os.Executable()
	if err := os.Symlink(self, filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+testPath())
	a := NewApp(all.Registry())
	scan, err := a.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Clouds) != 1 || scan.Clouds[0].Name != "claude-cloud" || scan.Clouds[0].Allowed || scan.Clouds[0].Status != "not-allowed" {
		t.Fatalf("clouds before consent: %+v", scan.Clouds)
	}
	if err := a.SetCloudAllowed("claude-cloud", true); err != nil {
		t.Fatal(err)
	}
	p, err := a.PasteCloud("https://claude.ai/code/session_01PastedAbc123", repo)
	if err != nil || p.Cloud != "claude-cloud" || p.Key != "claude/session_01PastedAbc123" {
		t.Fatalf("paste: %+v %v", p, err)
	}
	if scan, err = a.Scan(); err != nil {
		t.Fatal(err)
	}
	c := scan.Clouds[0]
	if c.Status != "ready" || !c.Partial || c.Sessions != 1 || !c.Rename || c.VendorPrefix != "claude/" || c.Fidelity != "native" {
		t.Fatalf("cloud: %+v", c)
	}
	var row *EntryDTO
	for _, g := range scan.Groups {
		for i, e := range g.Entries {
			if e.Cloud != nil {
				row = &g.Entries[i]
			}
		}
	}
	if row == nil || row.Machine != "claude-cloud" || row.Location != "cloud" || row.Cloud.ID != "session_01PastedAbc123" || row.Cloud.URL == "" || row.Cloud.Checkout == "" {
		t.Fatalf("cloud row: %+v", row)
	}
	if m := a.Machines(); len(m.Clouds) != 1 || !m.Clouds[0].Allowed {
		t.Fatalf("machines: %+v", m.Clouds)
	}
	ct, err := a.TestCloud("claude-cloud")
	if err != nil || !ct.OK || ct.Account != "claude.ai · max" {
		t.Fatalf("test: %+v %v", ct, err)
	}
	plan, err := a.Plan(row.Machine, row.Key, "", OptsDTO{})
	if err != nil || plan.Kind != "fetch" || plan.Fetch == nil || len(plan.Blockers) > 0 || !strings.Contains(plan.Fetch.Command, "claude --teleport session_01PastedAbc123") {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	core := a.snapshot()
	if !cloudPage(core, row.Cloud.URL) || !cloudPage(core, "https://github.com/anthropics/claude-code/issues/94836") {
		t.Fatal("a cloud session's page and a known problem may be opened")
	}
	if cloudPage(core, "https://example.com/") || cloudPage(core, "https://claude.ai/settings") {
		t.Fatal("another page may not be opened")
	}
}
