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

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	localruntime "github.com/roeehrl/hopsesh/internal/core/runtime"
	"github.com/roeehrl/hopsesh/internal/testkit/runtimecases"
	"github.com/roeehrl/hopsesh/sdk/agent"
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
	ctx, _, origin, cert, client := startSQLiteRelayFixture(t, 2*time.Minute)
	f := newRelayFleet(t, ctx, bin, origin, cert, client)
	if row.Network == "websocket-blocked" {
		f.blockWebSockets(t, origin, client)
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
	if row.Failure == "owner-restart" {
		var before, after localruntime.Status
		receiver := runtimeMatrixClient(t, f.homes['B'])
		if err := receiver.Call(ctx, "status", nil, &before); err != nil {
			t.Fatal(err)
		}
		f.stop['B']()
		f.start(t, 'B')
		if err := receiver.Call(ctx, "status", nil, &after); err != nil || before.Epoch == after.Epoch {
			t.Fatal("receiver restart did not replace runtime epoch", err)
		}
	}
	fork := row.Topology == "fork"
	operation := fmt.Sprintf("runtime-matrix-row-%d-123456789", row.N)
	if row.Scope == "receive-disabled" {
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
			if err == nil || !bytes.Contains(out, []byte("does not receive")) {
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

// All owners are moved behind a TLS-verifying policy proxy that rejects every
// WebSocket upgrade. The mailbox and encrypted native transfers must continue
// through the production HTTP fallback, with its real reconciliation intervals.
func (f *relayFleet) blockWebSockets(t *testing.T, origin string, upstream *http.Client) {
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
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/notifications" || r.Header.Get("Upgrade") != "" {
			denied.Add(1)
			http.Error(w, "WebSocket upgrades disabled by fixture network policy", http.StatusNotImplemented)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	caFile := filepath.Join(t.TempDir(), "policy-ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
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
	if denied.Load() < int64(len(f.homes)) {
		t.Fatal("HTTP fallback matrix did not reject every owner's WebSocket upgrade")
	}
}

func runtimeMatrixClient(t *testing.T, home machineHome) localruntime.Client {
	t.Helper()
	namespace, err := localruntime.NewNamespace(filepath.Join(home.home, "config"), filepath.Join(home.home, "state"))
	if err != nil {
		t.Fatal(err)
	}
	return localruntime.Client{Namespace: namespace}
}
