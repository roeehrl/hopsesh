package agenttest

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// LineageSuffix is the core's lineage manifest name beside a session's main file; agents
// must ignore such files.
const LineageSuffix = ".hopsesh.json"

var idSyntax = regexp.MustCompile(`^[a-z][a-z0-9]{1,15}$`)

// Run checks a module against the contract. newHost returns a machine with the module's
// test sessions on it (at least one), the agent's binary present and its roots resolved
// from the Spec's defaults.
func Run(t *testing.T, m agent.Module, newHost func(t *testing.T) *FakeHost) {
	t.Helper()
	ctx := context.Background()
	spec := m.Spec()

	t.Run("spec", func(t *testing.T) {
		if !idSyntax.MatchString(string(spec.ID)) {
			t.Fatalf("id %q must match %s", spec.ID, idSyntax)
		}
		if spec.Name == "" || spec.Vendor == "" || len(spec.Roots) == 0 || len(spec.Binaries) == 0 {
			t.Fatal("a Spec needs a name, a vendor, roots and binaries")
		}
		if len(spec.Tested) == 0 {
			t.Fatal("a Spec lists the versions it was tested with")
		}
		for _, c := range spec.Experimental {
			if !agent.Has(m, c) {
				t.Errorf("experimental capability %s is not implemented", c)
			}
		}
	})

	setup := func(t *testing.T) (*FakeHost, agent.Host, agent.Install, agent.Listing) {
		fh := newHost(t)
		in, err := m.Detect(ctx, fh)
		if err != nil {
			t.Fatalf("Detect: %v", err)
		}
		if !in.Present || in.Agent != spec.ID {
			t.Fatalf("Detect found no %s: %+v", spec.ID, in)
		}
		h := agent.Confine(fh, spec, in)
		l, err := m.List(ctx, h, in)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(l.Sessions) == 0 {
			t.Fatal("List found no sessions in the test data")
		}
		return fh, h, in, l
	}

	t.Run("list", func(t *testing.T) {
		fh, h, in, l := setup(t)
		for _, s := range l.Sessions {
			if s.Key.Agent != spec.ID || s.Key.Session == "" || s.Path == "" || s.CWD == "" {
				t.Errorf("incomplete summary: %+v", s)
			}
		}
		again, _ := m.List(ctx, h, in)
		if len(again.Sessions) != len(l.Sessions) {
			t.Fatal("two listings differ")
		}
		// Lineage manifests beside sessions must not show up as sessions or errors.
		for _, s := range l.Sessions {
			fh.Put(s.Path+LineageSuffix, []byte(`{"lineage":1}`), time.Now())
		}
		withManifests, err := m.List(ctx, h, in)
		if err != nil || len(withManifests.Sessions) != len(l.Sessions) || len(withManifests.Errors) != len(l.Errors) {
			t.Fatalf("lineage manifests changed the listing: %d sessions, %d errors (was %d, %d)",
				len(withManifests.Sessions), len(withManifests.Errors), len(l.Sessions), len(l.Errors))
		}
	})

	t.Run("bundle", func(t *testing.T) {
		_, h, in, l := setup(t)
		for _, s := range l.Sessions {
			b, err := m.Bundle(ctx, h, in, s)
			if err != nil {
				t.Fatalf("Bundle %s: %v", s.Key, err)
			}
			main, ok := b.Main()
			if !ok {
				t.Fatalf("Bundle %s has no main file", s.Key)
			}
			if got := h.Path().Join(in.Root(main.Root), main.Rel); got != s.Path {
				t.Errorf("Bundle %s main file %s, summary says %s", s.Key, got, s.Path)
			}
			for _, f := range b.Files {
				if _, err := h.FS().Stat(h.Path().Join(in.Root(f.Root), f.Rel)); err != nil {
					t.Errorf("Bundle %s names a missing file: %v", s.Key, err)
				}
			}
		}
	})

	t.Run("plan-move-identity", func(t *testing.T) {
		_, h, in, l := setup(t)
		s := l.Sessions[0]
		b, _ := m.Bundle(ctx, h, in, s)
		p := agent.Placement{Key: s.Key, SourceID: s.Key.Session, CWD: s.CWD, Location: "fake"}
		mp, err := m.PlanMove(in, in, s, b, p)
		if err != nil {
			t.Fatalf("PlanMove: %v", err)
		}
		if len(mp.Files) != len(b.Files) {
			t.Fatalf("PlanMove placed %d of %d files", len(mp.Files), len(b.Files))
		}
		for _, f := range mp.Files {
			if f.ToRoot != f.From.Root || f.ToRel != f.From.Rel {
				t.Errorf("an identity move must keep %s/%s in place, got %s/%s", f.From.Root, f.From.Rel, f.ToRoot, f.ToRel)
			}
		}
	})

	t.Run("confined", func(t *testing.T) {
		_, h, _, _ := setup(t)
		outside := h.Path().Join(h.Facts().Home, "hopsesh-outside-the-roots.txt")
		if err := h.FS().WriteFile(outside, []byte("x"), 0o600); !errors.Is(err, agent.ErrDenied) {
			t.Fatalf("a write outside the roots must be denied, got %v", err)
		}
	})

	if r, ok := m.(agent.Reader); ok {
		t.Run("reader", func(t *testing.T) {
			_, h, in, l := setup(t)
			for _, s := range l.Sessions {
				a, err := r.Read(ctx, h, in, s, ir.Cursor{})
				if err != nil {
					t.Fatalf("Read %s: %v", s.Key, err)
				}
				b, _ := r.Read(ctx, h, in, s, ir.Cursor{})
				if len(a.Nodes) == 0 || len(a.Nodes) != len(b.Nodes) || a.Cursor != b.Cursor {
					t.Fatalf("Read %s is empty or not deterministic", s.Key)
				}
				for i := range a.Nodes {
					if a.Nodes[i].ID != b.Nodes[i].ID || a.Nodes[i].ID == "" {
						t.Fatalf("Read %s: node %d ids differ", s.Key, i)
					}
				}
				rest, err := r.Read(ctx, h, in, s, a.Cursor)
				if err != nil || len(rest.Nodes) != 0 {
					t.Fatalf("Read from the end cursor must return nothing new, got %d nodes, %v", len(rest.Nodes), err)
				}
			}
		})
	}

	if w, ok := m.(agent.Writer); ok {
		t.Run("writer", func(t *testing.T) { runWriter(t, m, w, setup) })
	}
}

