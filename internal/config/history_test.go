package config

import (
	"os"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/sdk/ir"
)

func TestHistory(t *testing.T) {
	t.Setenv("HOPSESH_CONFIG_DIR", t.TempDir())
	c := Defaults()
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(Path()); strings.Contains(string(b), "[history") {
		t.Fatalf("unset history is written:\n%s", b)
	}
	if c.History.Limits() != ir.DefaultLimits() {
		t.Fatal("unset history must be the defaults")
	}
	c.History = History{ContextBudget: 32_000, Older: ir.OlderRecent, ReadMemoryMB: 1024, ArchiveMB: 2048}
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	l := got.History.Limits()
	if l.ContextBudget != 32_000 || l.Older != ir.OlderRecent || l.ReadBytes != 1<<30 || l.ArchiveBytes != 2<<30 || l.RecordBytes != ir.DefaultRecordBytes {
		t.Fatalf("round trip: %+v", l)
	}
	for _, bad := range []History{{ContextBudget: 10}, {Older: "everything"}, {ReadMemoryMB: -1}, {ArchiveMB: 1 << 20}, {RecordMB: 512}, {RecordMB: 256, ReadMemoryMB: 128}} {
		c.History = bad
		if c.Check() == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}
