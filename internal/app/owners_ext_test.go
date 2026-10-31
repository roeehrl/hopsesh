package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestOwnerRecords(t *testing.T) {
	dir := t.TempDir()
	write := func(program, name, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, program), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, program, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	key := agent.SessionKey{Agent: "claude", Session: "s1"}
	write("mycnc", "100.json", `{"owner":"mycnc","pid":100,"session":{"agent":"claude","session":"s1"},"stop":"Stop it in MyCNC"}`)
	write("mycnc", "101.json", `{"owner":"mycnc","pid":101,"session":{"agent":"claude","session":"s1"}}`) // not running
	write("mycnc", "102.json", `{"owner":"other","pid":102,"session":{"agent":"claude","session":"s1"}}`) // wrong owner
	write("mycnc", "103.json", `{"owner":"mycnc","pid":999,"session":{"agent":"claude","session":"s1"}}`) // pid differs from its name
	write("mycnc", "notes.txt", `x`)
	write("Bad Name", "104.json", `{"owner":"Bad Name","pid":104,"session":{"agent":"claude","session":"s1"}}`)
	write("mycnc", "105.json", `{`)
	alive := func(pid int) bool { return pid != 101 }
	got := readOwnerRecords(dir, alive)
	if len(got[key]) != 1 || got[key][0].PID != 100 || got[key][0].Stop != "Stop it in MyCNC" {
		t.Fatalf("records: %+v", got)
	}

	open := Entry{Session: agent.Summary{Key: key}, Live: agent.LiveInfo{State: agent.Live, Procs: []agent.LiveProc{{PID: 100}}}}
	if r := OwnerOf(got, open); r == nil || r.Owner != "mycnc" {
		t.Fatalf("owner of a session its process has open: %+v", r)
	}
	other := open
	other.Live.Procs = []agent.LiveProc{{PID: 555}}
	if r := OwnerOf(got, other); r != nil {
		t.Fatalf("a record whose process does not have the session open names no owner: %+v", r)
	}
	ended := open
	ended.Live = agent.LiveInfo{State: agent.Ended}
	if r := OwnerOf(got, ended); r != nil {
		t.Fatalf("an ended session has no owner: %+v", r)
	}
	unknown := open
	unknown.Live = agent.LiveInfo{State: agent.Unknown}
	if r := OwnerOf(got, unknown); r == nil {
		t.Fatal("when the agent can't say which process has it, the running record counts")
	}
	if got := readOwnerRecords(filepath.Join(dir, "missing"), alive); got != nil {
		t.Fatalf("no folder: %+v", got)
	}
}
