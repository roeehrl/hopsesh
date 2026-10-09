package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/observe"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	"github.com/roeehrl/hopsesh/internal/localstate"
)

// This opt-in workload uses the actual native listener, crypto, observation
// renewal and admission clients against staging. Server metrics must be collected
// separately for the Worker, both Durable Object classes and R2. Client counters
// exclude admission helpers that own their own HTTP clients.
// The 100 identities are logical clients in one process, not 100 physical hosts.
func TestRelayHostedSteadyLoadAndReconnect(t *testing.T) {
	if testing.Short() || os.Getenv("HOPSESH_HOSTED_RELAY") != "1" || os.Getenv("HOPSESH_HOSTED_LOAD") != "1" {
		t.Skip("explicit sustained hosted staging qualification")
	}
	var soak time.Duration
	if value := os.Getenv("HOPSESH_HOSTED_LOAD_DURATION"); value != "" {
		var err error
		soak, err = time.ParseDuration(value)
		if err != nil || soak < 20*time.Minute || soak > 2*time.Hour {
			t.Fatal("HOPSESH_HOSTED_LOAD_DURATION must be between 20m and 2h")
		}
	}
	admin, err := localstate.ReadPrivateFile(os.Getenv("HOPSESH_HOSTED_RELAY_ADMIN_FILE"), 256)
	if err != nil || len(admin) < 32 || strings.ContainsAny(string(admin), "\r\n") {
		t.Fatal("hosted qualification needs the private operator secret file")
	}
	budget := max(20*time.Minute, soak+15*time.Minute)
	ctx, cancel := context.WithTimeout(t.Context(), budget)
	defer cancel()
	const origin = "https://relay.hopsesh.codonic.dev"
	const clients = 100
	// Keep room for a fresh cloud incarnation under the real 32-device limit.
	const clientsPerSpace = 25
	started := time.Now().UTC()
	t.Log("whole-service workload starts", started.Format(time.RFC3339))
	transport := newHostedLoadTransport()
	defer transport.base.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(ctx context.Context, space, token, method, path string, body any, out any) (int, error) {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		req, err := http.NewRequestWithContext(ctx, method, origin+path, bytes.NewReader(data))
		if err != nil {
			return 0, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Hopsesh-Space", space)
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err != nil {
			return 0, errors.New("hosted workload HTTPS request failed")
		}
		defer response.Body.Close()
		if out != nil {
			err = json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(out)
		}
		return response.StatusCode, err
	}
	type endpoint struct {
		identity   relay.Identity
		connection relay.Connection
		service    *relay.Service
		to         relay.Grant
		received   atomic.Uint64
	}
	endpoints := make([]*endpoint, clients)
	spaces := make([]string, 4)
	for i := range spaces {
		spaces[i], err = relay.NewOperationID()
		if err != nil {
			t.Fatal(err)
		}
	}
	enroll := func(e *endpoint, space string) relay.Connection {
		t.Helper()
		var connection relay.Connection
		status, err := request(ctx, space, string(admin), "POST", "/v1/enrollment/register", map[string]any{"device": e.identity.Public.ID, "ttl": int((budget + 5*time.Minute) / time.Second)}, &connection)
		if err != nil || status != 201 || connection.Token == "" {
			t.Fatal("hosted workload enrollment failed", status, err)
		}
		connection.URL, connection.Space, connection.Device = origin, space, e.identity.Public.ID
		t.Cleanup(func() {
			cleanup, stop := context.WithTimeout(context.Background(), 20*time.Second)
			defer stop()
			status, err := request(cleanup, space, connection.Token, "POST", "/v1/enrollment/revoke", nil, nil)
			if err != nil || status != 200 && status != 403 {
				t.Error("hosted workload credential cleanup failed", status, err)
			}
		})
		return connection
	}
	for i := range endpoints {
		e := &endpoint{}
		e.identity, err = relay.GenerateIdentity(fmt.Sprintf("hosted-qualification-%03d", i))
		if err != nil {
			t.Fatal(err)
		}
		e.connection = enroll(e, spaces[i/clientsPerSpace])
		endpoints[i] = e
	}
	for i, e := range endpoints {
		start, size := i/clientsPerSpace*clientsPerSpace, min(clientsPerSpace, clients-i/clientsPerSpace*clientsPerSpace)
		to, from := endpoints[start+(i-start+1)%size], endpoints[start+(i-start+size-1)%size]
		store := relay.Store{Directory: filepath.Join(t.TempDir(), "relay")}
		e.to = relay.Grant{Peer: to.identity.Public, Kind: "device", Methods: []string{"observe"}}
		if err = store.Approve(ctx, e.to); err != nil {
			t.Fatal(err)
		}
		if err = store.Approve(ctx, relay.Grant{Peer: from.identity.Public, Kind: "device", SendMethods: []string{"observe"}}); err != nil {
			t.Fatal(err)
		}
		e.service = &relay.Service{Transport: relay.Transport{Base: origin, Space: e.connection.Space, Token: e.connection.Token, HTTP: client}, Processor: relay.Processor{Identity: e.identity, Space: e.connection.Space, Store: store}, OnObservation: func(_ context.Context, g relay.Grant, s observe.Snapshot) error {
			if g.Peer.ID != from.identity.Public.ID || s.Epoch != from.identity.Public.ID || string(s.Data) != `{"inventoryComplete":true}` {
				return errors.New("hosted source projection changed")
			}
			e.received.Store(s.Sequence)
			return nil
		}}
	}
	var stopListeners context.CancelFunc
	var listeners sync.WaitGroup
	stop := func() {
		if stopListeners != nil {
			stopListeners()
			listeners.Wait()
			stopListeners = nil
		}
	}
	defer stop()
	start := func() {
		var running context.Context
		running, stopListeners = context.WithCancel(ctx)
		for _, e := range endpoints {
			listeners.Go(func() { _ = e.service.Run(running) })
		}
	}
	wait := func(label string, check func() bool) {
		t.Helper()
		deadline := time.NewTimer(150 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatal(label, ctx.Err())
			case <-deadline.C:
				t.Fatal(label, "timed out")
			case <-tick.C:
			}
		}
	}
	connected := func(previous map[string]int) bool {
		for _, e := range endpoints {
			h := e.service.Health()
			if !h.Connected || h.DeliveryMode != "notifications" || !transport.upgradedSince(e.connection.Token, previous) {
				return false
			}
		}
		return true
	}
	publish := func(sequence uint64) {
		t.Helper()
		for _, e := range endpoints {
			now := time.Now().UTC()
			snapshot := observe.Snapshot{Epoch: e.identity.Public.ID, Sequence: sequence, AttemptedAt: now, ObservedAt: now, ExpiresAt: now.Add(relay.ObservationLease), Data: json.RawMessage(`{"inventoryComplete":true}`)}
			if err := e.service.PublishObservation(ctx, e.to, snapshot); err != nil {
				t.Fatal("hosted observation publication failed", sequence, err)
			}
		}
		wait("100 authenticated observations", func() bool {
			for _, e := range endpoints {
				if e.received.Load() != sequence {
					return false
				}
			}
			return true
		})
		t.Log("observation round", sequence, "received by all 100 clients", time.Now().UTC().Format(time.RFC3339))
	}
	steady := func() {
		t.Helper()
		timer := time.NewTimer(relay.ObservationRenew + 5*time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-timer.C:
		}
	}
	start()
	wait("initial notification connections", func() bool { return connected(nil) })
	publish(1)
	steady()
	publish(2)
	before := transport.upgradeSnapshot()
	transport.disconnect()
	wait("simultaneous connection recovery", func() bool { return connected(before) })
	publish(3)
	// Refresh routing credentials only while listeners are stopped. Old tokens
	// must fail and the same independently approved device keys must remain.
	stop()
	for _, index := range []int{0, 25, 50, 75} {
		t.Log("renewing native routing credential and claiming cloud invitation", index, time.Now().UTC().Format(time.RFC3339))
		e := endpoints[index]
		old := e.service.Transport
		e.connection = enroll(e, e.connection.Space)
		if _, err := old.Poll(ctx, 0); !errors.Is(err, relay.ErrAuthorizationRefused) {
			t.Fatal("old routing credential survived renewal", err)
		}
		e.service.Transport.Token = e.connection.Token
		// Include the Authorization object's real cloud ticket/claim/revoke
		// path in this workload, with synthetic identities and no transcript.
		store := relay.AdmissionStore{Directory: filepath.Join(t.TempDir(), "admission")}
		record, err := store.IssueTask(ctx, e.identity, e.connection, "codex-current", fmt.Sprintf("qualification-%d", index), 600*time.Second, "")
		if err != nil {
			t.Fatal("hosted workload cloud invitation failed", err)
		}
		connection := e.connection
		revoked := false
		t.Cleanup(func() {
			if revoked {
				return
			}
			cleanup, stop := context.WithTimeout(context.Background(), 20*time.Second)
			defer stop()
			if err := store.Revoke(cleanup, connection, record.ID); err != nil {
				t.Error("hosted workload cloud invitation cleanup failed", err)
			}
		})
		body, err := localstate.ReadPrivateFile(record.Path, 65536)
		if err != nil {
			t.Fatal(err)
		}
		var ticket relay.AdmissionTicket
		if err = json.Unmarshal(body, &ticket); err != nil {
			t.Fatal(err)
		}
		incarnation, err := relay.NewOperationID()
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := relay.GenerateIdentity("cloud/codex-current/" + incarnation)
		if err != nil {
			t.Fatal(err)
		}
		claim, err := relay.ClaimAdmission(ctx, ticket, e.identity.Public.ID, leaf, relay.CloudClaim{Provider: "codex-current", Session: ticket.Session, Incarnation: incarnation, Expires: time.Now().Add(9 * time.Minute).Unix()}, client)
		if err != nil {
			t.Fatal("hosted workload cloud claim failed", err)
		}
		if err = relay.RevokeAdmission(ctx, e.connection, ticket); err != nil {
			t.Fatal("hosted workload cloud revocation failed", err)
		}
		revoked = true
		if _, err = (relay.Transport{Base: origin, Space: claim.Space, Token: claim.Token, HTTP: client}).Poll(ctx, 0); !errors.Is(err, relay.ErrAuthorizationRefused) {
			t.Fatal("revoked cloud route survived", err)
		}
		t.Log("native renewal, cloud claim and revocation passed", index)
	}
	before = transport.upgradeSnapshot()
	start()
	wait("notification connections after credential renewal", func() bool { return connected(before) })
	steady()
	publish(4)
	if soak > 0 {
		sequence := uint64(4)
		for time.Since(started) < soak {
			steady()
			sequence++
			publish(sequence)
		}
		// Recover again after the longer-lived connections and journals have
		// accumulated normal source renewals, not only immediately after setup.
		before = transport.upgradeSnapshot()
		transport.disconnect()
		wait("aged notification connection recovery", func() bool { return connected(before) })
		publish(sequence + 1)
	}
	wait("ciphertext and deletion intent drain", func() bool {
		for _, space := range spaces {
			var counts struct{ Messages, CiphertextBytes, PendingDeletions, AdmittedFrames int }
			status, err := request(ctx, space, string(admin), "GET", "/v1/operator/stats", nil, &counts)
			if err != nil || status != 200 {
				t.Fatal("hosted workload counters unavailable", status, err)
			}
			if counts.Messages != 0 || counts.CiphertextBytes != 0 || counts.PendingDeletions != 0 {
				return false
			}
		}
		return true
	})
	stop()
	t.Logf("whole-service workload finished %s: clients=%d spaces=%d HTTP=%d WebSocket handshakes=%d rate-limited=%d; native observation, reconnect, token renewal, cloud admission/revocation and ciphertext drain passed", time.Now().UTC().Format(time.RFC3339), clients, len(spaces), transport.requests.Load(), transport.upgrades.Load(), transport.limited.Load())
	t.Log("metrics window begins", started.Format(time.RFC3339), "and ends after credential cleanup; collect Worker, both Durable Object classes and R2 metrics separately")
}

