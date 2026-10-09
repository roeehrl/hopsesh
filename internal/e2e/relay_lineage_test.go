package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/observe"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	localruntime "github.com/roeehrl/hopsesh/internal/core/runtime"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Named temporal routes are mandatory, in addition to covering-array tests.
// Every hop uses actual CLI/private IPC/age/HTTPS/SQLite/R2/native writers.
// The three disposable machine homes run on the job's native OS; this does not
// substitute for the separate mixed-OS machine qualifications.
func TestRelayLineageSQLiteR2(t *testing.T) {
	if testing.Short() || os.Getenv("HOPSESH_RELAY_PLATFORM") != "1" {
		t.Skip("actual SQLite/R2 relay matrix")
	}
	bin := buildHopsesh(t)
	for _, mode := range []string{"push", "pull"} {
		for _, route := range []string{"ABABA", "ABCA", "ABCBCAB"} {
			for _, agents := range []string{"claude", "codex", "alternating"} {
				t.Run(mode+"/"+route+"/"+agents, func(t *testing.T) {
					// Independent routes must not consume each other's global gateway
					// rate window. Preserve production limits in every local Worker.
					ctx, _, origin, cert, client := startSQLiteRelayFixture(t, time.Minute)
					fleet := newRelayFleet(t, ctx, bin, origin, cert, client)
					sourceAgent := "claude"
					if agents == "codex" {
						sourceAgent = "codex"
					}
					current := fleet.seed(t, sourceAgent)
					var sentinels []string
					for hop := 0; hop < len(route)-1; hop++ {
						from, to := route[hop], route[hop+1]
						sentinel := fmt.Sprintf("RELAY-ROUTE-WORK-%d-UNIQUE", hop)
						sentinels = append(sentinels, sentinel)
						fleet.appendWork(t, current, sourceAgent, sentinel)
						targetAgent := sourceAgent
						if agents == "alternating" {
							targetAgent = "claude"
							if to == 'B' {
								targetAgent = "codex"
							}
						}
						current = fleet.transfer(t, from, to, current, targetAgent, mode, false, fmt.Sprintf("route-hop-%02d-123456789", hop))
						sourceAgent = targetAgent
						fleet.assertText(t, to, current, sourceAgent, sentinels, nil)
						graph, err := lineage.Read(host.LocalFS(), current.Path)
						if err != nil {
							t.Fatal(err)
						}
						if graph == nil || graph.Journey().Transfers != hop+1 {
							t.Fatalf("hop %d lost or duplicated lineage: %+v", hop, graph)
						}
						if to == 'A' && (!fleet.lastPlan.Options.OtherAccount || string(current.Key.Session) == sid) {
							t.Fatal("unverified account return did not preserve its original with a portable replica", current.Key)
						}
						// Restart the destination after the second committed hop, repeat the
						// frozen operation and verify no native write or new arrival was added.
						if hop == 1 {
							before, err := os.ReadFile(current.Path)
							if err != nil {
								t.Fatal(err)
							}
							fleet.stop[to]()
							fleet.start(t, to)
							oldSource := fleet.lastSource
							repeated := fleet.transfer(t, from, to, oldSource, targetAgent, mode, false, fmt.Sprintf("route-hop-%02d-123456789", hop))
							after, err := os.ReadFile(repeated.Path)
							if err != nil || !bytes.Equal(before, after) || repeated.Key.Session != current.Key.Session {
								t.Fatal("owner restart retry rewrote native history", err)
							}
							graph, err = lineage.Read(host.LocalFS(), repeated.Path)
							if err != nil || graph.Journey().Transfers != hop+1 {
								t.Fatal("retry duplicated arrival", err)
							}
						}
					}
					graph, err := lineage.Read(host.LocalFS(), current.Path)
					if err != nil {
						t.Fatal(err)
					}
					expectedReturns := strings.Count(route[1:], "A")
					if graph.Journey().RoundTrips != expectedReturns {
						t.Fatalf("round trips %+v; expected %d", graph.Journey(), expectedReturns)
					}
				})
			}
		}
		t.Run(mode+"/independent-original-and-sibling-forks", func(t *testing.T) {
			ctx, _, origin, cert, client := startSQLiteRelayFixture(t, time.Minute)
			fleet := newRelayFleet(t, ctx, bin, origin, cert, client)
			original := fleet.seed(t, "claude")
			before, err := os.ReadFile(original.Path)
			if err != nil {
				t.Fatal(err)
			}
			left := fleet.transfer(t, 'A', 'B', original, "codex", mode, true, "fork-left-op-123456789")
			right := fleet.transfer(t, 'A', 'C', original, "codex", mode, true, "fork-right-op-123456789")
			lg, _ := lineage.Read(host.LocalFS(), left.Path)
			rg, _ := lineage.Read(host.LocalFS(), right.Path)
			if lg.Family != rg.Family || lg.Branch == rg.Branch {
				t.Fatal("fork identities collapsed")
			}
			current, currentAgent := left, "codex"
			route := "BCBCAB"
			var leftWork []string
			for hop := 0; hop < len(route)-1; hop++ {
				sentinel := fmt.Sprintf("LEFT-FORK-ONLY-%d", hop)
				leftWork = append(leftWork, sentinel)
				fleet.appendWork(t, current, currentAgent, sentinel)
				target := "claude"
				if route[hop+1] == 'B' {
					target = "codex"
				}
				current = fleet.transfer(t, route[hop], route[hop+1], current, target, mode, false, fmt.Sprintf("left-fork-hop-%d-123456789", hop))
				currentAgent = target
				fleet.assertText(t, route[hop+1], current, target, leftWork, []string{"RIGHT-FORK-ONLY", "ORIGINAL-ONLY"})
			}
			fleet.appendWork(t, right, "codex", "RIGHT-FORK-ONLY")
			right = fleet.transfer(t, 'C', 'A', right, "claude", mode, false, "right-fork-return-123456789")
			fleet.assertText(t, 'A', right, "claude", []string{"RIGHT-FORK-ONLY"}, append(leftWork, "ORIGINAL-ONLY"))
			after, err := os.ReadFile(original.Path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("travelling forks changed original native bytes", err)
			}
			fleet.appendWork(t, original, "claude", "ORIGINAL-ONLY")
			original = fleet.transfer(t, 'A', 'C', original, "codex", mode, false, "original-independent-123456789")
			fleet.assertText(t, 'C', original, "codex", []string{"ORIGINAL-ONLY"}, append(leftWork, "RIGHT-FORK-ONLY"))
		})
	}
}

