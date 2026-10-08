package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	"github.com/roeehrl/hopsesh/internal/localstate"
)

// Stats reads do not perform mailbox maintenance. An expired, unacknowledged
// packet disappearing here therefore exercises deployed DO alarms and R2 delete
// intents, independently of the two-day orphan-object lifecycle policy.
func TestRelayHostedRetention(t *testing.T) {
	if testing.Short() || os.Getenv("HOPSESH_HOSTED_RELAY") != "1" {
		t.Skip("explicit hosted staging qualification")
	}
	admin, err := localstate.ReadPrivateFile(os.Getenv("HOPSESH_HOSTED_RELAY_ADMIN_FILE"), 256)
	if err != nil || len(admin) < 32 || strings.ContainsAny(string(admin), "\r\n") {
		t.Fatal("hosted qualification requires a bounded private operator secret file")
	}
	const origin = "https://relay.hopsesh.codonic.dev"
	space, err := relay.NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(ctx context.Context, path, method, token string, value any) (int, []byte) {
		t.Helper()
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		r, err := http.NewRequestWithContext(ctx, method, origin+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Hopsesh-Space", space)
		r.Header.Set("Content-Type", "application/json")
		res, err := client.Do(r)
		if err != nil {
			t.Fatal("hosted qualification request failed")
		}
		defer res.Body.Close()
		out, err := io.ReadAll(io.LimitReader(res.Body, 8193))
		if err != nil || len(out) > 8192 {
			t.Fatal("invalid hosted response")
		}
		return res.StatusCode, out
	}
	enroll := func(ttl int) (relay.Identity, string) {
		t.Helper()
		id, err := relay.GenerateIdentity()
		if err != nil {
			t.Fatal(err)
		}
		status, body := request(ctx, "/v1/enrollment/register", "POST", string(admin), map[string]any{"device": id.Public.ID, "ttl": ttl})
		var c relay.Connection
		if status != 201 || json.Unmarshal(body, &c) != nil || c.Token == "" {
			t.Fatal("hosted enrollment refused", status)
		}
		t.Cleanup(func() {
			cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
			defer done()
			status, _ := request(cleanup, "/v1/enrollment/revoke", "POST", c.Token, nil)
			if status != 200 && status != 403 { // Already expired or explicitly revoked.
				t.Error("hosted credential cleanup failed", status)
			}
		})
		return id, c.Token
	}
	subscribe := func(token string) *websocket.Conn {
		t.Helper()
		streamClient := *client
		streamClient.Timeout = 0
		headers := http.Header{"Authorization": {"Bearer " + token}, "X-Hopsesh-Space": {space}}
		handshake, done := context.WithTimeout(ctx, 10*time.Second)
		defer done()
		socket, _, err := websocket.Dial(handshake, origin+"/v1/notifications", &websocket.DialOptions{HTTPClient: &streamClient, HTTPHeader: headers})
		if err != nil {
			t.Fatal("hosted notification handshake failed")
		}
		socket.SetReadLimit(256)
		t.Cleanup(func() { _ = socket.CloseNow() })
		return socket
	}
	readHint := func(socket *websocket.Conn) {
		t.Helper()
		read, done := context.WithTimeout(ctx, 10*time.Second)
		defer done()
		kind, body, err := socket.Read(read)
		if err != nil || kind != websocket.MessageText || string(body) != `{"type":"mailbox-changed"}` {
			t.Fatal("hosted notification did not deliver its bounded wake hint")
		}
	}
	a, ta := enroll(180)
	b, tb := enroll(180)
	_, expiring := enroll(60)
	expirySocket := subscribe(expiring)
	readHint(expirySocket)
	socket := subscribe(tb)
	readHint(socket)
	e, err := relay.Seal(a, b.Public, space, space, []byte("disposable hosted retention fixture"), time.Now(), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	transport := relay.Transport{Base: origin, Token: ta, Space: space, HTTP: client}
	if err = transport.Submit(ctx, e); err != nil {
		t.Fatal(err)
	}
	readHint(socket)
	stats := func() map[string]int64 {
		t.Helper()
		status, body := request(ctx, "/v1/operator/stats", "GET", string(admin), nil)
		var raw map[string]json.RawMessage
		if status != 200 || json.Unmarshal(body, &raw) != nil {
			t.Fatal("hosted operator stats failed", status)
		}
		out := make(map[string]int64)
		for _, name := range []string{"messages", "ciphertextBytes", "pendingDeletions", "tombstones", "admittedFrames"} {
			var v int64
			if json.Unmarshal(raw[name], &v) != nil {
				t.Fatal("hosted counter missing", name)
			}
			out[name] = v
		}
		return out
	}
	if s := stats(); s["messages"] != 1 || s["ciphertextBytes"] == 0 || s["tombstones"] != 1 {
		t.Fatal("hosted message was not retained before expiry", s)
	}
	deadline := time.Now().Add(45 * time.Second)
	for {
		s := stats()
		if s["messages"] == 0 && s["ciphertextBytes"] == 0 && s["pendingDeletions"] == 0 && s["tombstones"] == 0 {
			if s["admittedFrames"] != 1 {
				t.Fatal("expiry refunded the daily traffic budget")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("deployed alarm did not finish message and deletion-intent expiry", s)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
	t.Log("unacknowledged ciphertext and replay tombstone expired through deployed alarms; deletion intent drained")
	status, _ := request(ctx, "/v1/enrollment/revoke", "POST", tb, nil)
	if status != 200 {
		t.Fatal("self revocation failed", status)
	}
	assertClosed := func(socket *websocket.Conn, timeout time.Duration) {
		t.Helper()
		read, done := context.WithTimeout(ctx, timeout)
		defer done()
		_, _, err := socket.Read(read)
		if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
			t.Fatal("ended authorization did not close notification stream with policy violation")
		}
	}
	assertClosed(socket, 10*time.Second)
	if status, _ = request(ctx, "/v1/messages", "GET", tb, nil); status != 403 {
		t.Fatal("revoked credential retained HTTP access", status)
	}
	assertClosed(expirySocket, 75*time.Second)
	if status, _ = request(ctx, "/v1/messages", "GET", expiring, nil); status != 403 {
		t.Fatal("expired credential retained HTTP access", status)
	}
	t.Log("explicit revocation and lease expiry closed existing WebSockets and refused subsequent HTTP requests")
}

// Explicit opt-in only: this runs real native endpoints against the isolated
// deployed service. The operator secret stays in a bounded private file and is
// sent only to the fixed HTTPS enrollment route, with redirects refused.
func TestRelayHostedStaging(t *testing.T) {
	if testing.Short() || os.Getenv("HOPSESH_HOSTED_RELAY") != "1" {
		t.Skip("explicit hosted staging qualification")
	}
	admin, err := localstate.ReadPrivateFile(os.Getenv("HOPSESH_HOSTED_RELAY_ADMIN_FILE"), 256)
	if err != nil || len(admin) < 32 || strings.ContainsAny(string(admin), "\r\n") {
		t.Fatal("hosted qualification requires a bounded private operator secret file")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	f := newRelayFleetWithAdmission(t, ctx, buildHopsesh(t), "https://relay.hopsesh.codonic.dev", "", client, string(admin), true)
	current, sourceAgent := f.seed(t, "claude"), "claude"
	route := "ABCA"
	var sentinels []string
	for hop := 0; hop < len(route)-1; hop++ {
		from, to := route[hop], route[hop+1]
		sentinel := fmt.Sprintf("HOSTED-DISPOSABLE-WORK-%d", hop)
		sentinels = append(sentinels, sentinel)
		f.appendWork(t, current, sourceAgent, sentinel)
		target, mode := "codex", "push"
		if hop%2 == 1 {
			target, mode = "claude", "pull"
		}
		if to == 'A' {
			target = "claude" // Return to the original machine and agent.
		}
		operation := fmt.Sprintf("hosted-route-hop-%d-12345678", hop)
		previous := current
		current = f.transfer(t, from, to, current, target, mode, false, operation)
		f.assertText(t, to, current, target, sentinels, nil)
		if hop == 1 {
			before, err := os.ReadFile(current.Path)
			if err != nil {
				t.Fatal(err)
			}
			f.stop[to]()
			f.start(t, to)
			repeated := f.transfer(t, from, to, previous, target, mode, false, operation)
			after, err := os.ReadFile(repeated.Path)
			if err != nil || !bytes.Equal(before, after) || repeated.Key != current.Key {
				t.Fatal("hosted owner restart retry duplicated the native outcome", err)
			}
		}
		sourceAgent = target
	}
	graph, err := lineage.Read(host.LocalFS(), current.Path)
	if err != nil || graph == nil {
		t.Fatal("hosted route lost lineage", err)
	}
	if got := graph.Journey(); got.Transfers != 3 || got.RoundTrips != 1 {
		t.Fatal("hosted route lost transfer or round-trip count", got)
	}
	original := current
	child := f.transfer(t, 'A', 'B', original, "claude", "push", true, "hosted-independent-fork-12345678")
	f.appendWork(t, child, "claude", "HOSTED-FORK-ONLY")
	child = f.transfer(t, 'B', 'C', child, "codex", "pull", false, "hosted-fork-travel-123456789")
	f.assertText(t, 'C', child, "codex", append(sentinels, "HOSTED-FORK-ONLY"), []string{"HOSTED-ORIGINAL-ONLY"})
	f.appendWork(t, original, sourceAgent, "HOSTED-ORIGINAL-ONLY")
	moved := f.transfer(t, 'A', 'C', original, "claude", "push", false, "hosted-original-travel-12345678")
	f.assertText(t, 'C', moved, "claude", append(sentinels, "HOSTED-ORIGINAL-ONLY"), []string{"HOSTED-FORK-ONLY"})
	if f.lastResult.Journal == "" {
		t.Fatal("hosted native transfer has no undo journal")
	}
	f.run(t, f.homes['A'], "undo", f.lastResult.Journal, "--yes", "--json")
	if _, err = os.Stat(moved.Path); !os.IsNotExist(err) {
		t.Fatal("hosted peer undo left the newly written native session", err)
	}
	f.assertText(t, 'C', child, "codex", []string{"HOSTED-FORK-ONLY"}, []string{"HOSTED-ORIGINAL-ONLY"})
}
