package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualWorkerHandlerAndGoEndpointsRoundTrip(t *testing.T) {
	fixture, err := filepath.Abs("../../../infrastructure/relay/server-fixture.mjs")
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("HOPSESH_RELAY_REQUIRE_NODE") == "1" {
			t.Fatal(err)
		}
		t.Skip("Node is needed for cross-language relay qualification")
	}
	if _, err = os.Stat(filepath.Join(filepath.Dir(fixture), "node_modules", "jose")); err != nil {
		if os.Getenv("HOPSESH_RELAY_REQUIRE_NODE") == "1" {
			t.Fatal("run npm ci in infrastructure/relay", err)
		}
		t.Skip("relay Worker dependencies are installed in the relay matrix job")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, fixture)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	cmd.Stderr = &logs
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	line, err := bufio.NewReader(stdout).ReadBytes('\n')
	if err != nil {
		t.Fatal("fixture did not start", err, logs.String())
	}
	var ready struct {
		URL string `json:"url"`
	}
	if err = json.Unmarshal(line, &ready); err != nil {
		t.Fatal(err)
	}
	a, b := identities(t)
	space := "wire-space-123456"
	enroll := func(id string) string {
		body, _ := json.Marshal(map[string]any{"device": id, "ttl": 3600})
		req, err := http.NewRequestWithContext(ctx, "POST", ready.URL+"/v1/enrollment/register", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer fixture-admin-secret-with-32-bytes-minimum")
		req.Header.Set("X-Hopsesh-Space", space)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		if r.StatusCode != 201 {
			t.Fatal("fixture enrollment refused", r.StatusCode)
		}
		var c Connection
		if err = json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&c); err != nil {
			t.Fatal(err)
		}
		return c.Token
	}
	tokens := []string{enroll(a.Public.ID), enroll(b.Public.ID)}
	var calls atomic.Int32
	makeService := func(i, other Identity, token string) *Service {
		store := Store{Directory: privateTemp(t)}
		if err = store.Approve(ctx, Grant{Peer: other.Public, Kind: "device", Methods: []string{"apply"}, SendMethods: []string{"apply"}}); err != nil {
			t.Fatal(err)
		}
		return &Service{Transport: Transport{Base: ready.URL, Token: token, Space: space, AllowLoopback: true}, Processor: Processor{Identity: i, Space: space, Store: store, Handle: func(_ context.Context, _ Grant, _, method string, p json.RawMessage) (any, error) {
			calls.Add(1)
			return map[string]any{"method": method, "received": p}, nil
		}}}
	}
	sa, sb := makeService(a, b, tokens[0]), makeService(b, a, tokens[1])
	ended := make(chan error, 2)
	go func() { ended <- sa.Run(ctx) }()
	go func() { ended <- sb.Run(ctx) }()
	body := json.RawMessage(`{"branch":"fork-A","work":"unique-marker"}`)
	for range 2 {
		result, err := sa.Call(ctx, b.Public.ID, "native-operation-1234", "apply", body)
		if err != nil || !bytes.Contains(result, []byte("unique-marker")) {
			t.Fatal("encrypted reply missing", err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate delivery repeated native action", calls.Load())
	}
	if _, err = sa.Call(ctx, b.Public.ID, "native-operation-1234", "apply", json.RawMessage(`{"branch":"original"}`)); err == nil {
		t.Fatal("operation changed branches")
	}
	cancel()
	for range 2 {
		<-ended
	}
}
