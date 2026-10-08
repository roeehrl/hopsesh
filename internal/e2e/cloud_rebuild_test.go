package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/cloudintegration"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	"github.com/roeehrl/hopsesh/internal/localstate"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Provider-independent native lifecycle qualification: fresh CLI keys and claims
// use the actual SQLite/R2 service; immutable vendor records and private receipts
// prove repeated checkpoint ancestry without reusing access authority.
func TestCloudRebuildCheckpointSQLiteR2(t *testing.T) { cloudRebuildCheckpoint(t, nil) }

func TestCloudHandoffCheckpointSQLiteR2(t *testing.T) {
	for _, fork := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved-handoff", true: "cloud-fork"}[fork], func(t *testing.T) { cloudRebuildCheckpoint(t, &fork) })
	}
}

func cloudRebuildCheckpoint(t *testing.T, linkedFork *bool) {
	if testing.Short() || os.Getenv("HOPSESH_RELAY_PLATFORM") != "1" {
		t.Skip("actual SQLite/R2 cloud checkpoint lifecycle")
	}
	ctx, _, origin, cert, client := startSQLiteRelayFixture(t, 2*time.Minute)
	bin := buildHopsesh(t)
	f := newRelayFleet(t, ctx, bin, origin, cert, client)
	desktop := f.homes['A']
	cloud := newMachineHome(t, t.TempDir(), "cloud-rebuilt", linkedFork == nil)
	const savedBrief = "SAVED-CLOUD-HANDOFF-CONTEXT: original task and constraints"
	if linkedFork != nil {
		loc := newLocation(t, cloud.name, filepath.Dir(cloud.home))
		module := claude.New()
		j, err := journal.New(t.TempDir(), journal.KindContinue, "disposable provider transcript")
		if err != nil {
			t.Fatal(err)
		}
		h, err := loc.m.For(ctx, module.Spec(), loc.in, j)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := module.Write(ctx, h, loc.in, ir.WriteRequest{Mode: ir.WriteNew, SessionID: sid, Header: ir.Header{CWD: cloud.repo, Created: time.Now()}, Items: []ir.Item{{Node: "cloud-brief", Role: ir.RoleUser, Text: savedBrief}, {Node: "cloud-work", Role: ir.RoleAgent, Text: "CLOUD-WORK-AFTER-HANDOFF"}}}); err != nil {
			t.Fatal(err)
		}
	}
	cloud.writeConfig(t, config.Defaults())
	transcript := filepath.Join(cloud.home, ".claude", "projects", claude.Slug(cloud.repo), sid+".jsonl")
	expectedBytes, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	store := relay.Store{Directory: filepath.Join(desktop.home, "state", "relay")}
	owner, err := store.Identity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := store.Connection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	admissions := relay.AdmissionStore{Directory: filepath.Join(desktop.home, "state", "cloud-admissions")}
	runCloud := func(input []byte, args ...string) []byte {
		t.Helper()
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env, cmd.Stdin = cloud.env(), bytes.NewReader(input)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("cloud lifecycle %s failed: %v %s", args[1], err, stderr.String())
		}
		if bytes.Contains(out, []byte(`"token"`)) || bytes.Contains(out, []byte(`"ticket"`)) {
			t.Fatal("cloud claim exposed a credential")
		}
		return out
	}
	seenKeys := map[string]bool{}
	processes := map[string]<-chan struct{}{}
	nativeID := sid
	start := func(resume string) (cloudintegration.Incarnation, relay.AdmissionRecord, func()) {
		t.Helper()
		var incarnation cloudintegration.Incarnation
		args := []string{"cloud-integration", "prepare", "--provider", "claude-hosted", "--session", nativeID, "--workspace", cloud.repo, "--native-root", filepath.Join(cloud.home, ".claude", "projects"), "--transcript", transcript, "--allow-transcript-export"}
		if err := json.Unmarshal(runCloud(nil, args...), &incarnation); err != nil {
			t.Fatal(err)
		}
		if seenKeys[incarnation.Public.ID] {
			t.Fatal("rebuilt incarnation reused access keys")
		}
		seenKeys[incarnation.Public.ID] = true
		record, err := admissions.IssueTask(ctx, owner, connection, "claude-hosted", nativeID, 10*time.Minute, resume)
		if err != nil {
			t.Fatal(err)
		}
		invitation, err := localstate.ReadPrivateFile(record.Path, 8192)
		if err != nil {
			t.Fatal(err)
		}
		runCloud(invitation, "cloud-integration", "claim", incarnation.Directory, "--fingerprint", owner.Public.ID, "--ca-file", cert)
		if err = store.Approve(ctx, relay.Grant{Peer: incarnation.Public, Endpoint: incarnation.Public.Endpoint, Kind: "cloud-session", SendMethods: []string{"observe", "export"}, Expires: incarnation.Expires.Unix()}); err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, bin, "cloud-integration", "serve", incarnation.Directory)
		cmd.Env = cloud.env()
		if err = cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		processes[incarnation.ID] = done
		go func() { _ = cmd.Wait(); close(done) }()
		ended := false
		stop := func() {
			if !ended {
				ended = true
				select {
				case <-done:
					return
				default:
				}
				_ = cmd.Process.Kill()
				<-done
			}
		}
		t.Cleanup(stop)
		return incarnation, record, stop
	}
	importCheckpoint := func(instance cloudintegration.Incarnation, target, operation string, fork bool) (*lineage.Manifest, move.Plan, move.Result) {
		t.Helper()
		args := []string{"cloud-integration", "import", instance.Public.ID, "--to", desktop.repo, "--in", target, "--operation-id", operation, "--yes"}
		if fork {
			args = append(args, "--fork")
		}
		var result struct {
			Plan   move.Plan   `json:"plan"`
			Result move.Result `json:"result"`
		}
		if err := json.Unmarshal(f.run(t, desktop, args...), &result); err != nil {
			t.Fatal(err)
		}
		if result.Result.Journal == "" {
			t.Fatal("cloud checkpoint had no durable native outcome")
		}
		destination := f.find(t, 'A', target, string(result.Plan.Placement.Key.Session))
		receipt, err := lineage.Read(host.LocalFS(), destination.Path)
		if err != nil || receipt == nil {
			t.Fatal("native checkpoint lineage receipt missing", err)
		}
		return receipt, result.Plan, result.Result
	}
	var handoffID, handoffFamily string
	if linkedFork != nil {
		handoffID, handoffFamily = saveCloudHandoffFixture(t, f, owner.Public.Endpoint, savedBrief)
	}
	var family, task string
	wantFork := linkedFork != nil && *linkedFork
	var old cloudintegration.Incarnation
	var stopPrevious func()
	var originalRevisionCount int
	for rebuild := 0; rebuild < 3; rebuild++ {
		if rebuild == 1 {
			nativeID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
			expectedBytes = bytes.ReplaceAll(expectedBytes, []byte(sid), []byte(nativeID))
			transcript = filepath.Join(filepath.Dir(transcript), nativeID+".jsonl")
			if err = os.WriteFile(transcript, expectedBytes, 0600); err != nil {
				t.Fatal(err)
			}
		}
		instance, record, stop := start(task)
		if rebuild > 0 {
			select {
			case <-processes[old.ID]:
			case <-time.After(5 * time.Second):
				t.Fatal("superseded cloud CLI process did not drain after native ID changed")
			}
			stopPrevious()
			if _, err = cloudintegration.Load(ctx, old.Directory); err == nil {
				t.Fatal("old connector survived rebuild")
			}
			if record.TaskID != task {
				t.Fatal("rebuild changed explicitly resumed task")
			}
		}
		task = record.TaskID
		if rebuild == 0 && linkedFork != nil {
			args := []string{"cloud-integration", "link-handoff", task, handoffID, "--yes"}
			if *linkedFork {
				args = append(args, "--fork")
			}
			f.run(t, desktop, args...)
		}
		original, plan, outcome := importCheckpoint(instance, "claude", fmt.Sprintf("cloud-rebuild-original-%d", rebuild), false)
		if rebuild == 0 {
			family = original.Family
			if linkedFork != nil && family != handoffFamily {
				t.Fatal("saved local handoff ancestry was not recovered")
			}
		}
		if rebuild == 0 {
			originalRevisionCount = len(original.Revisions)
		} else if len(original.Revisions) != originalRevisionCount {
			t.Fatal("rebuilt unchanged native anchors duplicated logical conversation revisions", len(original.Revisions), originalRevisionCount)
		}
		if original.Family != family || original.Journey().Fork != wantFork || plan.Key.Profile != task {
			t.Fatal("rebuild lost original task lineage", original.Journey())
		}
		offset := 0
		if linkedFork != nil && !wantFork {
			offset = 1
		}
		if original.Journey().Transfers != rebuild*2+1+offset {
			t.Fatal("rebuild duplicated or erased original transfer ancestry", original.Journey())
		}
		var retried struct {
			Result move.Result `json:"result"`
		}
		args := []string{"cloud-integration", "import", instance.Public.ID, "--to", desktop.repo, "--in", "claude", "--operation-id", fmt.Sprintf("cloud-rebuild-original-%d", rebuild), "--yes"}
		if err = json.Unmarshal(f.run(t, desktop, args...), &retried); err != nil || retried.Result.Journal != outcome.Journal {
			t.Fatal("cloud checkpoint retry duplicated native outcome", err)
		}
		// Independently fork from each frozen checkpoint. Later original imports
		// retain branch-local counts and never adopt the travelling child's line.
		fork, _, _ := importCheckpoint(instance, "codex", fmt.Sprintf("cloud-rebuild-fork-%d", rebuild), true)
		if fork.Family != family || fork.Branch == original.Branch || !fork.Journey().Fork {
			t.Fatal("checkpoint fork collapsed into original")
		}
		stop()
		old = instance
		// A same-task ordinary checkpoint in Codex contributes to the original's
		// causal journey, while the prior fork keeps its independent branch.
		instance, record, stop = start(task)
		converted, convertedPlan, _ := importCheckpoint(instance, "codex", fmt.Sprintf("cloud-rebuild-convert-%d", rebuild), false)
		if converted.Family != family || converted.Journey().Fork != wantFork {
			t.Fatal("cross-agent rebuild inherited child branch")
		}
		if linkedFork != nil && rebuild == 2 {
			current := f.find(t, 'A', "codex", string(convertedPlan.Placement.Key.Session))
			base := converted.Journey().Transfers
			route := "ABCBCAB"
			for hop := 0; hop < len(route)-1; hop++ {
				target := "codex"
				if route[hop+1] == 'B' {
					target = "claude"
				}
				current = f.transfer(t, route[hop], route[hop+1], current, target, "push", false, fmt.Sprintf("linked-cloud-travel-%d-1234", hop))
				f.assertText(t, route[hop+1], current, target, []string{savedBrief, "CLOUD-WORK-AFTER-HANDOFF"}, nil)
				graph, err := lineage.Read(host.LocalFS(), current.Path)
				if err != nil || graph == nil || graph.Family != family || graph.Journey().Transfers != base+hop+1 || graph.Journey().Fork != wantFork {
					t.Fatal("multi-hop return lost task context, ancestry or branch-local counters", err)
				}
			}
		}
		stopPrevious = stop
		old = instance
	}
	stopPrevious()
	instance, record, stop := start("")
	defer stop()
	independent, _, _ := importCheckpoint(instance, "claude", "cloud-independent-same-native-id", false)
	if record.TaskID == task || independent.Family == family {
		t.Fatal("a new task with the same native ID inherited old lineage")
	}
	current, err := os.ReadFile(transcript)
	if err != nil || !bytes.Equal(expectedBytes, current) {
		t.Fatal("checkpoint lifecycle changed cloud vendor bytes", err)
	}
}

