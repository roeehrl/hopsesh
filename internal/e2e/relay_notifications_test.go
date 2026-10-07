package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/roeehrl/hopsesh/internal/core/relay"
)

// This runs inside the real SQLite Durable Object/R2 fixture, not a substitute
// HTTP or WebSocket server. Credentials and identities are disposable.
func qualifyRelayNotifications(t *testing.T, ctx context.Context, origin string, client *http.Client) {
	t.Helper()
	const space = "platform-notification-space-1234"
	request := func(path, token string, body any) *http.Response {
		t.Helper()
		data, _ := json.Marshal(body)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Hopsesh-Space", space)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = res.Body.Close() })
		return res
	}
	enroll := func(device string) relay.Connection {
		t.Helper()
		res := request("/v1/enrollment/register", "fixture-admin-secret-with-32-bytes-minimum", map[string]any{"device": device, "ttl": 3600})
		if res.StatusCode != 201 {
			t.Fatal("notification fixture enrollment", res.StatusCode)
		}
		var c relay.Connection
		if err := json.NewDecoder(res.Body).Decode(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	a, b := enroll("notification-endpoint-A-123"), enroll("notification-endpoint-B-123")
	socketClient := *client
	socketClient.Timeout = 0
	connect := func(token string) (*websocket.Conn, *http.Response, error) {
		headers := http.Header{"Authorization": []string{"Bearer " + token}, "X-Hopsesh-Space": []string{space}}
		return websocket.Dial(ctx, origin+"/v1/notifications", &websocket.DialOptions{HTTPClient: &socketClient, HTTPHeader: headers})
	}
	if socket, res, err := connect("forged"); err == nil {
		_ = socket.CloseNow()
		t.Fatal("forged subscription accepted")
	} else if res == nil || res.StatusCode != 403 {
		t.Fatal("forged subscription disposition")
	}
	socket, res, err := connect(b.Token)
	if err != nil {
		t.Fatalf("actual DO notification handshake: %v, response %v", err, res)
	}
	defer socket.CloseNow()
	read := func(s *websocket.Conn) error {
		t.Helper()
		readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		kind, body, err := s.Read(readCtx)
		if err != nil {
			return err
		}
		if kind != websocket.MessageText || string(body) != `{"type":"mailbox-changed"}` {
			t.Fatal("notification carried content or an unexpected frame")
		}
		return nil
	}
	if err = read(socket); err != nil {
		t.Fatal("initial reconciliation hint", err)
	}
	envelope := relay.Envelope{Protocol: 1, Kind: "request", ID: "notification-message-1234", Space: space, From: a.Device, To: b.Device, Operation: "notification-operation-1234", Created: time.Now().Unix(), Expires: time.Now().Add(time.Minute).Unix(), Ciphertext: []byte("encrypted fixture content"), Signature: make([]byte, 64)}
	if res = request("/v1/messages", a.Token, envelope); res.StatusCode != 201 {
		t.Fatal("encrypted mailbox publication", res.StatusCode)
	}
	if err = read(socket); err != nil {
		t.Fatal("committed publication did not notify", err)
	}
	// Bound paid server connections independently of the native owner's single
	// stream. Existing sockets do not bypass that bound.
	var extra []*websocket.Conn
	for range 3 {
		s, _, err := connect(b.Token)
		if err != nil {
			t.Fatal(err)
		}
		extra = append(extra, s)
		defer s.CloseNow()
		if err = read(s); err != nil {
			t.Fatal(err)
		}
	}
	if s, r, e := connect(b.Token); e == nil {
		_ = s.CloseNow()
		t.Fatal("per-device connection quota bypassed")
	} else if r == nil || r.StatusCode != 429 {
		t.Fatal("socket quota disposition")
	}
	// Renewal must close all old routing sockets. It must not leave a hibernated
	// connection authorized by an earlier token hash.
	renewed := enroll(b.Device)
	if err = read(socket); err == nil {
		t.Fatal("old stream remained open after routing renewal")
	}
	for _, s := range extra {
		if err = read(s); err == nil {
			t.Fatal("old sibling stream survived renewal")
		}
	}
	fresh, _, err := connect(renewed.Token)
	if err != nil {
		t.Fatal("renewed stream", err)
	}
	defer fresh.CloseNow()
	if err = read(fresh); err != nil {
		t.Fatal(err)
	}
	if res = request("/v1/enrollment/revoke", renewed.Token, map[string]any{}); res.StatusCode != 200 {
		t.Fatal("revoke", res.StatusCode)
	}
	if err = read(fresh); err == nil {
		t.Fatal("revoked stream remained open")
	}
	if s, r, e := connect(renewed.Token); e == nil {
		_ = s.CloseNow()
		t.Fatal("revoked routing token resubscribed")
	} else if r == nil || r.StatusCode != 403 {
		t.Fatal("revoked subscription disposition")
	}
}
