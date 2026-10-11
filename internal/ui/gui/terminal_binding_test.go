package gui

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/presence"
	"github.com/roeehrl/hopsesh/internal/core/pty"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestRuntimeBindingRequiresOwnedProcessAncestry(t *testing.T) {
	table := presence.Table{10: {PID: 10, PPID: 1}, 11: {PID: 11, PPID: 10}, 12: {PID: 12, PPID: 11}, 20: {PID: 20, PPID: 1}, 30: {PID: 30, PPID: 31}, 31: {PID: 31, PPID: 30}}
	for _, tc := range []struct {
		pid  int
		want bool
	}{{10, true}, {11, true}, {12, true}, {20, false}, {30, false}, {99, false}} {
		if got := descendsFrom(table, tc.pid, 10); got != tc.want {
			t.Errorf("pid %d: %v", tc.pid, got)
		}
	}
}

// An owned test process in a PTY; no native client or real session home is used.
func TestPresencePTYHelper(t *testing.T) {
	if os.Getenv("HOPSESH_PRESENCE_HELPER") == "1" {
		bufio.NewScanner(os.Stdin).Scan()
	}
}

type startupCodex struct {
	*codex.Module
	mu     sync.Mutex
	pid    int
	onLive func()
}

func (m *startupCodex) Live(_ context.Context, _ agent.Host, _ agent.Install, ids []agent.SessionID) (map[agent.SessionID]agent.LiveInfo, error) {
	m.mu.Lock()
	pid, wait := m.pid, m.onLive
	m.mu.Unlock()
	if wait != nil {
		wait()
	}
	out := map[agent.SessionID]agent.LiveInfo{}
	for _, id := range ids {
		li := agent.LiveInfo{State: agent.Live, Status: "idle"}
		if pid > 0 {
			li.PID = pid
			li.Procs = []agent.LiveProc{{PID: pid, ObservedAt: time.Now().UTC()}}
		}
		out[id] = li
	}
	return out, nil
}