// A durable local handoff receipt is fixture input here; the vendor creation
// itself is deliberately not claimed as a hosted-provider qualification.
func saveCloudHandoffFixture(t *testing.T, f *relayFleet, endpoint, brief string) (string, string) {
	t.Helper()
	source := f.seed(t, "claude")
	loc := f.places['A']
	seg := readAll(t, loc, claude.New(), loc.in, source)
	m := lineage.NewNative(endpoint, source.Key)
	from := m.Upsert(lineage.Replica{Endpoint: endpoint, Location: f.homes['A'].name, Key: source.Key})
	st, err := m.Observe(from, &seg)
	if err != nil {
		t.Fatal(err)
	}
	key := agent.SessionKey{Agent: "claude", Session: "session_outer_saved_handoff"}
	to := m.Upsert(lineage.Replica{Endpoint: "cloud:claude-cloud:fixture-account", Location: "claude-cloud", Key: key})
	m.Deliver(to, ir.Cursor{}, nil, st.Heads, []string{"handoff briefing; native reasoning stays at source"})
	state := filepath.Join(f.homes['A'].home, "state")
	j, err := journal.New(state, journal.KindHandoff, "Saved local handoff")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.AppendHop(lineage.Hop{ID: j.ID, Time: j.Time, From: from, To: to, Kind: lineage.HopHandoff}); err != nil {
		t.Fatal(err)
	}
	j.Entries = append(j.Entries, journal.Entry{Op: journal.OpCloud, Cloud: "claude-cloud", Key: &key})
	if err := j.WriteReceipt(host.LocalFS(), f.homes['A'].name, lineage.PathFor(source.Path), m.Encode(), false); err != nil {
		t.Fatal(err)
	}
	if err := move.SaveHandoff(state, &move.Handoff{Journal: j.ID, Time: j.Time, Machine: f.homes['A'].name, Cloud: "claude-cloud", Session: key, Brief: brief, Title: "Saved local handoff"}); err != nil {
		t.Fatal(err)
	}
	return j.ID, m.Family
}