func runWriter(t *testing.T, m agent.Module, w agent.Writer, setup func(t *testing.T) (*FakeHost, agent.Host, agent.Install, agent.Listing)) {
	ctx := context.Background()
	fh, h, in, l := setup(t)
	cwd := l.Sessions[0].CWD
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	items := []ir.Item{
		{Node: "n1", Role: ir.RoleUser, Time: now, Text: "What is the codeword in notes.txt?"},
		{Node: "n2", Role: ir.RoleAgent, Time: now.Add(time.Second), Text: "[prior agent · shell · exit 0]\n$ cat notes.txt\n[output]\nPLUM-7\n[/output]\nThe codeword is PLUM-7."},
	}
	res, err := w.Write(ctx, h, in, ir.WriteRequest{Mode: ir.WriteNew, Header: ir.Header{CWD: cwd, Title: "conformance", Created: now}, Items: items})
	if err != nil {
		t.Fatalf("Write new: %v", err)
	}
	b, ok := fh.Get(res.Path)
	if !ok || len(b) == 0 || b[len(b)-1] != '\n' {
		t.Fatalf("Write must create %s ending in a newline", res.Path)
	}
	if res.From != 0 || res.To != int64(len(b)) || res.Cursor.Offset != int64(len(b)) {
		t.Fatalf("WriteResult range %d-%d, cursor %d, file %d bytes", res.From, res.To, res.Cursor.Offset, len(b))
	}
	after, err := m.List(ctx, h, in)
	if err != nil {
		t.Fatal(err)
	}
	var written *agent.Summary
	for i := range after.Sessions {
		if string(after.Sessions[i].Key.Session) == res.SessionID {
			written = &after.Sessions[i]
		}
	}
	if written == nil {
		t.Fatalf("the written session %s is not listed", res.SessionID)
	}
	if written.CWD != cwd {
		t.Fatalf("the written session's cwd is %q, want %q", written.CWD, cwd)
	}
	if r, ok := m.(agent.Reader); ok {
		seg, err := r.Read(ctx, h, in, *written, ir.Cursor{})
		if err != nil {
			t.Fatal(err)
		}
		if !mentions(seg, "PLUM-7") || !mentions(seg, "What is the codeword") {
			t.Fatal("reading back the written session lost its messages")
		}
		more := []ir.Item{
			{Node: "n3", Role: ir.RoleUser, Time: now.Add(time.Minute), Text: "And the second one?"},
			{Node: "n4", Role: ir.RoleAgent, Time: now.Add(time.Minute + time.Second), Text: "FIG-3."},
		}
		ap, err := w.Write(ctx, h, in, ir.WriteRequest{Mode: ir.WriteAppend, SessionID: res.SessionID, Expect: res.Cursor, Header: seg.Header, Items: more})
		if err != nil {
			t.Fatalf("Write append: %v", err)
		}
		if ap.From != res.To {
			t.Fatalf("append started at %d, the file ended at %d", ap.From, res.To)
		}
		seg2, _ := r.Read(ctx, h, in, *written, ir.Cursor{})
		if !mentions(seg2, "FIG-3") || !mentions(seg2, "PLUM-7") {
			t.Fatal("the appended session must hold both writes")
		}
		_, err = w.Write(ctx, h, in, ir.WriteRequest{Mode: ir.WriteAppend, SessionID: res.SessionID, Expect: res.Cursor, Header: seg.Header, Items: more})
		if !errors.Is(err, agent.ErrDiverged) {
			t.Fatalf("an append at a stale cursor must fail with ErrDiverged, got %v", err)
		}
	}
}

func mentions(s ir.Segment, text string) bool {
	for _, n := range s.Nodes {
		if strings.Contains(n.Text, text) {
			return true
		}
	}
	return false
}
