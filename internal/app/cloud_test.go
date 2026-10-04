package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/internal/testkit/fakeagent"
	"github.com/roeehrl/hopsesh/internal/testkit/fakecloud"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// The test binary stands in for the agents' programs when it runs under their names.
func TestMain(m *testing.M) {
	switch strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") {
	case "fakecloud":
		os.Exit(fakeagent.Cloud())
	case "codex":
		os.Exit(fakeagent.Codex())
	case "claude":
		os.Exit(fakeagent.Claude())
	}
	os.Exit(m.Run())
}

// cloudEnv is a home with the stand-in programs on PATH, a fake cloud store and a call log.
func cloudEnv(t *testing.T, programs ...string) (store, log string) {
	t.Helper()
	work := t.TempDir()
	bin := filepath.Join(work, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range programs {
		if runtime.GOOS == "windows" {
			b, _ := os.ReadFile(self)
			if err := os.WriteFile(filepath.Join(bin, name+".exe"), b, 0o700); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.Symlink(self, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	store, log = filepath.Join(work, "cloud"), filepath.Join(work, "agents.log")
	for k, v := range map[string]string{"HOME": work, "USERPROFILE": work, "CLAUDE_CONFIG_DIR": "", "CODEX_HOME": "",
		"HOPSESH_CONFIG_DIR": filepath.Join(work, "config"), "HOPSESH_STATE_DIR": filepath.Join(work, "state"), "HOPSESH_MACHINE": "here",
		"PATH": bin, "FAKE_CLOUD_DIR": store, "FAKE_AGENT_LOG": log, "FAKE_CLOUD_FAIL": ""} {
		t.Setenv(k, v)
	}
	return store, log
}

func cloudApp(t *testing.T, reg *registry.Registry, allowed ...string) *App {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range allowed {
		cfg.SetCloudAllowed(c, true)
	}
	return New(cfg, reg, config.StateDir(), nil)
}

func calls(log string) string {
	b, _ := os.ReadFile(log)
	return string(b)
}

// An allowed cloud is listed through its module, in parallel with the machines; its
// sessions are entries at the cloud's location.
func TestScanListsAllowedCloud(t *testing.T) {
	store, log := cloudEnv(t, "fakecloud")
	seeded, err := fakecloud.Open(store).Seed(fakecloud.Session{Cloud: fakecloud.FakeCloud, Title: "Fix the parser", Repo: "github.com/example/demo",
		Branch: "hopsesh/handoff/x", State: fakecloud.StateDone})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := registry.New(fakecloud.Stub())
	if err != nil {
		t.Fatal(err)
	}
	inv := cloudApp(t, reg, fakecloud.FakeCloud).Scan(context.Background(), ScanOptions{SkipGit: true})
	defer inv.Close()
	c := inv.Cloud(fakecloud.FakeCloud)
	if c == nil || c.Status != CloudReady || !c.Listable || c.Sessions != 1 || c.Version != "1.0.0" || !c.Tested {
		t.Fatalf("cloud: %+v", c)
	}
	var e *Entry
	for i := range inv.Entries {
		if inv.Entries[i].Location.IsCloud() {
			e = &inv.Entries[i]
		}
	}
	if e == nil || e.Location.Name != fakecloud.FakeCloud || e.Machine != "" || e.Cloud == nil || string(e.Session.Key.Session) != seeded.ID ||
		e.Session.Title != "Fix the parser" || e.Cloud.State != agent.CloudDone {
		t.Fatalf("cloud entry: %+v", e)
	}
	if m := inv.Local(); m == nil || m.Kind != agent.AtMachine {
		t.Fatalf("this machine: %+v", m)
	}
	if !strings.Contains(calls(log), "fakecloud remote list") {
		t.Fatal("the listing runs the cloud's driver")
	}
}

// A cloud the user has not allowed is reported and never run; a cloud named in Hosts is
// scanned alone.
func TestScanLeavesCloudAlone(t *testing.T) {
	_, log := cloudEnv(t, "fakecloud")
	reg, _ := registry.New(fakecloud.Stub())
	inv := cloudApp(t, reg).Scan(context.Background(), ScanOptions{SkipGit: true})
	if c := inv.Cloud(fakecloud.FakeCloud); c == nil || c.Status != CloudNotAllowed || c.Sessions != 0 {
		t.Fatalf("cloud: %+v", c)
	}
	if strings.Contains(calls(log), "remote list") {
		t.Fatal("a cloud that is not allowed must not be run")
	}
	inv = cloudApp(t, reg, fakecloud.FakeCloud).Scan(context.Background(), ScanOptions{Hosts: []string{fakecloud.FakeCloud}, SkipGit: true})
	if len(inv.Machines) != 0 || len(inv.Clouds) != 1 || inv.Clouds[0].Status != CloudReady {
		t.Fatalf("a scan of one cloud: %d machines, %+v", len(inv.Machines), inv.Clouds)
	}
	inv = cloudApp(t, reg, fakecloud.FakeCloud).Scan(context.Background(), ScanOptions{Hosts: []string{"here"}, SkipGit: true})
	if len(inv.Clouds) != 0 || len(inv.Machines) != 1 {
		t.Fatalf("a scan of this machine alone: %+v", inv.Clouds)
	}
}

// The vendor's refusals become statuses with a remedy; a missing driver and a slow cloud
// do too, and a slow cloud never holds up the scan.
func TestScanCloudStatuses(t *testing.T) {
	cloudEnv(t, "fakecloud")
	reg, _ := registry.New(fakecloud.Stub())
	for fail, want := range map[string]string{"signed-out": CloudSignedOut, "not-eligible": CloudNotEligible} {
		t.Setenv("FAKE_CLOUD_FAIL", fail)
		c := cloudApp(t, reg, fakecloud.FakeCloud).Scan(context.Background(), ScanOptions{SkipGit: true}).Cloud(fakecloud.FakeCloud)
		if c.Status != want || c.Hint == "" || c.Error == "" {
			t.Errorf("%s: %+v", fail, c)
		}
	}
	t.Setenv("FAKE_CLOUD_FAIL", "bad-record")
	if c := cloudApp(t, reg, fakecloud.FakeCloud).Scan(context.Background(), ScanOptions{SkipGit: true}).Cloud(fakecloud.FakeCloud); c.Status != CloudReady || !strings.Contains(c.Error, "could not be read") {
		t.Errorf("an unreadable record: %+v", c)
	}
	t.Setenv("FAKE_CLOUD_FAIL", "slow")
	a := cloudApp(t, reg, fakecloud.FakeCloud)
	a.CloudTimeout = 300 * time.Millisecond
	start := time.Now()
	c := a.Scan(context.Background(), ScanOptions{SkipGit: true}).Cloud(fakecloud.FakeCloud)
	if c.Status != CloudError || !strings.Contains(c.Error, "did not answer within") || time.Since(start) > 4*time.Second {
		t.Errorf("a slow cloud: %+v after %s", c, time.Since(start))
	}
	t.Setenv("FAKE_CLOUD_FAIL", "")
	cloudEnv(t) // no programs on PATH
	if c := cloudApp(t, reg, fakecloud.FakeCloud).Scan(context.Background(), ScanOptions{SkipGit: true}).Cloud(fakecloud.FakeCloud); c.Status != CloudCLIMissing || !strings.Contains(c.Hint, "`fakecloud`") {
		t.Errorf("no driver: %+v", c)
	}
}

// Claude Code's and Codex's clouds are declared but not listable yet: off until allowed,
// then ready with no sessions when the driver is here, and nothing is run.
func TestScanDeclaredClouds(t *testing.T) {
	_, log := cloudEnv(t, "codex")
	inv := cloudApp(t, all.Registry(), "codex-cloud", "claude-cloud").Scan(context.Background(), ScanOptions{SkipGit: true})
	codex, claude := inv.Cloud("codex-cloud"), inv.Cloud("claude-cloud")
	if codex == nil || codex.Status != CloudReady || codex.Listable || codex.Sessions != 0 || codex.Version != "0.153.2" || !codex.Tested || codex.Title != "Codex cloud" {
		t.Fatalf("codex-cloud: %+v", codex)
	}
	// claude is not on PATH, but may be in one of its usual folders on this machine.
	if claude == nil || claude.Status == CloudNotAllowed || claude.Status == CloudError || claude.Listable {
		t.Fatalf("claude-cloud: %+v", claude)
	}
	if strings.Contains(calls(log), "cloud") {
		t.Fatalf("no cloud verb may run: %s", calls(log))
	}
	if c := cloudApp(t, all.Registry()).Scan(context.Background(), ScanOptions{SkipGit: true}).Cloud("codex-cloud"); c.Status != CloudNotAllowed {
		t.Fatalf("off until allowed: %+v", c)
	}
}

// The cloud copies the lineage records are passed to the cloud's listing as Known.
func TestKnownCloudIDs(t *testing.T) {
	m := lineage.New("L")
	m.Upsert(lineage.Replica{Key: agent.SessionKey{Agent: "claude", Session: "a"}, Location: "here"})
	m.Upsert(lineage.Replica{Key: agent.SessionKey{Agent: "claude", Session: "session_01x"}, Location: "claude-cloud"})
	m.Upsert(lineage.Replica{Key: agent.SessionKey{Agent: "codex", Session: "task_e_1"}, Location: "codex-cloud"})
	m.Upsert(lineage.Replica{Key: agent.SessionKey{Agent: "codex", Session: "nope"}, Location: "claude-cloud"}) // not claude's
	reg := all.Registry()
	var refs []cloudRef
	for _, mod := range reg.All() {
		for _, c := range mod.Spec().Clouds {
			refs = append(refs, cloudRef{mod, c})
		}
	}
	got := knownCloudIDs([]Entry{{Lineage: m}, {Lineage: m}, {}}, refs)
	if len(got["claude-cloud"]) != 1 || got["claude-cloud"][0] != "session_01x" || len(got["codex-cloud"]) != 1 {
		t.Fatalf("%v", got)
	}
}

// Undo reaches a cloud through its module: activity since the handoff refuses it, and an
// archive runs where the module can.
func TestUndoArchivesCloudSession(t *testing.T) {
	store, _ := cloudEnv(t, "fakecloud")
	s, err := fakecloud.Open(store).Seed(fakecloud.Session{Cloud: fakecloud.FakeCloud, Title: "handed off", Repo: "github.com/example/demo"})
	if err != nil {
		t.Fatal(err)
	}
	reg, _ := registry.New(fakecloud.Stub())
	a := cloudApp(t, reg, fakecloud.FakeCloud)
	j, err := journal.New(a.StateDir, journal.KindHandoff, "hand off")
	if err != nil {
		t.Fatal(err)
	}
	key := agent.SessionKey{Agent: "fakecloud", Session: agent.SessionID(s.ID)}
	if err := j.Cloud("here", agent.CloudSession{Key: key, Cloud: fakecloud.FakeCloud, Updated: s.Updated.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	j.AddKey(key)
	if _, err := a.Undo(context.Background(), s.ID, false); err == nil || !strings.Contains(err.Error(), "new activity") {
		t.Fatalf("activity since the handoff must refuse undo: %v", err)
	}
	if _, err := a.Undo(context.Background(), s.ID, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := fakecloud.Open(store).Get(s.ID); got.State != fakecloud.StateArchived {
		t.Fatalf("the module archives it: %s", got.State)
	}
}
