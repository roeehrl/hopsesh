package app

import (
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/cloudintegration"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func cloudHandoffLinkFixture(t *testing.T) (*App, func() relay.CloudTask, *journal.Journal) {
	t.Helper()
	a := New(config.Defaults(), nil, t.TempDir(), nil)
	store := relay.Store{Directory: filepath.Join(a.StateDir, "relay")}
	owner, err := store.Identity(t.Context(), "native-owner")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"ticket": strings.Repeat("a", 64), "provider": r.Form.Get("provider"), "session": r.Form.Get("session"), "lease_seconds": 600, "expires": time.Now().Add(10 * time.Minute).Unix()})
	}))
	t.Cleanup(server.Close)
	cert := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	connection := relay.Connection{URL: server.URL, Device: owner.Public.ID, Token: "private-test-credential", Space: "handoff-test-space-123", Expires: time.Now().Add(time.Hour).Unix(), CAFile: cert}
	admissions := relay.AdmissionStore{Directory: filepath.Join(a.StateDir, "cloud-admissions")}
	task := func() relay.CloudTask {
		t.Helper()
		record, err := admissions.IssueTask(t.Context(), owner, connection, "claude-hosted", "provider-native-incarnation", 10*time.Minute, "")
		if err != nil {
			t.Fatal(err)
		}
		task, err := admissions.Task(record.TaskID, owner.Public.ID)
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	j, err := journal.New(a.StateDir, journal.KindHandoff, "Saved handoff")
	if err != nil {
		t.Fatal(err)
	}
	m := lineage.New("saved-local-family")
	source := m.Upsert(lineage.Replica{Endpoint: owner.Public.Endpoint, Location: "local", Key: agent.SessionKey{Agent: "claude", Session: "local-original"}})
	seg := ir.Segment{Nodes: []ir.Node{{Kind: ir.KindMessage, Actor: ir.User, Text: "Original private source", Native: &ir.Native{Anchor: "local-message"}}}}
	ir.Chain(seg.Nodes, "")
	seg.Cursor = ir.Cursor{Head: seg.Nodes[0].ID, Offset: 100}
	state, err := m.Observe(source, &seg)
	if err != nil {
		t.Fatal(err)
	}
	key := agent.SessionKey{Agent: "claude", Session: "outer-vendor-cloud-task"}
	cloud := m.Upsert(lineage.Replica{Endpoint: "cloud:claude-cloud:account", Location: "claude-cloud", Key: key})
	m.Deliver(cloud, ir.Cursor{}, nil, state.Heads, []string{"handoff briefing; native reasoning stays at source"})
	if err := m.AppendHop(lineage.Hop{ID: j.ID, Time: j.Time, From: source, To: cloud, Kind: lineage.HopHandoff}); err != nil {
		t.Fatal(err)
	}
	j.Entries = append(j.Entries, journal.Entry{Op: journal.OpCloud, Cloud: "claude-cloud", Key: &key})
	if err := j.WriteReceipt(host.LocalFS(), "local", filepath.Join(a.StateDir, "original"+lineage.Suffix), m.Encode(), false); err != nil {
		t.Fatal(err)
	}
	if err := move.SaveHandoff(a.StateDir, &move.Handoff{Journal: j.ID, Time: j.Time, Machine: "local", Cloud: "claude-cloud", Session: key, Title: "Reviewed source", Brief: "Saved briefing"}); err != nil {
		t.Fatal(err)
	}
	return a, task, j
}

