package relay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

// Notifications contain only a wake hint. Authentication, decryption, durable
// outcomes and acknowledgement always use the existing mailbox protocol.
func (t Transport) notificationSocket(ctx context.Context) (*websocket.Conn, bool, error) {
	origin, err := t.endpoint("/v1/notifications")
	if err != nil {
		return nil, false, err
	}
	client := http.Client{}
	if t.HTTP != nil {
		client = *t.HTTP
	}
	client.Timeout = 0 // A stream outlives an individual HTTP request.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+t.Token)
	headers.Set("X-Hopsesh-Space", t.Space)
	handshake, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	socket, response, err := websocket.Dial(handshake, origin, &websocket.DialOptions{HTTPClient: &client, HTTPHeader: headers})
	if err != nil {
		unsupported := response != nil && (response.StatusCode == 404 || response.StatusCode == 405 || response.StatusCode == 426 || response.StatusCode == 501)
		return nil, unsupported, errors.New("relay notifications unavailable; using bounded HTTP reconciliation")
	}
	socket.SetReadLimit(256)
	return socket, false, nil
}

func readNotification(ctx context.Context, socket *websocket.Conn) error {
	kind, body, err := socket.Read(ctx)
	if err != nil {
		return errors.New("relay notification connection ended")
	}
	var hint struct {
		Type string `json:"type"`
	}
	if kind != websocket.MessageText || json.Unmarshal(body, &hint) != nil || hint.Type != "mailbox-changed" {
		return errors.New("relay notification frame refused")
	}
	return nil
}

func consumeNotifications(ctx context.Context, socket *websocket.Conn, wake chan<- struct{}) {
	child, cancel := context.WithCancel(ctx)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for readNotification(child, socket) == nil {
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	}()
	defer func() { cancel(); _ = socket.CloseNow(); <-readDone }()
	// Protocol ping/pong does not wake a hibernating Durable Object. A broken
	// idle proxy is discovered without renewing observation timestamps.
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-readDone:
			return
		case <-ticker.C:
			ping, stop := context.WithTimeout(child, 10*time.Second)
			err := socket.Ping(ping)
			stop()
			if err != nil {
				return
			}
		}
	}
}

func (t Transport) notifications(ctx context.Context, wake chan<- struct{}, state chan<- bool) {
	delay := time.Second
	report := func(connected bool) bool {
		select {
		case state <- connected:
			return true
		case <-ctx.Done():
			return false
		}
	}
	for ctx.Err() == nil {
		socket, unsupported, err := t.notificationSocket(ctx)
		if err == nil {
			if !report(true) {
				_ = socket.CloseNow()
				return
			}
			consumeNotifications(ctx, socket, wake)
		}
		if !report(false) {
			return
		}
		if unsupported {
			delay = 5 * time.Minute
		} else {
			delay = min(time.Minute, delay*2)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
