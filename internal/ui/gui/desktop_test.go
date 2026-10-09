package gui

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/ui/desktop"
)

type fakeDesktop struct {
	mu    sync.Mutex
	s     desktop.State
	fail  bool
	opens int
	attn  int
}

func (f *fakeDesktop) Snapshot() desktop.State { f.mu.Lock(); defer f.mu.Unlock(); return f.s }
func (f *fakeDesktop) Apply(p config.Desktop, login bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("native failure")
	}
	if err := desktop.Validate(p, f.s.Capabilities); err != nil {
		return err
	}
	f.s.Preferences = p
	f.s.Login = login
	f.s.Effective = desktop.Effective(p, f.s.Capabilities)
	return nil
}
func (f *fakeDesktop) Recheck() {}
func (f *fakeDesktop) KeepOnClose() bool {
	s := f.Snapshot()
	return desktop.KeepOnClose(s.Preferences)
}
func (f *fakeDesktop) OpenMain()             { f.mu.Lock(); defer f.mu.Unlock(); f.opens++ }
func (f *fakeDesktop) CloseQuick()           {}
func (f *fakeDesktop) SetAttention(n, _ int) { f.mu.Lock(); defer f.mu.Unlock(); f.attn = n }
func (f *fakeDesktop) Stop()                 {}
func desktopApp(t *testing.T) (*App, *fakeDesktop) {
	t.Helper()
	home(t)
	a := NewApp(all.Registry())
	t.Cleanup(func() { _ = a.core.Catalog.Close() })
	f := &fakeDesktop{s: desktop.State{Preferences: a.snapshot().Cfg.Desktop, Capabilities: desktop.Capabilities{Tray: true, HideApp: true}, Effective: "both"}}
	a.Desktop = f
	t.Cleanup(a.Shutdown)
	return a, f
}
func TestDesktopPreferencesAndClose(t *testing.T) {
	a, f := desktopApp(t)
	in := DesktopInput{Mode: "tray", Close: "keep", Attention: true, Previews: false}
	if err := a.SaveDesktop(in); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load()
	if err != nil || c.Desktop.Placement() != "tray" || c.Desktop.PreviewsOn() {
		t.Fatalf("save: %+v %v", c.Desktop, err)
	}
	if cancel, hide := a.MainClosing(); !cancel || !hide {
		t.Fatal("keep-running closes the main window")
	}
	if p, err := a.QuickPreview("invalid", "invalid"); err != nil || len(p.Items) != 0 {
		t.Fatalf("privacy must not read a session: %+v %v", p, err)
	}
	f.fail = true
	in.Mode = "app"
	if err = a.SaveDesktop(in); err == nil {
		t.Fatal("native error ignored")
	}
	if a.snapshot().Cfg.Desktop.Placement() != "tray" {
		t.Fatal("persisted unapplied mode")
	}
}
func TestQuickRefreshFirstScanStaysLocal(t *testing.T) {
	a, _ := desktopApp(t)
	a.mu.Lock()
	a.core.Cfg.Hosts = []config.Host{{Name: "remote-must-not-connect", Destination: "unreachable.invalid", Allowed: true, Auth: "password"}}
	a.mu.Unlock()
	a.refreshQuick()
	d := a.QuickSnapshot()
	if d.Error != "" || d.Scan == nil {
		t.Fatalf("refresh: %+v", d)
	}
	for _, m := range d.Scan.Machines {
		if !m.Local {
			t.Fatalf("background first scan contacted remote: %+v", m)
		}
	}
	if !a.invAt.IsZero() {
		t.Fatal("local refresh claims remote freshness")
	}
}

