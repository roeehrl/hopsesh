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
