package app

import (
	"context"
	"encoding/json"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
)

func TestNativeForkDiscoveredBeforeFirstHopseshMove(t *testing.T) {
	ctx := context.Background()
	fh := agenttest.NewFakeHost("/home/u")
	if err := fh.Load("../../agents/codex/testdata/0.153.2", "/home/u/.codex"); err != nil {
		t.Fatal(err)
	}
	fh.AddBinary("codex", "codex-cli 0.153.2")
	m := codex.New()
	in, err := m.Detect(ctx, fh)
	if err != nil {
		t.Fatal(err)
	}
	h := agent.Confine(fh, m.Spec(), in)
	ls, _ := m.List(ctx, h, in)
	var parent agent.Summary
	for _, s := range ls.Sessions {
		if strings.HasSuffix(string(s.Key.Session), "0001") {
			parent = s
		}
	}
	raw, _ := h.FS().ReadFile(parent.Path, 1<<20)
	lines := strings.Split(string(raw), "\n")
	var meta map[string]any
	json.Unmarshal([]byte(lines[0]), &meta)
	payload := meta["payload"].(map[string]any)
	childID := "01a0fe1c-0000-7000-8000-000000000009"
	payload["id"] = childID
	payload["forked_from_id"] = string(parent.Key.Session)
	payload["history_mode"] = "legacy"
	first, _ := json.Marshal(meta)
	lines[0] = string(first)
	childPath := path.Join(path.Dir(parent.Path), "rollout-2026-10-01T13-00-00-"+childID+".jsonl")
	fh.Put(childPath, []byte(strings.Join(lines, "\n")), time.Now())
	ls, err = m.List(ctx, h, in)
	if err != nil {
		t.Fatal(err)
	}
	graphs := make([]*lineage.Manifest, len(ls.Sessions))
	problems := make([]string, len(ls.Sessions))
	hm := &host.Machine{Name: "A", Local: true, Facts: host.Facts{Home: "/home/u", Endpoint: strings.Repeat("ab", 32)}}
	nativeForkManifests(ctx, hm, h, m, in, ls.Sessions, graphs, problems)
	var pg, cg *lineage.Manifest
	for i, s := range ls.Sessions {
		if problems[i] != "" {
			t.Fatal(problems[i])
		}
		if s.Key == parent.Key {
			pg = graphs[i]
		}
		if string(s.Key.Session) == childID {
			cg = graphs[i]
		}
	}
	if pg == nil || cg == nil || pg.Family != cg.Family || pg.Branch == cg.Branch || !cg.Journey().Fork {
		t.Fatalf("native fork not independently linked: %v %v", pg, cg)
	}
	if len(pg.Revisions) != len(cg.Revisions) {
		t.Fatal("copied history became new work")
	}
}

// Discovery without a stored Hopsesh endpoint reserves different causal IDs on
// each scan. UI grouping stays stable without persisting those temporary graphs.
func TestNativeFamilyPresentationDoesNotDependOnReservedEndpoint(t *testing.T) {
	makeInventory := func(endpoint string) *Inventory {
		root := lineage.NewNative(endpoint, agent.SessionKey{Agent: "codex", Session: "root"})
		root.Upsert(lineage.Replica{Key: agent.SessionKey{Agent: "codex", Session: "root"}, Endpoint: endpoint, Location: "A"})
		child := root.Clone()
		child.Branch = child.Fork("fork-operation", nil)
		child.Upsert(lineage.Replica{Line: child.Branch, Key: agent.SessionKey{Agent: "codex", Session: "child"}, Endpoint: endpoint, Location: "A"})
		return &Inventory{Entries: []Entry{{NativeRelationship: true, Machine: "A", Session: agent.Summary{Key: agent.SessionKey{Agent: "codex", Session: "root"}, Title: "Root"}, Lineage: root}, {NativeRelationship: true, Machine: "A", Session: agent.Summary{Key: agent.SessionKey{Agent: "codex", Session: "child"}, Title: "Child"}, Lineage: child}}}
	}
	a, b := makeInventory("reserved-a"), makeInventory("reserved-b")
	ar, br := a.Relationships(), b.Relationships()
	for key, r := range ar {
		if r.Family != br[key].Family || r.Branch != br[key].Branch {
			t.Fatalf("scan changed identity: %+v / %+v", r, br[key])
		}
	}
	if len(a.FamilyGroups()) != 1 || ar[EntryIdentity("A", "codex/child")].Depth != 1 {
		t.Fatal("native family lost ancestry")
	}
	if a.Entries[0].Lineage.Family == ar[EntryIdentity("A", "codex/root")].Family {
		t.Fatal("presentation identity rewrote causal manifest")
	}
}