type relayFleet struct {
	ctx          context.Context
	bin, cert    string
	homes        map[byte]machineHome
	places       map[byte]location
	stop         map[byte]func()
	ownerModes   map[byte]string // native matrix may host the shared desktop backend
	deliveryMode string          // empty requires notifications; policy tests assert HTTP fallback
	lastSource   agent.Summary
	lastPlan     move.Plan
	lastResult   move.Result
}

func newRelayFleet(t *testing.T, ctx context.Context, bin, origin, cert string, client *http.Client, extra ...byte) *relayFleet {
	t.Helper()
	return newRelayFleetWithAdmission(t, ctx, bin, origin, cert, client, "fixture-admin-secret-with-32-bytes-minimum", false, extra...)
}

func newRelayFleetWithAdmission(t *testing.T, ctx context.Context, bin, origin, cert string, client *http.Client, admin string, revoke bool, extra ...byte) *relayFleet {
	t.Helper()
	root := t.TempDir()
	space, err := relay.NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	f := &relayFleet{ctx: ctx, bin: bin, cert: cert, homes: map[byte]machineHome{}, places: map[byte]location{}, stop: map[byte]func(){}}
	ids := map[byte]relay.PublicIdentity{}
	keys := append([]byte{'A', 'B', 'C'}, extra...)
	for _, key := range keys {
		if _, ok := f.homes[key]; ok {
			t.Fatal("duplicate disposable fleet endpoint")
		}
		name := "relay-" + string(key)
		m := newMachineHome(t, root, name, key == 'A')
		f.homes[key] = m
		f.places[key] = newLocation(t, name, root)
		cfg := config.Defaults()
		cfg.Peer.Receive, cfg.Relay.Enabled = true, true
		cfg.ReposDir = filepath.Join(m.home, "git")
		m.writeConfig(t, cfg)
		var id relay.PublicIdentity
		if err = json.Unmarshal(f.run(t, m, "relay", "init"), &id); err != nil {
			t.Fatal(err)
		}
		ids[key] = id
		ttl := 3600
		if revoke {
			ttl = 600 // Hosted smoke credentials have a short upper bound even on interruption.
		}
		data, _ := json.Marshal(map[string]any{"device": id.ID, "ttl": ttl})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/v1/enrollment/register", bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+admin)
		req.Header.Set("X-Hopsesh-Space", space)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var connection relay.Connection
		err = json.NewDecoder(res.Body).Decode(&connection)
		_ = res.Body.Close()
		if err != nil || res.StatusCode != 201 {
			t.Fatal("fleet enrollment", res.StatusCode, err)
		}
		connection.URL, connection.CAFile = origin, cert
		if revoke {
			// Registered before owner cleanup, so processes stop before revocation.
			t.Cleanup(func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				req, err := http.NewRequestWithContext(cleanup, http.MethodPost, origin+"/v1/enrollment/revoke", nil)
				if err != nil {
					t.Error("construct hosted credential revocation", err)
					return
				}
				req.Header.Set("Authorization", "Bearer "+connection.Token)
				req.Header.Set("X-Hopsesh-Space", connection.Space)
				res, err := client.Do(req)
				if err != nil {
					t.Error("hosted credential revocation failed")
					return
				}
				_ = res.Body.Close()
				if res.StatusCode != http.StatusOK {
					t.Error("hosted credential revocation status", res.StatusCode)
				}
			})
		}
		if err = (relay.Store{Directory: filepath.Join(m.home, "state", "relay")}).SetConnection(ctx, connection); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range keys {
		m := f.homes[key]
		cfg := config.Defaults()
		cfg.Relay.Enabled, cfg.Peer.Receive = true, true
		cfg.ReposDir = filepath.Join(m.home, "git")
		store := relay.Store{Directory: filepath.Join(m.home, "state", "relay")}
		for _, other := range keys {
			if other == key {
				continue
			}
			id := ids[other]
			methods := []string{"hello", "observe", "plan", "apply", "undo", "export", "ack"}
			if err = store.Approve(ctx, relay.Grant{Peer: id, Endpoint: id.Endpoint, Kind: "device", Roots: []string{m.repo}, Methods: methods, SendMethods: methods}); err != nil {
				t.Fatal(err)
			}
			cfg.Hosts = append(cfg.Hosts, config.Host{Name: f.homes[other].name, RelayID: id.ID, Allowed: true, Via: "relay"})
		}
		m.writeConfig(t, cfg)
		f.run(t, m, "accounts", "scan", "--machine", "local", "--json")
	}
	for _, key := range keys {
		f.start(t, key)
	}
	return f
}