func TestQuickPublicationRevisionDoesNotDependOnTimestamp(t *testing.T) {
	a, _ := desktopApp(t)
	first := &ScanDTO{Updated: "2026-10-08T19:00:00Z"}
	second := &ScanDTO{Updated: first.Updated}
	a.publishQuick(first)
	before := first.Revision
	a.publishQuick(second)
	if before == 0 || first.Revision != before || second.Revision <= before || a.QuickSnapshot().Scan.Revision != second.Revision {
		t.Fatal("scan publications are not immutable and ordered within a second")
	}
}
func TestDiscoveryAndRuntimeUseOnePublicationOrder(t *testing.T) {
	a, _ := desktopApp(t)
	first := a.CachedScan()
	snapshot := a.ScanSnapshot()
	if snapshot.Revision <= first.Revision {
		t.Fatal("snapshot did not advance publication order")
	}
	runtime := &ScanDTO{}
	a.publishQuick(runtime)
	if runtime.Revision <= snapshot.Revision {
		t.Fatal("runtime publication restarted the revision clock")
	}
	a.publishQuick(snapshot)
	if a.QuickSnapshot().Scan != runtime {
		t.Fatal("late discovery replaced the newer runtime publication")
	}
	if snapshot.Revision >= runtime.Revision {
		t.Fatal("publication mutated an older visible DTO")
	}
}

func TestQuickRouteSurvivesColdWindow(t *testing.T) {
	a, f := desktopApp(t)
	d, err := a.RefreshHere()
	if err != nil {
		t.Fatal(err)
	}
	e := d.Groups[0].Entries[0]
	if err = a.QuickOpen("sessions", e.Machine, e.Key); err != nil {
		t.Fatal(err)
	}
	r := a.TakeQuickRoute()
	if r == nil || r.Key != e.Key || r.Machine != e.Machine || f.opens != 1 {
		t.Fatalf("route %+v", r)
	}
	if a.TakeQuickRoute() != nil {
		t.Fatal("route delivered twice")
	}
	if err = a.QuickOpen("sessions", e.Machine, "missing"); err == nil {
		t.Fatal("stale selection accepted")
	}
	if len(a.TerminalTabs()) != 0 {
		t.Fatal("selection launched a terminal")
	}
}
func TestQuickAttentionLocalOnly(t *testing.T) {
	a, f := desktopApp(t)
	a.publishQuick(&ScanDTO{Machines: []MachineDTO{{Name: "here", Local: true}, {Name: "remote"}}, Groups: []GroupDTO{{Entries: []EntryDTO{{Machine: "here", Key: "a", Needs: true}, {Machine: "remote", Key: "b", Needs: true}}}}})
	if f.attn != 1 {
		t.Fatalf("remote stale status counted as live attention: %d", f.attn)
	}
}

func TestDesktopSaveFailureRollsBack(t *testing.T) {
	a, f := desktopApp(t)
	before := f.Snapshot()
	// Make the config filename a directory: native success must not leave an
	// unpersisted preference after the atomic config write fails.
	if err := os.MkdirAll(filepath.Join(config.Dir(), "config.toml"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveDesktop(DesktopInput{Mode: "tray", Close: "keep", Attention: false, Previews: false}); err == nil {
		t.Fatal("save unexpectedly succeeded")
	}
	after := f.Snapshot()
	if after.Preferences.Placement() != before.Preferences.Placement() || after.Login != before.Login {
		t.Fatal("native preferences were not rolled back")
	}
	if a.snapshot().Cfg.Desktop.Placement() != before.Preferences.Placement() {
		t.Fatal("memory preferences were not rolled back")
	}
}

func TestBackgroundCloseWithoutTrayOrTerminal(t *testing.T) {
	a, f := desktopApp(t)
	f.s = desktop.State{Preferences: config.Desktop{}, Capabilities: desktop.Capabilities{}, Effective: "app"}
	if a.TerminalRunning() != 0 {
		t.Fatal("expected no terminals")
	}
	if cancel, hide := a.MainClosing(); !cancel || !hide {
		t.Fatal("default close must hide and keep process alive even without a tray")
	}
	if err := a.QuickOpen("sessions", "", ""); err != nil {
		t.Fatal(err)
	}
	if f.opens != 1 {
		t.Fatal("reopen did not route to existing main window")
	}
	f.s.Preferences.Close = "quit"
	if cancel, _ := a.MainClosing(); cancel {
		t.Fatal("explicit Quit choice was ignored")
	}
}