type hostedLoadTransport struct {
	base                        *http.Transport
	mu                          sync.Mutex
	connections                 map[*hostedLoadConn]struct{}
	successfulUpgrades          map[string]int
	requests, upgrades, limited atomic.Int64
}
type hostedLoadConn struct {
	net.Conn
	owner *hostedLoadTransport
}

func (c *hostedLoadConn) Close() error {
	c.owner.mu.Lock()
	delete(c.owner.connections, c)
	c.owner.mu.Unlock()
	return c.Conn.Close()
}
func newHostedLoadTransport() *hostedLoadTransport {
	r := &hostedLoadTransport{base: http.DefaultTransport.(*http.Transport).Clone(), connections: map[*hostedLoadConn]struct{}{}, successfulUpgrades: map[string]int{}}
	dial := r.base.DialContext
	r.base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		connection, err := dial(ctx, network, address)
		if err != nil {
			return nil, err
		}
		c := &hostedLoadConn{Conn: connection, owner: r}
		r.mu.Lock()
		r.connections[c] = struct{}{}
		r.mu.Unlock()
		return c, nil
	}
	return r
}
func (r *hostedLoadTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.requests.Add(1)
	if req.URL.Path == "/v1/notifications" {
		r.upgrades.Add(1)
	}
	response, err := r.base.RoundTrip(req)
	if response != nil && response.StatusCode == http.StatusSwitchingProtocols && req.URL.Path == "/v1/notifications" {
		r.mu.Lock()
		r.successfulUpgrades[strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")]++
		r.mu.Unlock()
	}
	if response != nil && response.StatusCode == 429 {
		r.limited.Add(1)
	}
	return response, err
}
func (r *hostedLoadTransport) upgradeSnapshot() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := make(map[string]int, len(r.successfulUpgrades))
	for key, count := range r.successfulUpgrades {
		copy[key] = count
	}
	return copy
}
func (r *hostedLoadTransport) upgradedSince(token string, previous map[string]int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.successfulUpgrades[token] > previous[token]
}
func (r *hostedLoadTransport) disconnect() {
	r.mu.Lock()
	all := make([]*hostedLoadConn, 0, len(r.connections))
	for c := range r.connections {
		all = append(all, c)
	}
	r.mu.Unlock()
	for _, c := range all {
		_ = c.Close()
	}
}