func (f *relayFleet) run(t *testing.T, m machineHome, args ...string) []byte {
	t.Helper()
	cmd := exec.CommandContext(f.ctx, f.bin, args...)
	cmd.Env = m.env()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		f.logHealth(t)
		t.Fatalf("%s %v: %v\n%s", m.name, args, err, stderr.String())
	}
	return out
}

func (f *relayFleet) logHealth(t *testing.T) {
	t.Helper()
	for key, home := range f.homes {
		namespace, err := localruntime.NewNamespace(filepath.Join(home.home, "config"), filepath.Join(home.home, "state"))
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		client := localruntime.Client{Namespace: namespace}
		var health relay.Health
		if err := client.Call(ctx, "relay.status", nil, &health); err == nil {
			t.Logf("disposable owner %c relay connected=%t mode=%s rejected=%d reason=%q", key, health.Connected, health.DeliveryMode, health.Rejected, health.Error)
		}
		var snapshot observe.Snapshot
		var observation app.Observation
		if err := client.Call(ctx, "snapshot", nil, &snapshot); err == nil && json.Unmarshal(snapshot.Data, &observation) == nil {
			t.Logf("disposable owner %c fresh=%t complete=%t snapshotError=%q problems=%v", key, snapshot.Fresh(time.Now()), observation.InventoryComplete, snapshot.Error, observation.Problems)
			for _, agent := range observation.Agents {
				if agent.Error != "" {
					t.Logf("disposable owner %c agent=%s error=%q", key, agent.Agent, agent.Error)
				}
			}
			for _, remote := range observation.Remotes {
				t.Logf("disposable owner %c remote phase=%s status=%s reason=%q", key, remote.Phase, remote.Status, remote.Error)
			}
		}
		cancel()
	}
}
func (f *relayFleet) start(t *testing.T, key byte) {
	t.Helper()
	m := f.homes[key]
	cmd := exec.CommandContext(f.ctx, f.bin, "runtime", "serve")
	cmd.Env = m.env()
	if f.ownerModes[key] == "desktop" {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd = exec.CommandContext(f.ctx, executable, "-test.run=^TestRuntimeMatrixDesktopOwner$")
		cmd.Env = append(m.env(), "HOPSESH_MATRIX_DESKTOP_OWNER=1")
	}
	var logs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	namespace, err := localruntime.NewNamespace(filepath.Join(m.home, "config"), filepath.Join(m.home, "state"))
	if err != nil {
		t.Fatal(err)
	}
	client := localruntime.Client{Namespace: namespace}
	ended := false
	stop := func() {
		if ended {
			return
		}
		ended = true
		bounded, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = client.Call(bounded, "stop", nil, nil)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	f.stop[key] = stop
	t.Cleanup(stop)
	deadline := time.Now().Add(10 * time.Second)
	wantMode := f.deliveryMode
	if wantMode == "" {
		wantMode = "notifications"
	}
	var health relay.Health
	for {
		bounded, cancel := context.WithTimeout(f.ctx, 200*time.Millisecond)
		err = client.Call(bounded, "relay.status", nil, &health)
		cancel()
		if err == nil && health.Connected && health.DeliveryMode == wantMode {
			return
		}
		if time.Now().After(deadline) {
			stop()
			t.Fatalf("fleet runtime startup %c: %v connected=%t mode=%s reason=%q\n%s", key, err, health.Connected, health.DeliveryMode, health.Error, logs.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}
func (f *relayFleet) seed(t *testing.T, agentName string) agent.Summary {
	t.Helper()
	if agentName == "claude" {
		return f.find(t, 'A', "claude", sid)
	}
	l := f.places['A']
	in := codexInstall(l)
	module := codex.New()
	j, err := journal.New(t.TempDir(), journal.KindContinue, "relay route seed")
	if err != nil {
		t.Fatal(err)
	}
	h, err := l.m.For(f.ctx, module.Spec(), in, j)
	if err != nil {
		t.Fatal(err)
	}
	_, err = module.Write(f.ctx, h, in, ir.WriteRequest{SessionID: sid, Header: ir.Header{CWD: l.repo}, Mode: ir.WriteNew, Items: []ir.Item{{Node: "seed", Role: ir.RoleUser, Text: "RELAY-SEED"}}})
	if err != nil {
		t.Fatal(err)
	}
	return f.find(t, 'A', "codex", sid)
}
func (f *relayFleet) find(t *testing.T, key byte, agentName, id string) agent.Summary {
	t.Helper()
	l := f.places[key]
	var module agent.Module = claude.New()
	in := l.in
	if agentName == "codex" {
		module = codex.New()
		in = codexInstall(l)
	}
	for _, s := range listAgent(t, l, module, in) {
		if string(s.Key.Session) == id {
			return s
		}
	}
	t.Fatalf("missing %s/%s on %c", agentName, id, key)
	return agent.Summary{}
}
func (f *relayFleet) appendWork(t *testing.T, s agent.Summary, agentName, sentinel string) {
	t.Helper()
	if agentName == "claude" {
		appendTurn(t, s.Path, sentinel)
	} else {
		appendCodexTurn(t, s.Path, sentinel, "completed this turn")
	}
}
func (f *relayFleet) transfer(t *testing.T, from, to byte, s agent.Summary, target, mode string, fork bool, operation string) agent.Summary {
	t.Helper()
	f.lastSource = s
	src, dst := f.homes[from], f.homes[to]
	args := []string{"push", string(s.Key.Agent) + "/" + string(s.Key.Session), dst.name}
	local := src
	if mode == "pull" {
		local = dst
		args = []string{"pull", src.name + ":" + string(s.Key.Agent) + "/" + string(s.Key.Session)}
	}
	args = append(args, "--in", target, "--to", dst.repo, "--no-sync", "--no-mark", "--notify", "--operation-id", operation, "--yes", "--json")
	if fork {
		args = append(args, "--fork")
	}
	var result struct {
		Plan   move.Plan   `json:"plan"`
		Result move.Result `json:"result"`
	}
	if err := json.Unmarshal(f.run(t, local, args...), &result); err != nil {
		t.Fatal(err)
	}
	f.lastPlan, f.lastResult = result.Plan, result.Result
	return f.find(t, to, target, string(result.Plan.Placement.Key.Session))
}
func (f *relayFleet) assertText(t *testing.T, key byte, s agent.Summary, agentName string, want, absent []string) {
	t.Helper()
	l := f.places[key]
	var module agent.Module = claude.New()
	in := l.in
	if agentName == "codex" {
		module = codex.New()
		in = codexInstall(l)
	}
	segment := readAll(t, l, module, in, s)
	var text strings.Builder
	for _, node := range segment.Nodes {
		text.WriteString(node.Text)
		text.WriteByte('\n')
	}
	for _, sentinel := range want {
		if strings.Count(text.String(), sentinel) != 1 {
			t.Fatalf("%s missing or duplicated on %c", sentinel, key)
		}
	}
	for _, sentinel := range absent {
		if strings.Contains(text.String(), sentinel) {
			t.Fatalf("independent branch %s leaked onto %c", sentinel, key)
		}
	}
}
