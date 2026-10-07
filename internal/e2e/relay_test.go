package e2e

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	localruntime "github.com/roeehrl/hopsesh/internal/core/runtime"
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
	stdout, err := server.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	server.Stderr = os.Stderr
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = server.Wait() }()
	line, err := bufio.NewReader(stdout).ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var ready struct {
		URL string `json:"url"`
	}
	if err = json.Unmarshal(line, &ready); err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}, Timeout: 10 * time.Second}
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
		if err = store.Approve(ctx, relay.Grant{Peer: p.remote, Endpoint: p.remote.Endpoint, Kind: "device", Roots: []string{p.local.repo}, Methods: []string{"hello", "observe", "plan", "apply", "undo"}, SendMethods: []string{"hello", "observe", "plan", "apply", "undo"}}); err != nil {
			t.Fatal(err)
		}
	}
	hereCfg := config.Defaults()
	hereCfg.Relay.Enabled = true
	hereCfg.Peer.Receive = true
	hereCfg.ReposDir = filepath.Join(here.home, "git")
	hereCfg.Hosts = []config.Host{{Name: box.name, RelayID: boxID.ID, Allowed: true, Via: "relay"}}
	here.writeConfig(t, hereCfg)
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
				break
			}
			if time.Now().After(deadline) {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				t.Fatalf("runtime failed: %v\n%s", err, output.String())
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
	_, _ = start(here)
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
	run(here, "undo", result.Result.Journal, "--yes", "--json")
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
