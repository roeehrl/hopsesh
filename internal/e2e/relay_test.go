package e2e

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/cloudintegration"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	localruntime "github.com/roeehrl/hopsesh/internal/core/runtime"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Exercise the real CLI/private IPC/age/HTTPS/Worker/receiver/native writer path.
// All homes, keys, credentials and native sessions are disposable fixtures.
func TestRelayPushSurvivesReceiverRestartAndPeerOwnedUndo(t *testing.T) {
	if testing.Short() {
		t.Skip("builds CLI and starts isolated runtime processes")
	}
	node, err := exec.LookPath("node")
	fixture, _ := filepath.Abs("../../infrastructure/relay/server-fixture.mjs")
	if err != nil {
		if os.Getenv("HOPSESH_RELAY_REQUIRE_NODE") == "1" {
			t.Fatal(err)
		}
		t.Skip("relay fixture needs Node")
	}
	if _, err = os.Stat(filepath.Join(filepath.Dir(fixture), "node_modules", "jose")); err != nil {
		if os.Getenv("HOPSESH_RELAY_REQUIRE_NODE") == "1" {
			t.Fatal("npm ci in infrastructure/relay is required")
		}
		t.Skip("relay dependencies are qualified in the relay matrix")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	root := t.TempDir()
	certFile, keyFile, pool := relayFixtureCertificate(t, root)
	server := exec.CommandContext(ctx, node, fixture)
	server.Env = append(os.Environ(), "HOPSESH_RELAY_FIXTURE_CERT="+certFile, "HOPSESH_RELAY_FIXTURE_KEY="+keyFile)
	var ready struct {
		URL string `json:"url"`
	}
	if os.Getenv("HOPSESH_RELAY_PLATFORM") == "1" {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		_ = listener.Close()
		server = exec.CommandContext(ctx, node, filepath.Join(filepath.Dir(fixture), "node_modules", "wrangler", "wrangler-dist", "cli.js"), "dev", "--local", "--ip", "127.0.0.1", "--port", fmt.Sprint(port), "--inspector-port", "0", "--local-protocol", "https", "--https-key-path", keyFile, "--https-cert-path", certFile, "--persist-to", filepath.Join(root, "platform-state"), "--var", "ENROLLMENT_ADMIN:fixture-admin-secret-with-32-bytes-minimum", "--log-level", "error", "--show-interactive-dev-session=false")
		prepareRelayFixture(server)
		server.Dir = filepath.Dir(fixture)
		server.Env = append(os.Environ(), "WRANGLER_SEND_METRICS=false")
		log, err := os.Create(filepath.Join(root, "platform.log"))
		if err != nil {
			t.Fatal(err)
		}
		defer log.Close()
		server.Stdout, server.Stderr = log, log
		if err = server.Start(); err != nil {
			t.Fatal(err)
		}
		ready.URL = fmt.Sprintf("https://127.0.0.1:%d", port)
	} else {
		stdout, err := server.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		server.Stderr = os.Stderr
		if err = server.Start(); err != nil {
			t.Fatal(err)
		}
		line, err := bufio.NewReader(stdout).ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(line, &ready); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { _ = server.Process.Signal(os.Interrupt); cancel(); _ = server.Wait() }()
	httpClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}, Timeout: 10 * time.Second}
	deadline := time.Now().Add(30 * time.Second)
	for {
		res, err := httpClient.Get(ready.URL + "/v1/capabilities")
		if err == nil {
			_ = res.Body.Close()
			if res.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			log, _ := os.ReadFile(filepath.Join(root, "platform.log"))
			t.Fatalf("local platform did not become ready: %v\n%s", err, log)
		}
		time.Sleep(100 * time.Millisecond)
	}
	bin := buildHopsesh(t)
	box := newMachineHome(t, root, "relay-box", false)
	here := newMachineHome(t, root, "relay-here", true)
	run := func(m machineHome, args ...string) []byte {
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env = append(m.env(), "SSL_CERT_FILE="+certFile)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", m.name, args, err, out)
		}
		return out
	}
	setup := func(m machineHome) relay.PublicIdentity {
		cfg := config.Defaults()
		cfg.Peer.Receive = true
		cfg.Relay.Enabled = true
		cfg.ReposDir = filepath.Join(m.home, "git")
		m.writeConfig(t, cfg)
		var id relay.PublicIdentity
		if err = json.Unmarshal(run(m, "relay", "init"), &id); err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(map[string]any{"device": id.ID, "ttl": 3600})
		req, err := http.NewRequestWithContext(ctx, "POST", ready.URL+"/v1/enrollment/register", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer fixture-admin-secret-with-32-bytes-minimum")
		req.Header.Set("X-Hopsesh-Space", "relay-e2e-space-1234")
		res, err := httpClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != 201 {
			t.Fatal("fixture enrollment", res.StatusCode)
		}
		var connection relay.Connection
		if err = json.NewDecoder(res.Body).Decode(&connection); err != nil {
			t.Fatal(err)
		}
		connection.URL = ready.URL
		connection.CAFile = certFile
		store := relay.Store{Directory: filepath.Join(m.home, "state", "relay")}
		if err = store.SetConnection(ctx, connection); err != nil {
			t.Fatal(err)
		}
		return id
	}
	boxID, hereID := setup(box), setup(here)
	for _, p := range []struct {
		local  machineHome
		remote relay.PublicIdentity
	}{{box, hereID}, {here, boxID}} {
		store := relay.Store{Directory: filepath.Join(p.local.home, "state", "relay")}
		if err = store.Approve(ctx, relay.Grant{Peer: p.remote, Endpoint: p.remote.Endpoint, Kind: "device", Roots: []string{p.local.repo}, Methods: []string{"hello", "observe", "plan", "apply", "undo", "export", "ack"}, SendMethods: []string{"hello", "observe", "plan", "apply", "undo", "export", "ack"}}); err != nil {
			t.Fatal(err)
		}
	}
	hereCfg := config.Defaults()
	hereCfg.Relay.Enabled = true
	hereCfg.Peer.Receive = true
	hereCfg.ReposDir = filepath.Join(here.home, "git")
	hereCfg.Hosts = []config.Host{{Name: box.name, RelayID: boxID.ID, Allowed: true, Via: "relay"}}
	here.writeConfig(t, hereCfg)
	boxCfg := config.Defaults()
	boxCfg.Relay.Enabled, boxCfg.Peer.Receive = true, true
	boxCfg.ReposDir = filepath.Join(box.home, "git")
	boxCfg.Hosts = []config.Host{{Name: here.name, RelayID: hereID.ID, Allowed: true, Via: "relay"}}
	box.writeConfig(t, boxCfg)
	start := func(m machineHome) (localruntime.Client, func()) {
		cmd := exec.CommandContext(ctx, bin, "runtime", "serve")
		cmd.Env = append(m.env(), "SSL_CERT_FILE="+certFile)
		var output bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &output
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		n, err := localruntime.NewNamespace(filepath.Join(m.home, "config"), filepath.Join(m.home, "state"))
		if err != nil {
			t.Fatal(err)
		}
		client := localruntime.Client{Namespace: n}
		deadline := time.Now().Add(10 * time.Second)
		for {
			var status localruntime.Status
			probe, done := context.WithTimeout(ctx, 200*time.Millisecond)
			err = client.Call(probe, "status", nil, &status)
			done()
			if err == nil {
				var health relay.Health
				probe, done = context.WithTimeout(ctx, 200*time.Millisecond)
				err = client.Call(probe, "relay.status", nil, &health)
				done()
				if err == nil && !health.Connected {
					err = fmt.Errorf("relay startup pending: %s", health.Error)
				}
			}
			if err == nil {
				break
			}
			if time.Now().After(deadline) {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				t.Fatalf("runtime or relay failed: %v\n%s", err, output.String())
			}
			time.Sleep(20 * time.Millisecond)
		}
		ended := false
		stop := func() {
			if ended {
				return
			}
			ended = true
			stopCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			_ = client.Call(stopCtx, "stop", nil, nil)
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		t.Cleanup(stop)
		return client, stop
	}
	_, stopBox := start(box)
	_, stopHere := start(here)
	args := []string{"push", sid, box.name, "--in", "codex", "--to", box.repo, "--no-sync", "--no-mark", "--operation-id", "relay-restart-op-123456", "--yes", "--json"}
	planJSON := run(here, append(append([]string(nil), args...), "--dry-run")...)
	stopBox()
	_, _ = start(box)
	var result struct {
		Plan struct {
			Placement struct{ Key struct{ Session string } }
		}
		Result struct {
			Journal string
			Result  struct{ Journal string }
		}
	}
	if err = json.Unmarshal(run(here, args...), &result); err != nil {
		t.Fatal(err)
	}
	var planned struct {
		Placement struct{ Key struct{ Session string } }
	}
	if err = json.Unmarshal(planJSON, &planned); err != nil {
		t.Fatal(err)
	}
	if planned.Placement.Key.Session == "" || result.Plan.Placement.Key.Session != planned.Placement.Key.Session {
		t.Fatal("restart changed the accepted native session ID")
	}
	if result.Result.Journal == "" || result.Result.Result.Journal == "" {
		t.Fatal("missing source or destination undo journal")
	}
	// Restart both owners and repeat the committed operation. Source-side marks
	// and journals must not be duplicated, and the original frozen request must
	// remain valid even though its lineage receipt changed after completion.
	stopHere()
	hereClient, _ := start(here)
	var repeated struct {
		Result struct {
			Journal string
			Result  struct{ Journal string }
		}
	}
	if err = json.Unmarshal(run(here, args...), &repeated); err != nil {
		t.Fatal(err)
	}
	if repeated.Result.Journal != result.Result.Journal || repeated.Result.Result.Journal != result.Result.Result.Journal {
		t.Fatal("retry duplicated a source or destination journal", result, repeated)
	}
	journals, err := filepath.Glob(filepath.Join(here.home, "state", "journal", "*", "journal.json"))
	if err != nil || len(journals) != 1 {
		t.Fatal("source retry created another journal", journals, err)
	}
	run(here, "undo", result.Result.Journal, "--yes", "--json")
	// Reverse direction: source inventory is obtained over the relay, the exact
	// native package is frozen, and the local planner commits its source receipt.
	pullArgs := []string{"pull", here.name + ":claude/" + sid, "--in", "codex", "--to", box.repo, "--no-sync", "--no-mark", "--operation-id", "relay-pull-op-12345678", "--yes", "--json"}
	var pullResult struct {
		Result move.Result `json:"result"`
	}
	if err = json.Unmarshal(run(box, pullArgs...), &pullResult); err != nil {
		t.Fatal(err)
	}
	if pullResult.Result.Journal == "" {
		t.Fatal("pull returned no destination journal")
	}
	stopBox()
	_, _ = start(box)
	var retryPull struct {
		Result move.Result `json:"result"`
	}
	if err = json.Unmarshal(run(box, pullArgs...), &retryPull); err != nil {
		t.Fatal(err)
	}
	if retryPull.Result.Journal != pullResult.Result.Journal {
		t.Fatal("pull retry duplicated native installation")
	}
	run(box, "undo", pullResult.Result.Journal, "--yes", "--json")
	qualifyCloudConnector(t, ctx, bin, root, ready.URL, certFile, httpClient, hereClient, hereID, here)
}

// This is a real CLI connector process, not a provider startup simulation. It
// qualifies scoped authorization and encrypted read-only delivery independently
// of the hosted provider's install/start lifecycle.
func qualifyCloudConnector(t *testing.T, ctx context.Context, bin, root, origin, certFile string, httpClient *http.Client, client localruntime.Client, peer relay.PublicIdentity, desktop machineHome) {
	t.Helper()
	cloud := newMachineHome(t, root, "cloud-scoped", true)
	transcript := filepath.Join(cloud.home, ".claude", "projects", claude.Slug(cloud.repo), sid+".jsonl")
	native, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	run := func(input []byte, args ...string) []byte {
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env = cloud.env()
		cmd.Stdin = bytes.NewReader(input)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("cloud %s: %v\n%s", args[0], err, out)
		}
		return out
	}
	prepare := []string{"cloud-integration", "prepare", "--provider", "claude-hosted", "--session", sid, "--workspace", cloud.repo, "--native-root", filepath.Join(cloud.home, ".claude", "projects"), "--transcript", transcript, "--allow-transcript-export"}
	var instance cloudintegration.Incarnation
	if err = json.Unmarshal(run(nil, prepare...), &instance); err != nil {
		t.Fatal(err)
	}
	store := relay.Store{Directory: filepath.Join(desktop.home, "state", "relay")}
	ownerConnection, err := store.Connection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ownerTokenHash := sha256.Sum256([]byte(ownerConnection.Token))
	body, _ := json.Marshal(map[string]any{"device": instance.Public.ID, "kind": "cloud-session", "issuer": map[string]string{"device": peer.ID, "credential": hex.EncodeToString(ownerTokenHash[:])}, "ttl": int(time.Until(instance.Expires).Seconds())})
	req, err := http.NewRequestWithContext(ctx, "POST", origin+"/v1/enrollment/register", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer fixture-admin-secret-with-32-bytes-minimum")
	req.Header.Set("X-Hopsesh-Space", "relay-e2e-space-1234")
	res, err := httpClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatal("cloud enrollment", res.StatusCode)
	}
	var connection relay.Connection
	if err = json.NewDecoder(res.Body).Decode(&connection); err != nil {
		t.Fatal(err)
	}
	connection.CAFile = certFile
	credential, _ := json.Marshal(connection)
	public, _ := json.Marshal(peer)
	publicFile := filepath.Join(root, "desktop-public.json")
	if err = os.WriteFile(publicFile, public, 0600); err != nil {
		t.Fatal(err)
	}
	run(credential, "cloud-integration", "authorize", instance.Directory, "--peer", publicFile, "--fingerprint", peer.Fingerprint(), "--origin", origin)
	if err = store.Approve(ctx, relay.Grant{Peer: instance.Public, Endpoint: instance.Public.Endpoint, Kind: "cloud-session", SendMethods: []string{"observe", "export"}, Expires: connection.Expires}); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, bin, "cloud-integration", "serve", instance.Directory)
	cmd.Env = cloud.env()
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	call := func(method string, params any, result any) error {
		operation, err := relay.NewOperationID()
		if err != nil {
			return err
		}
		bounded, done := context.WithTimeout(ctx, 30*time.Second)
		defer done()
		return client.Call(bounded, "relay.call", map[string]any{"peer": instance.Public.ID, "operation": operation, "method": method, "params": params}, result)
	}
	var observed cloudintegration.Observation
	if err = call("observe", nil, &observed); err != nil {
		t.Fatal("scoped cloud observation", err)
	}
	if observed.Session != sid || observed.Incarnation != instance.ID || observed.Workspace != cloud.repo || !observed.TranscriptAvailable || !observed.ExportAllowed {
		t.Fatal("cloud observation widened or lost its scope", observed)
	}
	var exported cloudintegration.Export
	if err = call("export", nil, &exported); err != nil {
		t.Fatal("scoped cloud export", err)
	}
	sum := sha256.Sum256(native)
	if exported.Session != sid || exported.Format != "native-jsonl" || !bytes.Equal(exported.Data, native) || exported.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("cloud export did not preserve the bound native transcript")
	}
	if exported.Checkpoint.Kind != "complete-record-prefix" || exported.Checkpoint.OmittedTail != 0 || exported.Checkpoint.ExportedBytes != int64(len(native)) {
		t.Fatal("cloud checkpoint lacked fidelity metadata")
	}
	firstCheckpoint := bytes.Clone(exported.Data)
	partial := []byte(`{"type":"assistant","message":`)
	currentNative := append(bytes.Clone(native), partial...)
	if err = os.WriteFile(transcript, currentNative, 0600); err != nil {
		t.Fatal(err)
	}
	if err = call("export", nil, &exported); err != nil || !bytes.Equal(exported.Data, firstCheckpoint) || exported.Checkpoint.OmittedTail != int64(len(partial)) {
		t.Fatal("encrypted export included or hid the unfinished vendor write", err)
	}
	inspect := func(preview, success bool) []byte {
		t.Helper()
		args := []string{"cloud-integration", "inspect", instance.Public.ID}
		if preview {
			args = append(args, "--preview")
		}
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env = desktop.env()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if (err == nil) != success {
			t.Fatalf("native scoped cloud inspect: %v\n%s", err, stderr.String())
		}
		return out
	}
	var checked cloudintegration.Observation
	if err = json.Unmarshal(inspect(false, true), &checked); err != nil || !checked.ExportAllowed || checked.Session != sid {
		t.Fatal("native cloud inspector widened or lost scope", err)
	}
	var preview app.CloudConnectorPreview
	if err = json.Unmarshal(inspect(true, true), &preview); err != nil || preview.Preview.Messages() == 0 || preview.Checkpoint.OmittedTail != int64(len(partial)) {
		t.Fatal("native cloud preview lost conversation or fidelity metadata", err)
	}
	if err = store.Approve(ctx, relay.Grant{Peer: instance.Public, Endpoint: instance.Public.Endpoint, Kind: "cloud-session", SendMethods: []string{"observe"}, Expires: connection.Expires}); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(inspect(false, true), &checked); err != nil || checked.ExportAllowed {
		t.Fatal("observation-only approval exposed transcript permission", err)
	}
	inspect(true, false)
	if err = store.Approve(ctx, relay.Grant{Peer: instance.Public, Endpoint: instance.Public.Endpoint, Kind: "cloud-session", SendMethods: []string{"observe", "export"}, Expires: connection.Expires}); err != nil {
		t.Fatal(err)
	}
	// Real portable imports use the shared native planner, private local source
	// ledger and stable operation recovery. A sibling fork stays independent.
	for _, target := range []string{"claude", "codex"} {
		for _, fork := range []bool{false, true} {
			operation := fmt.Sprintf("cloud-checkpoint-%s-%t", target, fork)
			args := []string{"cloud-integration", "import", instance.Public.ID, "--to", desktop.repo, "--in", target, "--operation-id", operation, "--yes"}
			if fork {
				args = append(args, "--fork")
			}
			invoke := func(extra ...string) []byte {
				t.Helper()
				cmd := exec.CommandContext(ctx, bin, append(append([]string{}, args...), extra...)...)
				cmd.Env = desktop.env()
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				out, err := cmd.Output()
				if err != nil {
					t.Fatalf("checkpoint import: %v\n%s", err, stderr.String())
				}
				return out
			}
			var review struct {
				Plan       move.Plan                   `json:"plan"`
				Checkpoint cloudintegration.Checkpoint `json:"checkpoint"`
			}
			if err = json.Unmarshal(invoke("--dry-run"), &review); err != nil || !review.Plan.Options.NewReplica || !review.Plan.Options.OtherAccount || review.Checkpoint.OmittedTail != int64(len(partial)) {
				t.Fatal("checkpoint review lost portable or fidelity boundary", err)
			}
			var result struct {
				Plan   move.Plan   `json:"plan"`
				Result move.Result `json:"result"`
			}
			if err = json.Unmarshal(invoke(), &result); err != nil || result.Result.Journal == "" {
				t.Fatal("checkpoint destination was not durable", err)
			}
			if result.Plan.Placement.Key.Session == sid {
				t.Fatal("cloud import reused a protected native identity")
			}
			l := newLocation(t, desktop.name, root)
			var mod agent.Module = claude.New()
			install := l.in
			if target == "codex" {
				mod = codex.New()
				install = codexInstall(l)
			}
			var destination agent.Summary
			for _, s := range listAgent(t, l, mod, install) {
				if s.Key.Session == result.Plan.Placement.Key.Session {
					destination = s
				}
			}
			if destination.Path == "" {
				t.Fatal("imported native session not listed")
			}
			before, err := os.ReadFile(destination.Path)
			if err != nil {
				t.Fatal(err)
			}
			graph, err := lineage.Read(host.LocalFS(), destination.Path)
			if err != nil || graph == nil || graph.Journey().Fork != fork || graph.Journey().MachineTransfers != 0 {
				t.Fatal("checkpoint lineage misclassified cloud or fork", err)
			}
			var retry struct {
				Plan   move.Plan   `json:"plan"`
				Result move.Result `json:"result"`
			}
			if err = json.Unmarshal(invoke(), &retry); err != nil || retry.Plan.Placement.Key != result.Plan.Placement.Key || retry.Result.Journal != result.Result.Journal {
				t.Fatal("checkpoint retry allocated a new destination", err)
			}
			after, err := os.ReadFile(destination.Path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("checkpoint retry rewrote native history", err)
			}
			cmd := exec.CommandContext(ctx, bin, "undo", result.Result.Journal, "--yes", "--json")
			cmd.Env = desktop.env()
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("checkpoint undo: %v\n%s", err, out)
			}
			if _, err = os.Stat(destination.Path); !os.IsNotExist(err) {
				t.Fatal("checkpoint undo left its new native session", err)
			}
		}
	}
	if err = call("apply", nil, nil); err == nil {
		t.Fatal("cloud grant allowed device mutation")
	}
	if err = call("export", map[string]string{"session": "another-session", "path": cloud.home}, nil); err == nil {
		t.Fatal("cloud request widened the session")
	}
	// A fork has separate startup keys and cannot supersede the original.
	run(nil, "cloud-integration", "prepare", "--provider", "claude-hosted", "--session", "independent-fork", "--workspace", cloud.repo)
	if err = call("observe", nil, &observed); err != nil {
		t.Fatal("fork invalidated original connector", err)
	}
	// Restart/rebuild of the same session must invalidate the cached credential.
	var replacement cloudintegration.Incarnation
	if err = json.Unmarshal(run(nil, prepare...), &replacement); err != nil {
		t.Fatal(err)
	}
	if replacement.Public.ID == instance.Public.ID || replacement.Directory == instance.Directory {
		t.Fatal("rebuild reused cached cloud identity")
	}
	if _, err = cloudintegration.Load(ctx, instance.Directory); err == nil {
		t.Fatal("superseded cloud connector retained authority")
	}
	after, err := os.ReadFile(transcript)
	if err != nil || !bytes.Equal(after, currentNative) {
		t.Fatal("read-only cloud delivery changed the native transcript", err)
	}
	journals, err := filepath.Glob(filepath.Join(cloud.home, "state", "journal", "*", "journal.json"))
	if err != nil || len(journals) != 0 {
		t.Fatal("scoped cloud reads created mutation journals", journals, err)
	}
}

func relayFixtureCertificate(t *testing.T, root string) (string, string, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "disposable relay fixture"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	secret := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})
	certFile, keyFile := filepath.Join(root, "fixture-ca.pem"), filepath.Join(root, "fixture-key.pem")
	if err = os.WriteFile(certFile, cert, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(keyFile, secret, 0600); err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(cert)
	return certFile, keyFile, pool
}