func TestCloudHandoffBindingRequiresSavedProofAndKeepsForksIndependent(t *testing.T) {
	a, issue, j := cloudHandoffLinkFixture(t)
	task := issue()
	options, err := a.CloudHandoffOptions()
	if err != nil || len(options) != 1 {
		t.Fatal(options, err)
	}
	plan, err := a.PlanCloudHandoffLink(task.ID, j.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.LinkCloudHandoff(t.Context(), task.ID, j.ID, false, "stale-review"); err == nil {
		t.Fatal("unreviewed association saved")
	}
	for range 2 {
		if _, err := a.LinkCloudHandoff(t.Context(), task.ID, j.ID, false, plan.Review); err != nil {
			t.Fatal(err)
		}
	}
	link, m, err := a.cloudHandoffLink(task)
	if err != nil || link == nil || m.Family != plan.Handoff.Family {
		t.Fatal("signed provenance missing", err)
	}
	second := issue()
	next, err := a.PlanCloudHandoffLink(second.ID, j.ID, false)
	if err == nil {
		t.Fatal("preview did not disclose an existing handoff association")
	}
	if _, err := a.LinkCloudHandoff(t.Context(), second.ID, j.ID, false, next.Review); err == nil {
		t.Fatal("two independent tasks silently joined one branch")
	}
	fork, err := a.PlanCloudHandoffLink(second.ID, j.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.LinkCloudHandoff(t.Context(), second.ID, j.ID, true, fork.Review); err != nil {
		t.Fatal(err)
	}
	child, branch, err := a.cloudHandoffLink(second)
	if err != nil || !child.Origin.Fork || branch.Branch == m.Branch || branch.Family != m.Family {
		t.Fatal("fork lost independent line", err)
	}
	rows, err := a.CloudHandoffLinks()
	if err != nil || len(rows) != 2 {
		t.Fatal("saved ancestry not available to GUI", rows, err)
	}
	for _, row := range rows {
		if row.Journal != j.ID || row.Family != m.Family || row.Problem != "" || row.Fork != (row.Task == second.ID) {
			t.Fatal("incorrect saved ancestry", row)
		}
	}
	// Signature covers graph, briefing, fork choice, task and exact journal.
	path := filepath.Join(a.StateDir, "cloud-task-links", task.ID+".json")
	link.Origin.Brief = "different briefing"
	body, _ := json.Marshal(link)
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.cloudHandoffLink(task); err == nil {
		t.Fatal("modified provenance accepted")
	}
}

func TestCloudHandoffBindingRejectsLateAttachmentChangedReviewAndUndo(t *testing.T) {
	for _, mode := range []string{"existing checkpoint", "reviewed checkpoint", "changed briefing", "unapplied receipt", "undone", "wrong path"} {
		t.Run(mode, func(t *testing.T) {
			a, issue, j := cloudHandoffLinkFixture(t)
			task := issue()
			plan, err := a.PlanCloudHandoffLink(task.ID, j.ID, false)
			if err != nil {
				t.Fatal(err)
			}
			id := j.ID
			switch mode {
			case "reviewed checkpoint":
				root := filepath.Join(a.StateDir, "cloud-checkpoints")
				if err := os.MkdirAll(root, 0700); err != nil {
					t.Fatal(err)
				}
				record := cloudCheckpointRecord{Export: cloudintegration.Export{Observation: cloudintegration.Observation{Task: &task}}}
				if err := saveRelayRecord(filepath.Join(root, "reviewed-operation.json"), record); err != nil {
					t.Fatal(err)
				}
			case "existing checkpoint":
				p := lineage.PathFor(filepath.Join(a.StateDir, "cloud-checkpoints", task.ID, "source.jsonl"))
				if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("existing receipt"), 0600); err != nil {
					t.Fatal(err)
				}
			case "changed briefing":
				h, err := move.LoadHandoff(a.StateDir, j.ID)
				if err != nil {
					t.Fatal(err)
				}
				h.Brief = "changed after review"
				if err := move.SaveHandoff(a.StateDir, h); err != nil {
					t.Fatal(err)
				}
			case "unapplied receipt":
				j.Receipts[0].Applied = false
				if err := j.Save(); err != nil {
					t.Fatal(err)
				}
			case "undone":
				j.Undone = true
				if err := j.Save(); err != nil {
					t.Fatal(err)
				}
			case "wrong path":
				id = "../" + j.ID
			}
			if _, err := a.LinkCloudHandoff(t.Context(), task.ID, id, false, plan.Review); err == nil {
				t.Fatal("unverified or stale ancestry accepted")
			}
			if mode == "reviewed checkpoint" {
				if err := a.RemoveCloudCheckpoint(t.Context(), "reviewed-operation"); err != nil {
					t.Fatal(err)
				}
				if _, err := a.LinkCloudHandoff(t.Context(), task.ID, id, false, plan.Review); err != nil {
					t.Fatal("retired review still prevents linking", err)
				}
				if checkpointRetired(filepath.Join(a.StateDir, "cloud-checkpoints"), "reviewed-operation") == nil {
					t.Fatal("old review could be applied after linking")
				}
			}
		})
	}
	a, issue, j := cloudHandoffLinkFixture(t)
	task := issue()
	plan, err := a.PlanCloudHandoffLink(task.ID, j.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.LinkCloudHandoff(t.Context(), task.ID, j.ID, false, plan.Review); err != nil {
		t.Fatal(err)
	}
	j.Undone = true
	if err := j.Save(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.cloudHandoffLink(task); err == nil {
		t.Fatal("post-link undo did not invalidate future ancestry use")
	}
}
