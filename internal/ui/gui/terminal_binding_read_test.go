package gui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/presence"
	"github.com/roeehrl/hopsesh/internal/core/pty"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestCheckingSessionRoutingRequiresFreshProof(t *testing.T) {
	a, mod, info, entry := presencePTYFixture(t)
	mod.mu.Lock()
	mod.pid = info.PID
	mod.mu.Unlock()
	if _, err := a.Presence(); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	owned := a.inv
	owned.Entries[0].Live = agent.LiveInfo{State: agent.Unknown}
	a.mu.Unlock()
	a.bindTerminalSessions(owned)
	checking := a.TerminalTabs()[0]
	if checking.Association != associationChecking {
		t.Fatal(checking)
	}
	key := entry.Session.Key.String()
	if id := a.Terms.liveSessionTab(entry.Machine, key); id != "" {
		t.Fatal("last-known session remains a focus target")
	}
	for _, where := range []string{"", WhereHere, WhereTerminal} {
		if opened, err := a.ResumeSession(entry.Machine, key, where); opened != nil || !errors.Is(err, errCheckingSession) {
			t.Fatalf("ResumeSession %q: %+v %v", where, opened, err)
		}
	}
	if shown, err := a.ShowEntry(entry.Machine, key); shown != "" || !errors.Is(err, errCheckingSession) {
		t.Fatalf("ShowEntry: %q %v", shown, err)
	}
	if tab, err := a.EntryTab(entry.Machine, key); tab != nil || !errors.Is(err, errCheckingSession) {
		t.Fatalf("EntryTab: %+v %v", tab, err)
	}
	if err := a.ResumeEntry(entry.Machine, key, false); !errors.Is(err, errCheckingSession) {
		t.Fatalf("ResumeEntry: %v", err)
	}
	a.mu.Lock()
	a.plan = &move.Plan{Placement: agent.Placement{Key: entry.Session.Key}}
	a.res = &move.Result{Command: "must not open", Run: agent.Command{Argv: []string{"must-not-launch"}}}
	a.mu.Unlock()
	for _, where := range []string{WhereHere, WhereTerminal} {
		if opened, err := a.OpenResult(where); opened != nil || !errors.Is(err, errCheckingSession) {
			t.Fatalf("OpenResult %q: %+v %v", where, opened, err)
		}
	}
	a.mu.Lock()
	a.plan.Options.App = true
	a.mu.Unlock()
	if opened, err := a.OpenResult(""); opened != nil || !errors.Is(err, errCheckingSession) {
		t.Fatalf("OpenResult app: %+v %v", opened, err)
	}
	a.mu.Lock()
	a.plan, a.res = nil, nil
	a.mu.Unlock()
	// Rerun of a different, ended tab must not remove/relaunch it while the
	// same session has an uncertain owned client. This uses only a test helper.
	a.Terms.AutoClose = func() bool { return false }
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ended, err := a.Terms.Open(pty.Spec{Argv: []string{self, "-test.run=^TestPresencePTYHelper$"}, Dir: info.Dir, Env: pty.Env{Set: []string{"HOPSESH_PRESENCE_HELPER=0"}}}, TabSetup{Background: true, Meta: TabMeta{Kind: TabSession, Machine: entry.Machine, Key: key, Rerun: true}})
	if err != nil {
		t.Fatal(err)
	}
	session, _ := a.Terms.Manager().Get(ended.ID)
	if _, err := session.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	tb, _ := a.Terms.tabOf(ended.ID)
	a.Terms.rerun(ended.ID, tb)
	if _, ok := a.Terms.Manager().Get(ended.ID); !ok {
		t.Fatal("rerun replaced the ended tab despite an uncertain duplicate")
	}
	if len(a.TerminalTabs()) != 2 {
		t.Fatal("an entry point created another client")
	}
	if tab := a.TerminalTabs()[0]; tab.ID == info.ID && (tab.PID != info.PID || !tab.Started.Equal(info.Started)) {
		t.Fatal("owned lifetime changed")
	}
	// /new's last-known key and the original launch both block uncertain routing.
	a.Terms.setMeta(info.ID, func(m *TabMeta) { m.Key = "codex@" + entry.Session.Key.Profile + "/last-known-new" })
	if _, err := a.Terms.sessionTabRoute(entry.Machine, key); !errors.Is(err, errCheckingSession) {
		t.Fatal("original launch bypasses the uncertain duplicate guard")
	}
	a.Terms.setMeta(info.ID, func(m *TabMeta) { m.Key = key })
	if err := a.Terms.Manager().Close(ended.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Presence(); err != nil {
		t.Fatal(err)
	}
	if id, err := a.Terms.sessionTabRoute(entry.Machine, key); id != info.ID || err != nil {
		t.Fatalf("fresh native verification did not restore routing: %q %v", id, err)
	}
}

func TestTerminalBindingCheckingKeepsOnlyLastVerifiedIdentity(t *testing.T) {
	a, mod, info, e := presencePTYFixture(t)
	owned := a.inv
	mod.pid = info.PID
	a.Terms.setMeta(info.ID, func(m *TabMeta) { m.External, m.Rerun = true, true })
	if _, err := a.Presence(); err != nil {
		t.Fatal(err)
	}
	confirmed := a.TerminalTabs()[0]
	if confirmed.Association != "Confirmed by native process registry" {
		t.Fatal(confirmed)
	}
	// Full/local scans can publish cached/failed Live evidence. Exercise their real
	// binding caller, not launch argv or a renderer-only metadata modification.
	a.mu.Lock()
	owned.Discovering = true
	owned.Entries[0].Live = agent.LiveInfo{State: agent.Unknown}
	a.mu.Unlock()
	a.bindTerminalSessions(owned)
	checking := a.TerminalTabs()[0]
	if checking.Association != "Checking session association" || checking.Key != confirmed.Key || checking.Relationship == nil || checking.External || checking.Rerun || checking.PID != info.PID || !checking.Started.Equal(info.Started) {
		t.Fatalf("provisional scan erased verified identity: %+v", checking)
	}
	var wire struct{ Tabs []TermTab }
	if err := json.Unmarshal(a.Terms.listMessage(), &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Tabs) != 1 || wire.Tabs[0].Association != "Checking session association" {
		t.Fatalf("terminal stream lost checking state: %+v", wire)
	}
	if _, err := a.Presence(); err != nil {
		t.Fatal(err)
	}
	if tab := a.TerminalTabs()[0]; tab.Association != "Confirmed by native process registry" || !tab.External || !tab.Rerun {
		t.Fatal(tab)
	}
	// A definitive end removes the association even during discovery.
	a.mu.Lock()
	owned.Entries[0].Live = agent.LiveInfo{State: agent.Ended}
	a.mu.Unlock()
	a.bindTerminalSessions(owned)
	if tab := a.TerminalTabs()[0]; tab.Association != "Session association not confirmed" || tab.Relationship != nil {
		t.Fatalf("ended evidence was retained: %+v", tab)
	}
	// Unknown after an end cannot resurrect proof from the original launch.
	a.mu.Lock()
	owned.Entries[0].Live = agent.LiveInfo{State: agent.Unknown}
	a.mu.Unlock()
	a.bindTerminalSessions(owned)
	if tab := a.TerminalTabs()[0]; tab.Association != "Session association not confirmed" || tab.Relationship != nil {
		t.Fatal("unknown promoted launch intent")
	}
	// Native /new is bound by fresh process evidence, never the original key.
	table, err := presence.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	fork := e
	fork.Session.Key.Session = "native-new"
	fork.Live = agent.LiveInfo{State: agent.Live, Procs: []agent.LiveProc{{PID: info.PID, ObservedAt: time.Now().UTC()}}}
	a.bindTerminalSnapshot(owned, &app.Inventory{Entries: []app.Entry{fork}}, table)
	tab := a.TerminalTabs()[0]
	if tab.Key != fork.Session.Key.String() || tab.LaunchedKey != e.Session.Key.String() || tab.External || tab.Rerun {
		t.Fatalf("native switch was lost: %+v", tab)
	}
	// An ambiguous native switch clears proof even if the previous key was checking.
	ambiguous := fork
	ambiguous.Session.Key.Session = "second-owned-thread"
	a.bindTerminalSnapshot(owned, &app.Inventory{Entries: []app.Entry{fork, ambiguous}}, table)
	if tab := a.TerminalTabs()[0]; tab.Association != "Session association not confirmed" || tab.Relationship != nil {
		t.Fatal("ambiguous native evidence retained proof")
	}
	a.bindTerminalSnapshot(owned, &app.Inventory{Entries: []app.Entry{fork}}, table)
	wrong := fork
	wrong.Session.Key.Profile = "wrong-profile"
	a.bindTerminalSnapshot(owned, &app.Inventory{Entries: []app.Entry{wrong}}, table)
	if tab := a.TerminalTabs()[0]; tab.Association != "Session association not confirmed" || tab.Relationship != nil {
		t.Fatal("wrong namespace retained confirmation")
	}
}

func TestTerminalBindingRejectsOlderPresenceAndScanResults(t *testing.T) {
	for _, source := range []string{"presence", "scan"} {
		t.Run(source, func(t *testing.T) {
			a, mod, info, _ := presencePTYFixture(t)
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			mod.mu.Lock()
			mod.state = agent.Ended
			mod.onLive = func() { close(entered); <-release }
			mod.mu.Unlock()
			go func() {
				var err error
				if source == "scan" {
					_, err = a.RefreshHere()
				} else {
					_, err = a.Presence()
				}
				done <- err
			}()
			<-entered // old read has captured Ended before the native PID arrives
			mod.mu.Lock()
			mod.state, mod.pid, mod.onLive = agent.Live, info.PID, nil
			mod.mu.Unlock()
			if _, err := a.Presence(); err != nil {
				t.Fatal(err)
			}
			confirmed := a.TerminalTabs()[0]
			if confirmed.Association != "Confirmed by native process registry" {
				t.Fatal(confirmed)
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			tab := a.TerminalTabs()[0]
			if tab.Association != confirmed.Association || tab.Key != confirmed.Key || tab.Relationship == nil || tab.PID != info.PID || !tab.Started.Equal(info.Started) {
				t.Fatalf("older %s read demoted newer native confirmation: %+v", source, tab)
			}
			var wire struct{ Tabs []TermTab }
			if err := json.Unmarshal(a.Terms.listMessage(), &wire); err != nil {
				t.Fatal(err)
			}
			if len(wire.Tabs) != 1 || wire.Tabs[0].Association != confirmed.Association {
				t.Fatal("terminal list emitted the older binding")
			}
		})
	}
}

func TestTerminalMetadataRevisionOrdersConcurrentEmits(t *testing.T) {
	a, mod, info, _ := presencePTYFixture(t)
	a.Terms.mu.Lock()
	a.Terms.tabs[info.ID].meta.Association = "Session association not confirmed"
	a.Terms.mu.Unlock()
	entered, release, oldDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	emitted := make(chan TermTab, 2)
	a.Terms.Emit = func(name string, data any) {
		if name != TerminalEvent {
			return
		}
		tab := data.(TermTab)
		if tab.Association == "Session association not confirmed" {
			close(entered)
			<-release
		}
		emitted <- tab
	}
	go func() { defer close(oldDone); a.Terms.changed(info) }()
	<-entered // old metadata was captured, but its event has not been delivered
	mod.mu.Lock()
	mod.pid = info.PID
	mod.mu.Unlock()
	if _, err := a.Presence(); err != nil {
		t.Fatal(err)
	}
	newer := <-emitted
	if newer.Association != "Confirmed by native process registry" || newer.Revision == 0 {
		t.Fatal(newer)
	}
	close(release)
	older := <-emitted
	<-oldDone
	if older.Revision >= newer.Revision {
		t.Fatalf("late old event masquerades as newer: old=%d new=%d", older.Revision, newer.Revision)
	}
	current := a.TerminalTabs()[0]
	if current.Association != newer.Association || current.Revision < newer.Revision {
		t.Fatal("startup RPC snapshot is older than the committed metadata")
	}
	// Stop observing cleanup events; the closed-tab event belongs to cleanup.
	a.Terms.Emit = nil
}
