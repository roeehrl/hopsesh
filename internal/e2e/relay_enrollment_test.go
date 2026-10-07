package e2e

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/cloudintegration"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	"github.com/roeehrl/hopsesh/internal/localstate"
)

// Real Go/CLI OAuth clients over verified HTTPS to the actual authorization and
// mailbox handlers. The fixture browser stands in for Access; signature/issuer/
// audience verification is qualified separately in the Worker contracts.
func TestRelayEnrollmentCLIAndBrowserPKCE(t *testing.T) {
	if testing.Short() {
		t.Skip("builds CLI and starts Node authorization handler")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("HOPSESH_RELAY_REQUIRE_NODE") == "1" {
			t.Fatal(err)
		}
		t.Skip("Node relay fixture unavailable")
	}
	fixture, _ := filepath.Abs("../../infrastructure/relay/server-fixture.mjs")
	if _, err = os.Stat(filepath.Join(filepath.Dir(fixture), "node_modules", "jose")); err != nil {
		if os.Getenv("HOPSESH_RELAY_REQUIRE_NODE") == "1" {
			t.Fatal("relay npm ci is required")
		}
		t.Skip("relay dependencies unavailable")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	root := t.TempDir()
	cert, key, pool := relayFixtureCertificate(t, root)
	server := exec.CommandContext(ctx, node, fixture)
	server.Env = append(os.Environ(), "HOPSESH_RELAY_FIXTURE_CERT="+cert, "HOPSESH_RELAY_FIXTURE_KEY="+key)
	out, err := server.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	server.Stderr = os.Stderr
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = server.Wait() }()
	var ready struct {
		URL string `json:"url"`
	}
	if err = json.NewDecoder(out).Decode(&ready); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}, Timeout: 10 * time.Second}
	approve := func(code string) map[string]any {
		t.Helper()
		call := func(path string, form url.Values) map[string]any {
			t.Helper()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, ready.URL+path, strings.NewReader(form.Encode()))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Authorization", "Bearer fixture-browser-secret")
			req.Header.Set("Origin", ready.URL)
			req.Header.Set("X-Hopsesh-CSRF", "review")
			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			var data map[string]any
			if err = json.NewDecoder(res.Body).Decode(&data); err != nil || res.StatusCode != 200 {
				t.Fatalf("browser approval %s: HTTP %d, %v", path, res.StatusCode, err)
			}
			return data
		}
		review := call("/v1/device/review", url.Values{"user_code": {code}})
		return call("/v1/device/approve", url.Values{"user_code": {code}, "nonce": {review["nonce"].(string)}, "decision": {"approve"}})
	}
	bin := buildHopsesh(t)
	box := newMachineHome(t, root, "headless-login", false)
	box.writeConfig(t, config.Defaults())
	init := exec.CommandContext(ctx, bin, "relay", "init")
	init.Env = box.env()
	publicJSON, err := init.Output()
	if err != nil {
		t.Fatal(err)
	}
	var public relay.PublicIdentity
	if err = json.Unmarshal(publicJSON, &public); err != nil {
		t.Fatal(err)
	}
	login := exec.CommandContext(ctx, bin, "relay", "login", "--origin", ready.URL, "--ca-file", cert)
	login.Env = box.env()
	stdout, err := login.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	login.Stderr = &stderr
	if err = login.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = login.Process.Kill()
			_ = login.Wait()
		}
	}()
	reader := bufio.NewReader(stdout)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	code := regexp.MustCompile(`enter ([A-F0-9]{5}-[A-F0-9]{5})`).FindStringSubmatch(line)
	if len(code) != 2 {
		t.Fatal("CLI did not display an approval code")
	}
	line, err = reader.ReadString('\n')
	if err != nil || !strings.Contains(line, public.ID) {
		t.Fatal("CLI did not display its independent fingerprint")
	}
	approve(code[1])
	rest, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	err = login.Wait()
	waited = true
	if err != nil || !strings.Contains(string(rest), "Relay delivery approved") || strings.Contains(string(rest), `"token"`) {
		t.Fatalf("CLI login failed: %v %s", err, stderr.String())
	}
	store := relay.Store{Directory: filepath.Join(box.home, "state", "relay")}
	connection, err := store.Connection(ctx)
	if err != nil || connection.Device != public.ID || connection.CAFile != cert {
		t.Fatal("CLI routing credential not saved safely", err)
	}
	grants, err := store.Grants()
	if err != nil || len(grants) != 0 {
		t.Fatal("routing login granted native peer permissions", err)
	}
	if _, err = (relay.Transport{Base: ready.URL, Space: connection.Space, Token: connection.Token, HTTP: client}).Poll(ctx, 0); err != nil {
		t.Fatal("issued credential cannot access its own mailbox", err)
	}
	qualifyCloudAdmission(t, ctx, bin, root, ready.URL, cert, client, box, public)
	id, err := relay.GenerateIdentity("browser-native-machine")
	if err != nil {
		t.Fatal(err)
	}
	connection, err = (relay.Enrollment{Origin: ready.URL, HTTP: client}).Browser(ctx, id, func(uri string) error {
		u, err := url.Parse(uri)
		if err != nil {
			return err
		}
		approved := approve(u.Query().Get("user_code"))
		res, err := client.Get(approved["redirect_uri"].(string))
		if err != nil {
			return err
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatal("loopback approval callback rejected")
		}
		return nil
	})
	if err != nil || connection.Device != id.Public.ID {
		t.Fatal("real handler PKCE exchange failed", err)
	}
	if _, err = (relay.Transport{Base: ready.URL, Space: connection.Space, Token: connection.Token, HTTP: client}).Poll(ctx, 0); err != nil {
		t.Fatal("browser credential cannot access its own mailbox", err)
	}
}

