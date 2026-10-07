package e2e

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
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
	"github.com/roeehrl/hopsesh/internal/core/relay"
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
