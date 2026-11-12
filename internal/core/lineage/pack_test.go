package lineage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// packFixture is a session observed record by record, delivered to a second replica with
// coalesced fragments and a generated record, the projections packing leaves as objects.
func packFixture(t *testing.T, records int) *Manifest {
	t.Helper()
	m := New("pack")
	src := m.Upsert(Replica{Key: agent.SessionKey{Agent: "claude", Session: "a"}, Location: "studio"})
	var nodes []ir.Node
	for i := range records {
		nodes = append(nodes, ir.Node{Kind: ir.KindMessage, Text: fmt.Sprint("record ", i), Native: &ir.Native{Anchor: fmt.Sprintf("a-%04d", i)}})
		seg := ir.Segment{Nodes: slices.Clone(nodes), Cursor: ir.Cursor{Head: ir.NodeID(fmt.Sprint("h", i)), Offset: int64(10 * (i + 1))}}
		if _, err := m.Observe(src, &seg); err != nil {
			t.Fatal(err)
		}
	}
	s, _ := m.LatestState(src)
	dst := m.Upsert(Replica{Key: agent.SessionKey{Agent: "codex", Session: "b"}, Location: "here"})
	both := []ir.NodeID{s.Projection[0].Coverage[0], s.Projection[1].Coverage[0]}
	projection := []ir.Projection{
		{Anchor: "b-0", Hash: "x", Coverage: both, Fragments: []ir.Fragment{{Text: "one", Coverage: both[:1]}, {Text: "two", Coverage: both[1:]}}},
		{Anchor: "b-1", Hash: "y", Coverage: []ir.NodeID{s.Projection[2].Coverage[0]}, Generated: true},
	}
	m.Deliver(dst, ir.Cursor{Head: "b", Offset: 5}, projection, s.Heads, []string{"tool output"})
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	return m
}

func model(m *Manifest) string {
	b, _ := json.Marshal(m)
	return string(b)
}

func TestPackedFileHoldsTheWholeManifest(t *testing.T) {
	m := packFixture(t, 40)
	b := m.Encode()
	if !bytes.HasPrefix(b, []byte(`{"hopsesh":"`+FileFormat+`"`)) {
		t.Fatalf("not packed: %.80s", b)
	}
	got, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Format != Format || model(got) != model(m) {
		t.Fatal("packing changed the manifest")
	}
	if string(got.Encode()) != string(b) {
		t.Fatal("packing is not stable")
	}
	plain := model(m)
	if len(b)*3 > len(plain) {
		t.Fatalf("packed %d bytes of %d", len(b), len(plain))
	}
}

func TestEmptyManifestPacks(t *testing.T) {
	m := New("empty")
	got, err := Parse(m.Encode())
	if err != nil || model(got) != model(m) {
		t.Fatalf("empty manifest read differently: %v", err)
	}
}

func TestPlainFormatFilesStillRead(t *testing.T) {
	m := packFixture(t, 5)
	got, err := Parse([]byte(model(m)))
	if err != nil {
		t.Fatal(err)
	}
	if model(got) != model(m) {
		t.Fatal("lineage/5 file read differently")
	}
}

// A merged graph need not list a revision after its parents.
func TestPackedParentsMayComeLater(t *testing.T) {
	m := packFixture(t, 6)
	slices.Reverse(m.Revisions)
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	got, err := Parse(m.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if model(got) != model(m) {
		t.Fatal("reordered graph read differently")
	}
}

func TestPackedFileChangedIsRefused(t *testing.T) {
	m := packFixture(t, 3)
	s, _ := m.LatestState(m.Replicas[0].ID)
	b := m.Encode()
	edited := bytes.Replace(b, []byte(s.Projection[1].Hash), []byte(strings.Repeat("0", len(s.Projection[1].Hash))), 1)
	if bytes.Equal(edited, b) {
		t.Fatal("fixture has no hash to change")
	}
	if _, err := Parse(edited); err == nil {
		t.Fatal("a changed record was accepted")
	}
	for _, bad := range []string{
		`{"hopsesh":"lineage/6","family":"f","branch":"b","branches":[{"id":"b"}],"replicas":[],"revisions":[[0,"a","h"]],"states":[],"hops":[]}`,
		`{"hopsesh":"lineage/6","family":"f","branch":"b","branches":[{"id":"b"}],"replicas":[],"revisions":[],"states":[],"hops":[],"extra":1}`,
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

// A manifest whose references point nowhere is written as it is, for Validate to refuse.
func TestUnpackableManifestIsWrittenPlain(t *testing.T) {
	m := packFixture(t, 3)
	m.Revisions[1].Replica = "nowhere"
	if b := m.Encode(); !bytes.HasPrefix(b, []byte(`{"hopsesh":"`+Format+`"`)) {
		t.Fatalf("packed an invalid manifest: %.80s", b)
	}
}