func TestCloudAdmissionSQLiteR2(t *testing.T) {
	if testing.Short() || os.Getenv("HOPSESH_RELAY_PLATFORM") != "1" {
		t.Skip("qualified in the actual SQLite/R2 relay job")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../infrastructure/relay")
	root := t.TempDir()
	cert, key, pool := relayFixtureCertificate(t, root)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	server := exec.CommandContext(ctx, node, filepath.Join(fixture, "node_modules", "wrangler", "wrangler-dist", "cli.js"), "dev", "--local", "--ip", "127.0.0.1", "--port", fmt.Sprint(port), "--inspector-port", "0", "--local-protocol", "https", "--local-upstream", fmt.Sprintf("127.0.0.1:%d", port), "--https-key-path", key, "--https-cert-path", cert, "--persist-to", filepath.Join(root, "platform-state"), "--var", "ENROLLMENT_ADMIN:fixture-admin-secret-with-32-bytes-minimum", "--log-level", "error", "--show-interactive-dev-session=false")
	prepareRelayFixture(server)
	server.Dir = fixture
	server.Env = append(os.Environ(), "WRANGLER_SEND_METRICS=false")
	var logs bytes.Buffer
	server.Stdout = &logs
	server.Stderr = &logs
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Process.Signal(os.Interrupt); cancel(); _ = server.Wait() }()
	origin := fmt.Sprintf("https://127.0.0.1:%d", port)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}, Timeout: 5 * time.Second}
	deadline := time.Now().Add(20 * time.Second)
	for {
		res, err := client.Get(origin + "/v1/capabilities")
		if err == nil {
			_ = res.Body.Close()
			if res.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("local platform not ready", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	bin := buildHopsesh(t)
	native := newMachineHome(t, root, "platform-admission-native", false)
	native.writeConfig(t, config.Defaults())
	init := exec.CommandContext(ctx, bin, "relay", "init")
	init.Env = native.env()
	body, err := init.Output()
	if err != nil {
		t.Fatal(err)
	}
	var owner relay.PublicIdentity
	if err = json.Unmarshal(body, &owner); err != nil {
		t.Fatal(err)
	}
	body, _ = json.Marshal(map[string]any{"device": owner.ID, "ttl": 3600})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/v1/enrollment/register", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer fixture-admin-secret-with-32-bytes-minimum")
	req.Header.Set("X-Hopsesh-Space", "platform-admission-space-1234")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 201 {
		t.Fatal("native enrollment failed", res.StatusCode)
	}
	var c relay.Connection
	if err = json.NewDecoder(res.Body).Decode(&c); err != nil {
		t.Fatal(err)
	}
	c.URL, c.CAFile = origin, cert
	if err = (relay.Store{Directory: filepath.Join(native.home, "state", "relay")}).SetConnection(ctx, c); err != nil {
		t.Fatal(err)
	}
	qualifyCloudAdmission(t, ctx, bin, root, origin, cert, client, native, owner)
}

// The actual native/CLI clients cross the outer Worker's JWT role boundary,
// shared Authorization object and Mailbox, using real age and signing keys.
func qualifyCloudAdmission(t *testing.T, ctx context.Context, bin, root, origin, cert string, client *http.Client, native machineHome, owner relay.PublicIdentity) {
	t.Helper()
	cloud := newMachineHome(t, root, "cloud-admission", false)
	cloud.writeConfig(t, config.Defaults())
	run := func(box machineHome, stdin []byte, succeeds bool, args ...string) []byte {
		t.Helper()
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env, cmd.Stdin = box.env(), bytes.NewReader(stdin)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if (err == nil) != succeeds {
			t.Fatalf("cloud admission command %s: success=%v error=%v %s", args[1], succeeds, err, stderr.String())
		}
		if bytes.Contains(out, []byte(`"token"`)) || bytes.Contains(out, []byte(`"ticket"`)) {
			t.Fatal("admission command exposed secret on stdout")
		}
		return out
	}
	issue := func(session string) ([]byte, string) {
		t.Helper()
		out := run(native, nil, true, "cloud-integration", "ticket", "--provider", "codex-current", "--session", session, "--lease", "10m")
		lines := strings.Split(string(out), "\n")
		path := strings.TrimPrefix(lines[0], "Private invitation saved at ")
		body, err := localstate.ReadPrivateFile(path, 8192)
		if err != nil {
			t.Fatal("private admission not saved", err)
		}
		var invitation relay.AdmissionTicket
		if json.Unmarshal(body, &invitation) != nil || invitation.Verify(owner.ID) != nil || invitation.Origin != origin || bytes.Contains(out, []byte(invitation.Ticket)) {
			t.Fatal("invitation owner/scope/privacy mismatch")
		}
		return body, path
	}
	prepare := func(session string) cloudintegration.Incarnation {
		t.Helper()
		out := run(cloud, nil, true, "cloud-integration", "prepare", "--provider", "codex-current", "--session", session, "--workspace", cloud.repo)
		var instance cloudintegration.Incarnation
		if err := json.Unmarshal(out, &instance); err != nil {
			t.Fatal(err)
		}
		return instance
	}
	claim := func(instance cloudintegration.Incarnation, ticket []byte, succeeds bool) {
		t.Helper()
		run(cloud, ticket, succeeds, "cloud-integration", "claim", instance.Directory, "--fingerprint", owner.ID, "--ca-file", cert)
	}
	ticket, path := issue("original-cloud-session")
	nativeStore := relay.Store{Directory: filepath.Join(native.home, "state", "relay")}
	nativeConnection, err := nativeStore.Connection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	admissions := relay.AdmissionStore{Directory: filepath.Join(native.home, "state", "cloud-admissions")}
	localID := strings.TrimSuffix(filepath.Base(path), ".json")
	state, err := admissions.Check(ctx, nativeConnection, localID)
	if err != nil || state.Status != "pending" || state.Public != nil {
		t.Fatal("unclaimed invitation incorrectly reported an endpoint", err)
	}
	original := prepare("original-cloud-session")
	claim(original, ticket, true)
	store := relay.Store{Directory: original.Directory}
	connection, err := store.Connection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	claim(original, ticket, true)
	second, err := store.Connection(ctx)
	if err != nil || second.Token != connection.Token || second.Expires != connection.Expires {
		t.Fatal("claim retry rotated delivery credential", err)
	}
	grants, err := store.Grants()
	if err != nil || len(grants) != 1 || grants[0].Kind != "cloud-session" || !grants[0].Allows("observe", time.Now()) || grants[0].Allows("export", time.Now()) || grants[0].Allows("apply", time.Now()) {
		t.Fatal("claim granted unintended native or transcript permission", err)
	}
	state, err = admissions.Check(ctx, nativeConnection, localID)
	if err != nil || state.Status != "claimed" || state.Public == nil || state.Public.ID != original.Public.ID || state.LeaseExpires != connection.Expires {
		t.Fatal("claim status not bound to actual fresh cloud identity", err)
	}
	if grants, err = nativeStore.Grants(); err != nil {
		t.Fatal(err)
	}
	for _, grant := range grants {
		if grant.Kind == "cloud-session" {
			t.Fatal("provisional routing approved a cloud peer on desktop")
		}
	}
	if _, err = (relay.Transport{Base: origin, Space: connection.Space, Token: connection.Token, HTTP: client}).Poll(ctx, 0); err != nil {
		t.Fatal("claimed connection cannot poll own mailbox", err)
	}
	forkTicket, _ := issue("independent-cloud-fork")
	fork := prepare("independent-cloud-fork")
	claim(fork, ticket, false)
	claim(fork, forkTicket, true)
	if _, err = cloudintegration.Load(ctx, original.Directory); err != nil {
		t.Fatal("fork superseded its original", err)
	}
	rebuilt := prepare("original-cloud-session")
	if _, err = cloudintegration.Load(ctx, original.Directory); err == nil {
		t.Fatal("resume retained old local scope")
	}
	claim(rebuilt, ticket, false)
	newTicket, _ := issue("original-cloud-session")
	claim(rebuilt, newTicket, true)
	for range 2 {
		run(native, nil, true, "cloud-integration", "revoke-ticket", path)
	}
	state, err = admissions.Check(ctx, nativeConnection, localID)
	if err != nil || state.Status != "revoked" {
		t.Fatal("server revocation status not confirmed", err)
	}
	rows, err := admissions.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == localID && !row.Revoked {
			t.Fatal("CLI revocation not reflected in GUI metadata")
		}
	}
	out := run(native, nil, true, "cloud-integration", "status-ticket", path)
	if err = json.Unmarshal(out, &state); err != nil || state.Status != "revoked" {
		t.Fatal("CLI claim status did not confirm revocation", err)
	}
	if _, err = (relay.Transport{Base: origin, Space: connection.Space, Token: connection.Token, HTTP: client}).Poll(ctx, 0); err == nil {
		t.Fatal("revoked cloud credential remained usable")
	}
	forkConnection, err := (relay.Store{Directory: fork.Directory}).Connection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (relay.Transport{Base: origin, Space: forkConnection.Space, Token: forkConnection.Token, HTTP: client}).Poll(ctx, 0); err != nil {
		t.Fatal("revoking original revoked independent fork", err)
	}
}