// A legacy fork holds its parent's records, so once Codex deletes the parent the fork is a
// conversation of its own. An archived parent or a paginated fork still needs the parent.
func TestNativeForkWithDeletedParent(t *testing.T) {
	const goneID = "01a0fe1c-0000-7000-8000-0000000000aa"
	const childID = "01a0fe1c-0000-7000-8000-00000000000b"
	for _, tc := range []struct {
		name     string
		archived bool
		paged    bool
		blocked  bool
	}{
		{name: "deleted"},
		{name: "archived", archived: true, blocked: true},
		{name: "paginated", paged: true, blocked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			fh := agenttest.NewFakeHost("/home/u")
			if err := fh.Load("../../agents/codex/testdata/0.153.2", "/home/u/.codex"); err != nil {
				t.Fatal(err)
			}
			fh.AddBinary("codex", "codex-cli 0.153.2")
			m := codex.New()
			in, err := m.Detect(ctx, fh)
			if err != nil {
				t.Fatal(err)
			}
			h := agent.Confine(fh, m.Spec(), in)
			ls, _ := m.List(ctx, h, in)
			src := ls.Sessions[0]
			raw, _ := h.FS().ReadFile(src.Path, 1<<20)
			lines := strings.Split(string(raw), "\n")
			var meta map[string]any
			json.Unmarshal([]byte(lines[0]), &meta)
			payload := meta["payload"].(map[string]any)
			payload["id"] = childID
			payload["forked_from_id"] = goneID
			payload["history_mode"] = "legacy"
			if tc.paged {
				payload["history_base"] = map[string]any{"thread_id": goneID}
			}
			first, _ := json.Marshal(meta)
			lines[0] = string(first)
			fh.Put(path.Join(path.Dir(src.Path), "rollout-2026-10-01T13-00-00-"+childID+".jsonl"), []byte(strings.Join(lines, "\n")), time.Now())
			if tc.archived {
				fh.Put("/home/u/.codex/archived_sessions/rollout-2026-10-01T12-00-00-"+goneID+".jsonl", []byte(lines[0]+"\n"), time.Now())
			}
			ls, err = m.List(ctx, h, in)
			if err != nil {
				t.Fatal(err)
			}
			graphs := make([]*lineage.Manifest, len(ls.Sessions))
			problems := make([]string, len(ls.Sessions))
			hm := &host.Machine{Name: "A", Local: true, Facts: host.Facts{Home: "/home/u", Endpoint: strings.Repeat("ab", 32)}}
			nativeForkManifests(ctx, hm, h, m, in, ls.Sessions, graphs, problems)
			found := false
			for i, s := range ls.Sessions {
				if string(s.Key.Session) != childID {
					continue
				}
				found = true
				if blocked := problems[i] != ""; blocked != tc.blocked {
					t.Fatalf("blocked = %v (%q), want %v", blocked, problems[i], tc.blocked)
				}
				if graphs[i] != nil {
					t.Fatal("an orphaned fork must not join a family it cannot verify")
				}
			}
			if !found {
				t.Fatal("child not listed")
			}
		})
	}
}
