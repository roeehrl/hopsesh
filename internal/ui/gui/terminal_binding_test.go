package gui

import (
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/presence"
	"github.com/roeehrl/hopsesh/internal/core/pty"
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