func presencePTYFixture(t *testing.T) (*App, *startupCodex, pty.Info, app.Entry) {
	repo := home(t)
	name := "rollout-2026-10-01T13-00-00-01a0fe1c-0000-7000-8000-000000000001.jsonl"
	b, err := os.ReadFile(filepath.Join("../../../agents/codex/testdata/0.153.2/sessions/2026/10/01", name))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(os.Getenv("HOME"), ".codex", "sessions", "2026", "10", "01", name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	b = []byte(strings.ReplaceAll(strings.ReplaceAll(string(b), "/home/u/git/demo", filepath.ToSlash(repo)), "0.153.2", "0.160.1"))
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	mod := &startupCodex{Module: codex.New()}
	reg, err := registry.New(mod)
	if err != nil {
		t.Fatal(err)
	}
	a := NewApp(reg, WithoutSessionWatching())
	t.Cleanup(a.Shutdown)
	if _, err := a.Scan(); err != nil {
		t.Fatal(err)
	}
	if len(a.inv.Entries) != 1 {
		t.Fatalf("expected one fixture thread, got %d", len(a.inv.Entries))
	}
	e := a.inv.Entries[0]
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	info, err := a.Terms.Open(pty.Spec{Argv: []string{exe, "-test.run=^TestPresencePTYHelper$"}, Dir: repo,
		Env: pty.Env{Set: []string{"HOPSESH_PRESENCE_HELPER=1"}}}, TabSetup{Background: true,
		Meta: TabMeta{Kind: TabSession, Machine: e.Machine, Key: e.Session.Key.String(), LaunchedKey: e.Session.Key.String(), Command: "codex resume " + string(e.Session.Key.Session)}})
	if err != nil {
		t.Fatal(err)
	}
	return a, mod, info, e
}

func TestPresenceConfirmsDelayedNativePIDWithoutScan(t *testing.T) {
	a, mod, info, e := presencePTYFixture(t)
	owned, scans := a.inv, app.ResourceCounters().Scans
	if _, err := a.Presence(); err != nil {
		t.Fatal(err)
	}
	tab := a.TerminalTabs()[0]
	if tab.Association != "Session association not confirmed" || tab.Relationship != nil || tab.PID != info.PID || tab.PID <= 0 || tab.Started.IsZero() {
		t.Fatalf("launch arguments confirmed a session or lost owned PID/start: %+v", tab)
	}
	encoded, _ := json.Marshal(tab)
	if !strings.Contains(string(encoded), `"pid":`) || !strings.Contains(string(encoded), `"started":`) {
		t.Fatal("unconfirmed tab hides authoritative process metadata")
	}
	mod.mu.Lock()
	mod.pid = info.PID
	mod.mu.Unlock()
	// Reject late frames if a scan replaced the inventory or another selection
	// currently owns reads. The same evidence confirms once ownership is stable.
	for _, scenario := range []string{"replacement", "selection", "scan"} {
		t.Run(scenario, func(t *testing.T) {
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			mod.mu.Lock()
			mod.onLive = func() { close(entered); <-release }
			mod.mu.Unlock()
			go func() { defer close(done); _, _ = a.Presence() }()
			<-entered
			a.mu.Lock()
			switch scenario {
			case "replacement":
				copy := *owned
				a.inv = &copy
			case "selection":
				a.selectionReads++
			case "scan":
				a.scanMu.Lock()
			}
			a.mu.Unlock()
			close(release)
			<-done
			if a.TerminalTabs()[0].Association != "Session association not confirmed" {
				t.Fatal("late presence frame published across another owner")
			}
			a.mu.Lock()
			a.inv = owned
			if scenario == "selection" {
				a.selectionReads--
			}
			a.mu.Unlock()
			if scenario == "scan" {
				a.scanMu.Unlock()
			}
			mod.mu.Lock()
			mod.onLive = nil
			mod.mu.Unlock()
		})
	}
	if _, err := a.Presence(); err != nil {
		t.Fatal(err)
	}
	tab = a.TerminalTabs()[0]
	if tab.Association != "Confirmed by native process registry" || tab.Relationship == nil || tab.Key != e.Session.Key.String() || tab.PID != info.PID || tab.Started != info.Started {
		t.Fatalf("fresh native evidence did not bind the owned PTY: %+v", tab)
	}
	if a.inv != owned || len(owned.Entries[0].Live.Procs) != 0 || app.ResourceCounters().Scans != scans {
		t.Fatal("Presence mutated borrowed inventory or required another scan")
	}
}

func TestTerminalBindingExitCallbackLockOrder(t *testing.T) {
	a, _, info, entry := presencePTYFixture(t)
	tab := a.TerminalTabs()[0]
	entry.Live = agent.LiveInfo{State: agent.Live, Procs: []agent.LiveProc{{PID: info.PID, ObservedAt: time.Now().UTC()}}}
	observed := &app.Inventory{Entries: []app.Entry{entry}}
	table, err := presence.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found, ambiguous := boundEntry(observed, table, tab, entry.Session.Key)
	if found == nil || ambiguous {
		t.Fatal("owned native process evidence missing")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	callback := make(chan bool, 1)
	var once sync.Once
	a.Terms.AutoClose = func() bool {
		once.Do(func() {
			close(entered) // Terms.changed holds Terms.mu here.
			<-release
			// A binder blocked on Terms.mu must not hold App.mu. TryLock
			// reports the inversion without wedging test cleanup on old code.
			ok := a.mu.TryLock()
			if ok {
				a.mu.Unlock()
			}
			callback <- ok
		})
		return false
	}
	exited := info
	exited.State, exited.Code = pty.Exited, 0
	changed := make(chan struct{})
	go func() { defer close(changed); a.Terms.changed(exited) }()
	<-entered
	bound := make(chan struct{})
	go func() {
		defer close(bound)
		a.commitTerminalBinding(a.inv, tab, entry.Session.Key, found, false, observed.Relationships())
	}()
	// Let the binding commit contend with the paused exit callback's lock.
	time.Sleep(50 * time.Millisecond)
	close(release)
	select {
	case ok := <-callback:
		if !ok {
			t.Error("binding held App.mu while waiting for Terms.mu")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("exit callback deadlocked")
	}
	for _, done := range []chan struct{}{changed, bound} {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("exit and binding did not finish")
		}
	}
}

func TestRuntimeRebindingRejectsStaleAmbiguousAndWrongNamespace(t *testing.T) {
	now := time.Now()
	key := agent.SessionKey{Agent: "claude", Profile: "work", Session: "original"}
	tab := TermTab{Info: pty.Info{PID: 10, Started: now}, TabMeta: TabMeta{Machine: "here", Key: key.String()}}
	table := presence.Table{10: {PID: 10, PPID: 1}, 11: {PID: 11, PPID: 10}, 99: {PID: 99, PPID: 1}}
	entry := func(id string, pid int, at time.Time) app.Entry {
		return app.Entry{Machine: "here", Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Profile: "work", Session: agent.SessionID(id)}}, Live: agent.LiveInfo{State: agent.Live, Procs: []agent.LiveProc{{PID: pid, ObservedAt: at}}}}
	}
	for _, tc := range []struct {
		name      string
		entries   []app.Entry
		want      string
		ambiguous bool
	}{
		{"wrapper child after native fork", []app.Entry{entry("fork", 11, now.Add(time.Second))}, "fork", false},
		{"delayed observation", nil, "", false},
		{"PID reuse old registry", []app.Entry{entry("fork", 10, now.Add(-time.Second))}, "", false},
		{"unowned process", []app.Entry{entry("fork", 99, now)}, "", false},
		{"unknown observation time", []app.Entry{entry("fork", 10, time.Time{})}, "", false},
		{"ambiguous switch", []app.Entry{entry("original", 10, now), entry("fork", 11, now)}, "fork", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			found, amb := boundEntry(&app.Inventory{Entries: tc.entries}, table, tab, key)
			got := ""
			if found != nil {
				got = string(found.Session.Key.Session)
			}
			if got != tc.want || amb != tc.ambiguous {
				t.Fatalf("got %q/%v want %q/%v", got, amb, tc.want, tc.ambiguous)
			}
		})
	}
	for _, change := range []func(*app.Entry){func(e *app.Entry) { e.Machine = "remote" }, func(e *app.Entry) { e.Session.Key.Profile = "personal" }, func(e *app.Entry) { e.Session.Key.Agent = "codex" }} {
		e := entry("fork", 11, now)
		change(&e)
		if found, _ := boundEntry(&app.Inventory{Entries: []app.Entry{e}}, table, tab, key); found != nil {
			t.Fatal("cross-namespace binding")
		}
	}
}
