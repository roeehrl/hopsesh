package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/observe"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	localruntime "github.com/roeehrl/hopsesh/internal/core/runtime"
	"github.com/roeehrl/hopsesh/internal/testkit/runtimecases"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// The desktop backend runs in its own process/environment, as it does in the
// GUI. This deliberately does not claim native window/login qualification.
func TestRuntimeMatrixDesktopOwner(t *testing.T) {
	if os.Getenv("HOPSESH_MATRIX_DESKTOP_OWNER") != "1" {
		t.Skip("subprocess helper for the native runtime matrix")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a := app.New(cfg, all.Registry(), config.StateDir(), nil)
	defer a.Catalog.Close()
	owner, err := a.StartRuntime(t.Context(), "desktop", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	<-owner.Done()
}

func TestRuntimeNativeMatrix(t *testing.T) {
	if testing.Short() || os.Getenv("HOPSESH_RUNTIME_MATRIX") != "1" {
		t.Skip("hsmatrix runtime-run supplies the selected native matrix rows")
	}
	input := os.Getenv("HOPSESH_RUNTIME_MATRIX_INPUT")
	report := os.Getenv("HOPSESH_RUNTIME_MATRIX_REPORT")
	if input == "" || report == "" {
		t.Fatal("native matrix requires explicit input and report files")
	}
	body, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	var rows []runtimecases.Row
	if err := json.Unmarshal(body, &rows); err != nil || len(rows) == 0 {
		t.Fatal("invalid or empty native matrix input", err)
	}
	seen := map[int]bool{}
	for _, row := range rows {
		if err := row.Validate(); err != nil || row.N < 1 || seen[row.N] {
			t.Fatal("invalid or duplicate native matrix row", row, err)
		}
		seen[row.N] = true
	}
	bin := buildHopsesh(t)
	var results []runtimecases.Result
	defer func() {
		body, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			t.Error(err)
			return
		}
		if err := os.WriteFile(report, body, 0600); err != nil {
			t.Error(err)
		}
	}()
	for _, row := range rows {
		start := time.Now()
		executed := false
		passed := t.Run(row.String(), func(t *testing.T) {
			runRuntimeNativeRow(t, bin, row)
			executed = true
		})
		status := "failed"
		if passed && executed {
			status = "passed"
		}
		results = append(results, runtimecases.Result{Row: row, Status: status, Seconds: time.Since(start).Seconds()})
	}
}

func runRuntimeNativeRow(t *testing.T, bin string, row runtimecases.Row) {
	t.Helper()
	if row.Host == "cloud" {
		runRuntimeCloudRow(t, bin, row)
		return
	}
	ctx, _, origin, cert, client := startSQLiteRelayFixture(t, 2*time.Minute)
	f := newRelayFleet(t, ctx, bin, origin, cert, client)
	if row.Network != "unrestricted" {
		f.applyNetworkPolicy(t, origin, client, row.Network)
	}
	if row.Network == "post-blocked" {
		f.assertRelayFailure(t, row, "observe", "POST access is required", "before-scope")
	}
	original := f.seed(t, "claude")
	f.appendWork(t, original, "claude", "NATIVE-MATRIX-SOURCE-WORK")
	before, err := os.ReadFile(original.Path)
	if err != nil {
		t.Fatal(err)
	}
	switch row.Host {
	case "one-shot":
		f.stop['A']()
		bounded, cancel := context.WithTimeout(ctx, time.Second)
		var status localruntime.Status
		err := runtimeMatrixClient(t, f.homes['A']).Call(bounded, "status", nil, &status)
		cancel()
		if err == nil {
			t.Fatal("one-shot row retained a sender runtime")
		}
	case "desktop":
		f.stop['A']()
		f.ownerModes = map[byte]string{'A': "desktop"}
		f.start(t, 'A')
		fallthrough
	case "headless":
		var status localruntime.Status
		if err := runtimeMatrixClient(t, f.homes['A']).Call(ctx, "status", nil, &status); err != nil || status.Mode != row.Host {
			t.Fatal("matrix owner did not run the requested host mode", status.Mode, err)
		}
	}
	if row.Scope == "receive-disabled" {
		m := f.homes['B']
		// Load the current disposable config without manufacturing registrations
		// or replacing its approved peer identities.
		for _, kv := range m.env() {
			key, value, _ := strings.Cut(kv, "=")
			t.Setenv(key, value)
		}
		cfg, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		cfg.Peer.Receive = false
		m.writeConfig(t, cfg)
	}
	var observedNative string
	if row.Integration == "observed" {
		observedNative = f.observationOnly(t)
	}
	if row.Failure == "owner-restart" {
		f.restartRuntime(t, 'B')
	}
	if row.Failure == "grant-revoked" {
		store := relay.Store{Directory: filepath.Join(f.homes['A'].home, "state", "relay")}
		identity, err := (relay.Store{Directory: filepath.Join(f.homes['B'].home, "state", "relay")}).Identity(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Revoke(f.ctx, identity.Public.ID); err != nil {
			t.Fatal(err)
		}
	}
	wantObserveFailure := ""
	switch row.Network {
	case "post-blocked":
		wantObserveFailure = "POST access is required"
	case "untrusted-ca":
		wantObserveFailure = "check proxy, trust roots and network access"
	}
	if row.Failure == "grant-revoked" {
		wantObserveFailure = relay.ErrRevoked.Error()
	}
	if row.Integration == "observed" {
		f.assertObservationOnly(t, row, observedNative, wantObserveFailure)
	} else if wantObserveFailure != "" {
		f.assertRelayFailure(t, row, "observe", wantObserveFailure)
	}
	fork := row.Topology == "fork"
	operation := fmt.Sprintf("runtime-matrix-row-%d-123456789", row.N)
	if row.Scope == "receive-disabled" || row.Integration == "observed" || wantObserveFailure != "" {
		if row.Transport == "ssh" {
			f.pushSSH(t, 'A', 'B', original, "codex", fork, operation, true)
		} else {
			args := []string{"push", "claude/" + string(original.Key.Session), f.homes['B'].name, "--in", "codex", "--to", f.homes['B'].repo, "--no-sync", "--no-mark", "--notify", "--operation-id", operation, "--yes", "--json"}
			if fork {
				args = append(args, "--fork")
			}
			cmd := exec.CommandContext(ctx, bin, args...)
			cmd.Env = f.homes['A'].env()
			out, err := cmd.CombinedOutput()
			want := "does not receive"
			if wantObserveFailure != "" {
				want = wantObserveFailure
			}
			if row.Integration == "observed" || row.Failure == "grant-revoked" {
				want = relay.ErrRevoked.Error()
			}
			if err == nil || !bytes.Contains(out, []byte(want)) || bytes.Contains(out, []byte("PRIVATE-PROXY-ERROR")) {
				t.Fatalf("disabled relay receiver was not explicitly refused: %v %s", err, out)
			}
		}
		files, err := filepath.Glob(filepath.Join(f.homes['B'].home, ".codex", "sessions", "*", "*", "*", "*.jsonl"))
		if err != nil || len(files) != 0 {
			t.Fatal("refused receiver acquired native conversation files", files, err)
		}
	} else {
		var arrival agent.Summary
		if row.Transport == "ssh" {
			arrival = f.pushSSH(t, 'A', 'B', original, "codex", fork, operation)
		} else {
			arrival = f.transfer(t, 'A', 'B', original, "codex", "push", fork, operation)
		}
		f.appendWork(t, arrival, "codex", "NATIVE-MATRIX-DESTINATION-WORK")
		if row.Failure == "sender-restart-after-apply" || row.Failure == "receiver-restart-after-apply" {
			beforeRetry, err := os.ReadFile(arrival.Path)
			if err != nil {
				t.Fatal(err)
			}
			key := byte('B')
			if row.Failure == "sender-restart-after-apply" {
				key = 'A'
			}
			f.restartRuntime(t, key)
			retried := f.transfer(t, 'A', 'B', original, "codex", "push", fork, operation)
			afterRetry, err := os.ReadFile(retried.Path)
			if err != nil || arrival.Key != retried.Key || arrival.Path != retried.Path || !bytes.Equal(beforeRetry, afterRetry) {
				t.Fatal("retry after owner restart replaced destination work or created another native session", err)
			}
			files, err := filepath.Glob(filepath.Join(f.homes['B'].home, ".codex", "sessions", "*", "*", "*", "*.jsonl"))
			if err != nil || len(files) != 1 {
				t.Fatal("post-apply retry left an extra or displaced native session", files, err)
			}
			listed, listErr := os.Stat(files[0])
			returned, returnErr := os.Stat(arrival.Path)
			if listErr != nil || returnErr != nil || !os.SameFile(listed, returned) {
				t.Fatal("retry returned a different native file", listErr, returnErr)
			}
		}
		f.assertText(t, 'B', arrival, "codex", []string{"NATIVE-MATRIX-SOURCE-WORK", "NATIVE-MATRIX-DESTINATION-WORK"}, nil)
		graph, err := lineage.Read(host.LocalFS(), arrival.Path)
		if err != nil || graph == nil || graph.Journey().Fork != fork || len(graph.ActiveHops()) != 1 || graph.ActiveHops()[0].ID != operation || arrival.Key.Session == original.Key.Session {
			t.Fatal("runtime matrix lost native account separation or causal lineage", err)
		}
	}
	after, err := os.ReadFile(original.Path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("runtime matrix mutated the source native conversation", err)
	}
}

func (f *relayFleet) restartRuntime(t *testing.T, key byte) {
	t.Helper()
	var before, after localruntime.Status
	client := runtimeMatrixClient(t, f.homes[key])
	if err := client.Call(f.ctx, "status", nil, &before); err != nil {
		t.Fatal(err)
	}
	f.stop[key]()
	f.start(t, key)
	if err := client.Call(f.ctx, "status", nil, &after); err != nil || before.Epoch == after.Epoch || before.Mode != after.Mode {
		t.Fatal("owner restart did not replace epoch while preserving host mode", err)
	}
}

const observationPrivateText = "OBSERVATION-ONLY-MUST-NOT-EXPORT-THIS-TURN"

// Populate a remote native session, then restrict only the A -> B direction to
// observation. Reverse-direction and unrelated peer approvals stay unchanged.
func (f *relayFleet) observationOnly(t *testing.T) string {
	t.Helper()
	loc := f.places['B']
	module := claude.New()
	j, err := journal.New(t.TempDir(), journal.KindContinue, "observation-only fixture")
	if err != nil {
		t.Fatal(err)
	}
	h, err := loc.m.For(f.ctx, module.Spec(), loc.in, j)
	if err != nil {
		t.Fatal(err)
	}
	_, err = module.Write(f.ctx, h, loc.in, ir.WriteRequest{Mode: ir.WriteNew, SessionID: sid, Header: ir.Header{CWD: f.homes['B'].repo, Title: "Observation fixture"}, Items: []ir.Item{{Node: "public-title", Role: ir.RoleUser, Text: "Observation fixture"}, {Node: "private-user", Role: ir.RoleUser, Text: observationPrivateText + ": user"}, {Node: "private-turn", Role: ir.RoleAgent, Text: observationPrivateText + ": assistant"}}})
	if err != nil {
		t.Fatal(err)
	}
	stores := map[byte]relay.Store{}
	ids := map[byte]string{}
	for _, key := range []byte{'A', 'B'} {
		store := relay.Store{Directory: filepath.Join(f.homes[key].home, "state", "relay")}
		identity, err := store.Identity(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		stores[key], ids[key] = store, identity.Public.ID
	}
	for _, key := range []byte{'A', 'B'} {
		other := byte('A')
		if key == 'A' {
			other = 'B'
		}
		grant, err := stores[key].Grant(f.ctx, ids[other])
		if err != nil {
			t.Fatal(err)
		}
		if key == 'A' {
			grant.SendMethods = []string{"observe"}
		} else {
			grant.Methods = []string{"observe"}
		}
		if err := stores[key].Approve(f.ctx, grant); err != nil {
			t.Fatal(err)
		}
	}
	return f.find(t, 'B', "claude", sid).Path
}

func (f *relayFleet) assertObservationOnly(t *testing.T, row runtimecases.Row, native, wantObserveFailure string) {
	t.Helper()
	before, err := os.ReadFile(native)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := (relay.Store{Directory: filepath.Join(f.homes['B'].home, "state", "relay")}).Identity(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	call := func(method string, result any) error {
		return runtimeMatrixClient(t, f.homes['A']).Call(f.ctx, "relay.call", map[string]any{"peer": identity.Public.ID, "operation": fmt.Sprintf("matrix-observe-%d-%s", row.N, method), "method": method, "params": map[string]any{"refresh": true}}, result)
	}
	if wantObserveFailure != "" {
		f.assertRelayFailure(t, row, "observe", wantObserveFailure)
	} else {
		var snapshot observe.Snapshot
		if err := call("observe", &snapshot); err != nil || !snapshot.Fresh(time.Now()) {
			t.Fatal("observation-only permission did not deliver fresh metadata", err)
		}
		var observation app.Observation
		if err := json.Unmarshal(snapshot.Data, &observation); err != nil || !observation.InventoryComplete || len(observation.Entries) != 1 || observation.Entries[0].Session.Key.Session != sid || observation.Receive || bytes.Contains(snapshot.Data, []byte(observationPrivateText)) {
			t.Fatal("observation-only response lost metadata or exposed native contents/receiving", err)
		}
	}
	for _, method := range []string{"preview", "export", "plan", "apply", "undo"} {
		var result json.RawMessage
		if err := call(method, &result); err == nil || !strings.Contains(err.Error(), relay.ErrRevoked.Error()) || len(result) != 0 {
			t.Fatal("observation-only grant permitted a data or mutation method", method, err)
		}
	}
	after, err := os.ReadFile(native)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("observation-only methods changed remote native data", err)
	}
}

func (f *relayFleet) assertRelayFailure(t *testing.T, row runtimecases.Row, method, want string, stage ...string) {
	t.Helper()
	identity, err := (relay.Store{Directory: filepath.Join(f.homes['B'].home, "state", "relay")}).Identity(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	var result json.RawMessage
	err = runtimeMatrixClient(t, f.homes['A']).Call(f.ctx, "relay.call", map[string]any{"peer": identity.Public.ID, "operation": fmt.Sprintf("matrix-refused-%d-%s-%s", row.N, method, strings.Join(stage, "-")), "method": method, "params": map[string]any{"refresh": true}}, &result)
	if err == nil || !strings.Contains(err.Error(), want) || len(result) != 0 || strings.Contains(err.Error(), "PRIVATE-PROXY-ERROR") {
		t.Fatal("denied policy did not return an empty, classified, sanitized failure", err)
	}
}

// All owners use a policy proxy and production reconciliation intervals. A
// blocked upgrade permits HTTPS fallback; denied POST and bad TLS must fail
// without bypassing the policy or installing any native conversation.
func (f *relayFleet) applyNetworkPolicy(t *testing.T, origin string, upstream *http.Client, policy string) {
	t.Helper()
	for _, stop := range f.stop {
		stop()
	}
	target, err := url.Parse(origin)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = upstream.Transport
	var denied atomic.Int64
	var posts atomic.Int64
	var requests atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/v1/notifications" || r.Header.Get("Upgrade") != "" {
			denied.Add(1)
			http.Error(w, "WebSocket upgrades disabled by fixture network policy", http.StatusNotImplemented)
			return
		}
		if policy == "post-blocked" && r.Method == http.MethodPost {
			posts.Add(1)
			http.Error(w, "PRIVATE-PROXY-ERROR", http.StatusMethodNotAllowed)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	caFile := filepath.Join(t.TempDir(), "policy-ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if policy == "untrusted-ca" {
		caFile = f.cert // the fixture origin's independent CA cannot authenticate this proxy
		body, err := os.ReadFile(caFile)
		block, _ := pem.Decode(body)
		if err != nil || block == nil || bytes.Equal(block.Bytes, server.Certificate().Raw) {
			t.Fatal("policy fixture did not supply an independent untrusted certificate", err)
		}
		f.healthError = "check proxy, trust roots and network access"
	}
	if policy == "post-blocked" {
		// GET polling may succeed while inventory publication or acknowledgement
		// POSTs fail. Both states are truthful; the explicit call below must
		// still prove POST refusal and absence of private response content.
		f.healthError = "POST access is required"
		f.healthMayConnect = true
	}
	f.deliveryMode = "http-fallback"
	for _, key := range []byte{'A', 'B', 'C'} {
		store := relay.Store{Directory: filepath.Join(f.homes[key].home, "state", "relay")}
		connection, err := store.Connection(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		connection.URL, connection.CAFile = server.URL, caFile
		if err := store.SetConnection(f.ctx, connection); err != nil {
			t.Fatal(err)
		}
		f.start(t, key)
	}
	if policy != "untrusted-ca" && denied.Load() < int64(len(f.homes)) {
		t.Fatal("HTTP fallback matrix did not reject every owner's WebSocket upgrade")
	}
	t.Cleanup(func() {
		if policy == "untrusted-ca" && requests.Load() != 0 {
			t.Error("untrusted TLS policy accepted authenticated HTTP traffic")
		}
		if policy == "post-blocked" && posts.Load() == 0 {
			t.Error("POST-denied policy was never exercised")
		}
	})
}

func runtimeMatrixClient(t *testing.T, home machineHome) localruntime.Client {
	t.Helper()
	namespace, err := localruntime.NewNamespace(filepath.Join(home.home, "config"), filepath.Join(home.home, "state"))
	if err != nil {
		t.Fatal(err)
	}
	return localruntime.Client{Namespace: namespace}
}
