package app

import (
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestUsage(t *testing.T) {
	e := func(machine string, id agent.ID, size int64) Entry {
		return Entry{Machine: machine, Location: agent.MachineLocation(machine), Agent: id, AgentName: string(id), Session: agent.Summary{Size: size}}
	}
	inv := &Inventory{Entries: []Entry{e("a", "claude", 3<<20), e("a", "codex", 9<<30), e("a", "claude", 1<<20), e("b", "claude", 5)}}
	got := inv.Usage("a")
	if len(got) != 2 || got[0].Agent != "codex" || got[0].Bytes != 9<<30 || got[1].Sessions != 2 || got[1].Bytes != 4<<20 {
		t.Fatalf("usage of a: %+v", got)
	}
	if got := inv.Usage("none"); got == nil || len(got) != 0 {
		t.Fatalf("usage of an empty machine: %#v", got)
	}
	for n, want := range map[int64]string{0: "0 B", 812: "812 B", 1536: "2 KB", 3 << 20: "3.0 MB", 9<<30 + 1<<29: "9.5 GB"} {
		if got := HumanBytes(n); got != want {
			t.Errorf("HumanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}
