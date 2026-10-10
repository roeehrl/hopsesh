package host

import (
	"context"
	"errors"
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestSnapshotReadsAndRecordsWrites(t *testing.T) {
	m := NewSnapshot("laptop", Facts{OS: "darwin", Home: "/Users/u"}, []SnapshotFile{
		{Path: "/Users/u/.claude/projects/p/s1.jsonl", Data: []byte("a\n")},
		{Path: "/Users/u/.claude/projects/p/s1/subagents/x.jsonl", Data: []byte("b\n")},
	})
	fsys, _ := m.FS(context.Background())
	if fi, err := fsys.Stat("/Users/u/.claude/projects/p"); err != nil || !fi.IsDir() {
		t.Fatalf("implied folder: %v %v", fi, err)
	}
	ls, err := fsys.ReadDir("/Users/u/.claude/projects/p")
	if err != nil || len(ls) != 2 || ls[0].Name() != "s1" || !ls[0].IsDir() || ls[1].Name() != "s1.jsonl" {
		t.Fatalf("readdir: %v %v", ls, err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(fsys.Append("/Users/u/.claude/projects/p/s1.jsonl", []byte("mark\n"), agent.AppendOptions{KeepMtime: true}))
	must(fsys.WriteFile("/Users/u/.claude/projects/p/s1.hopsesh.json", []byte("{}"), 0o600))
	if b, _ := fsys.ReadFile("/Users/u/.claude/projects/p/s1.jsonl", 100); string(b) != "a\nmark\n" {
		t.Fatalf("read after append: %q", b)
	}
	w := m.Writes()
	if len(w) != 2 || w[0].Op != "append" || !w[0].Append.KeepMtime || w[1].Op != "write" {
		t.Fatalf("writes: %+v", w)
	}
	if _, err := m.Exec().Run(context.Background(), []string{"claude"}, agent.RunOptions{}); err == nil {
		t.Fatal("nothing runs on a snapshot")
	}
}

func TestSnapshotDurabilityFailurePreventsAcknowledgementMutation(t *testing.T) {
	m := NewSnapshot("source", Facts{OS: "linux"}, []SnapshotFile{{Path: "/session", Data: []byte("original\n")}})
	m.PersistWrites(func([]SnapshotWrite) error { return errors.New("disk full") })
	f, _ := m.FS(context.Background())
	for _, apply := range []func() error{
		func() error { return f.WriteFile("/session", []byte("replacement"), 0600) },
		func() error { return f.Append("/session", []byte("mark"), agent.AppendOptions{}) },
		func() error { return f.Rename("/session", "/elsewhere") },
	} {
		if err := apply(); err == nil {
			t.Fatal("in-memory write preceded durable acknowledgement")
		}
	}
	b, _ := f.ReadFile("/session", 100)
	if string(b) != "original\n" || len(m.Writes()) != 0 {
		t.Fatal("failed persistence mutated snapshot")
	}
}
