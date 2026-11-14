package move

import (
	"context"
	"testing"

	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// appendless is a writer that cannot add to its sessions (OpenCode's Profile).
type appendless struct{ *capacityReader }

func (appendless) Profile(agent.Install) ir.Profile { return ir.Profile{Window: ir.FallbackWindow} }

// Work brought back from a cloud is offered as an addition to the session it was handed
// off from only when that session's agent can add to it (Profile.PortableAppend); with
// one that cannot (OpenCode), it comes as a new session, and asking to add is refused
// before anything is written.
func TestFetchAppendNeedsPortableAppend(t *testing.T) {
	key := agent.SessionKey{Agent: "codex", Session: "original"}
	head := ir.Cursor{Head: "left", Offset: 3}
	m := lineage.NewNative("local", key)
	from := m.Upsert(lineage.Replica{Key: key, Endpoint: "local", Head: head.Head})
	to := m.Upsert(lineage.Replica{Key: agent.SessionKey{Agent: "codex", Session: "task_1"}, Location: "codex-cloud"})
	if err := m.AppendHop(lineage.Hop{ID: "handoff", From: from, To: to, Kind: lineage.HopHandoff}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		module agent.Module
		offer  bool
	}{
		{"can add", &capacityReader{Module: codex.New(), cursor: head}, true},
		{"cannot add", appendless{&capacityReader{Module: codex.New(), cursor: head}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := FetchInput{Machine: &host.Machine{Local: true, Facts: host.Facts{Endpoint: "local"}}, Module: tc.module, Install: agent.Install{Agent: "codex"},
				Lineage: m, Original: &Copy{Summary: agent.Summary{Key: key}}}
			for _, appendOriginal := range []bool{false, true} {
				p := &Plan{Fetch: &FetchPlan{Session: "task_1", Cloud: "codex-cloud"}}
				var errs []string
				relateFetch(context.Background(), in, p, Options{AppendOriginal: appendOriginal}, func(level, msg string) {
					if level == "err" {
						errs = append(errs, msg)
					}
				})
				if p.Fetch.CanAppend != tc.offer || (p.Fetch.Original != nil) != tc.offer || p.Fetch.Append != (tc.offer && appendOriginal) || len(errs) > 0 {
					t.Fatalf("append %t: offered %t, original %v, appending %t, %v", appendOriginal, p.Fetch.CanAppend, p.Fetch.Original, p.Fetch.Append, errs)
				}
			}
		})
	}
	// Adding to it anyway (a plan from before) is refused.
	err := appendOriginal(context.Background(), &Fetch{}, Side{Module: appendless{&capacityReader{Module: codex.New()}}}, nil, agent.Adoption{}, &Adopted{})
	if err == nil {
		t.Fatal("an agent that cannot add to its sessions was asked to")
	}
}
