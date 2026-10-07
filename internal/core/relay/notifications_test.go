package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestNotificationListenerSleepsWakesAndJoins(t *testing.T) {
	var polls atomic.Int32
	socketReady := make(chan *websocket.Conn, 1)
	peerEnded := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-test-credential" || r.Header.Get("X-Hopsesh-Space") != "test-space-123456" {
			w.WriteHeader(403)
			return
		}
		if r.URL.Path == "/v1/notifications" {
			if r.URL.RawQuery != "" {
				t.Error("credential/query in notification URL")
			}
			socket, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer socket.CloseNow()
			socketReady <- socket
			_, _, _ = socket.Read(r.Context())
			close(peerEnded)
			return
		}
		polls.Add(1)
		_ = json.NewEncoder(w).Encode(Batch{})
	}))
	defer server.Close()
	transport := Transport{Base: server.URL, Space: "test-space-123456", Token: "private-test-credential", AllowLoopback: true}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	modes := make(chan string, 8)
	done := make(chan error, 1)
	go func() {
		done <- (Listener{Transport: transport, OnDeliveryMode: func(mode string) { modes <- mode }}).Run(ctx)
	}()
	socket := <-socketReady
	select {
	case mode := <-modes:
		if mode != "notifications" {
			t.Fatal(mode)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream not established")
	}
	time.Sleep(100 * time.Millisecond)
	before := polls.Load()
	time.Sleep(2300 * time.Millisecond)
	if polls.Load() != before {
		t.Fatal("healthy idle stream kept polling", before, polls.Load())
	}
	if err := socket.Write(ctx, websocket.MessageText, []byte(`{"type":"mailbox-changed"}`)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for polls.Load() == before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if polls.Load() == before {
		t.Fatal("committed mailbox hint did not wake receiver")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("listener did not join stream")
	}
	select {
	case <-peerEnded:
	case <-time.After(time.Second):
		t.Fatal("notification socket survived listener exit")
	}
}

func TestNotificationHandshakeRefusesRedirectAndSanitizesErrors(t *testing.T) {
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect) }))
	defer server.Close()
	transport := Transport{Base: server.URL, Space: "test-space-123456", Token: "secret-routing-token", AllowLoopback: true}
	_, _, err := transport.notificationSocket(t.Context())
	if err == nil || leaked.Load() || strings.Contains(err.Error(), transport.Token) {
		t.Fatal("unsafe redirect or error", err)
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(426)
		_, _ = w.Write([]byte(transport.Token))
	}))
	defer proxy.Close()
	transport.Base = proxy.URL
	_, unsupported, err := transport.notificationSocket(t.Context())
	if err == nil || !unsupported || strings.Contains(err.Error(), transport.Token) {
		t.Fatal("unsafe proxy fallback", unsupported, err)
	}
}

func TestNotificationFramesRefuseBinaryOversizedAndPayloads(t *testing.T) {
	for _, test := range []struct {
		name string
		kind websocket.MessageType
		body string
	}{
		{"binary", websocket.MessageBinary, `{"type":"mailbox-changed"}`},
		{"oversized", websocket.MessageText, strings.Repeat("x", 257)},
		{"payload", websocket.MessageText, `{"type":"session-content","secret":"must not appear"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				socket, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer socket.CloseNow()
				_ = socket.Write(r.Context(), test.kind, []byte(test.body))
				_, _, _ = socket.Read(r.Context())
			}))
			defer server.Close()
			transport := Transport{Base: server.URL, Space: "test-space-123456", Token: "test-token", AllowLoopback: true}
			socket, _, err := transport.notificationSocket(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer socket.CloseNow()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			err = readNotification(ctx, socket)
			if err == nil || strings.Contains(err.Error(), "must not appear") {
				t.Fatal("notification payload accepted or exposed", err)
			}
		})
	}
}
