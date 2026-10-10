package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// A reply already committed to the mailbox must not wait behind the sender's
// minute-long idle reconciliation timer. Keep the real encryption, grant and
// durable request/reply path, with a deterministic clock and HTTP transport.
func TestHTTPFallbackActiveRequestWakesIdleMailbox(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, b := identities(t)
		store := Store{Directory: privateTemp(t)}
		if err := store.Approve(t.Context(), Grant{Peer: b.Public, Kind: "device", SendMethods: []string{"observe"}}); err != nil {
			t.Fatal(err)
		}
		const space = "active-request-space"
		var mu sync.Mutex
		var pending *Envelope
		polls := 0
		httpClient := &http.Client{Transport: retryRoundTrip(func(r *http.Request) (*http.Response, error) {
			mu.Lock()
			defer mu.Unlock()
			if r.URL.Path == "/v1/notifications" {
				return retryResponse(501, "", ""), nil
			}
			if r.Method == http.MethodPost && r.URL.Path == "/v1/messages" {
				var request Envelope
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					return nil, err
				}
				if _, err := Open(b, a.Public, request, space, time.Now()); err != nil {
					return nil, err
				}
				reply, err := sealKind(b, a.Public, space, request.Operation, "response", []byte(`{"method":"observe","outcome":{"result":{"ready":true}}}`), time.Now(), time.Until(time.Unix(request.Expires, 0)))
				pending = &reply
				return retryResponse(200, "", `{}`), err
			}
			if r.Method == http.MethodGet {
				polls++
				batch := Batch{}
				if pending != nil {
					batch.Messages = []Delivery{{Sequence: 1, Envelope: *pending}}
					batch.Cursor = 1
					pending = nil
				}
				body, _ := json.Marshal(batch)
				return retryResponse(200, "", string(body)), nil
			}
			return retryResponse(200, "", `{}`), nil
		})}
		service := Service{Transport: Transport{Base: DefaultOrigin, Space: space, Token: "fixture", HTTP: httpClient}, Processor: Processor{Identity: a, Space: space, Store: store}}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- service.Run(ctx) }()
		defer func() { cancel(); <-done }()
		time.Sleep(2 * time.Minute)
		bounded, stop := context.WithTimeout(ctx, 3*time.Second)
		result, err := service.Call(bounded, b.Public.ID, "active-request-operation", "observe", nil)
		stop()
		if err != nil || string(result) != `{"ready":true}` {
			t.Fatal("committed reply waited behind idle fallback", string(result), err)
		}
		// After completing the request, empty polling returns to its ordinary
		// adaptive cadence. A client subscription adds no permanent fast timer.
		time.Sleep(2 * time.Minute)
		mu.Lock()
		before := polls
		mu.Unlock()
		time.Sleep(20 * time.Second)
		mu.Lock()
		delta := polls - before
		mu.Unlock()
		if delta > 1 {
			t.Fatal("completed request left fast polling enabled", delta)
		}
	})
}

func TestHTTPFallbackActiveRequestStopsAtLeaseExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, b := identities(t)
		store := Store{Directory: privateTemp(t)}
		if err := store.Approve(t.Context(), Grant{Peer: b.Public, Kind: "device", SendMethods: []string{"observe"}}); err != nil {
			t.Fatal(err)
		}
		client := &http.Client{Transport: retryRoundTrip(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/v1/notifications" {
				return retryResponse(501, "", ""), nil
			}
			return retryResponse(200, "", `{"messages":[],"cursor":0}`), nil
		})}
		service := Service{Transport: Transport{Base: DefaultOrigin, Space: "expired-active-request", Token: "fixture", HTTP: client}, Processor: Processor{Identity: a, Space: "expired-active-request", Store: store}}
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
		done := make(chan error, 1)
		go func() { done <- service.Run(ctx) }()
		defer func() { cancel(); <-done }()
		started := time.Now()
		_, err := service.Call(ctx, b.Public.ID, "expired-operation-id", "observe", nil)
		if err == nil || !strings.Contains(err.Error(), "request lease expired") || time.Since(started) > 90*time.Second {
			t.Fatal("expired reply kept active request alive", err, time.Since(started))
		}
		service.mu.Lock()
		pending := len(service.waiters)
		service.mu.Unlock()
		if pending != 0 {
			t.Fatal("expired request retained fast-poll demand", pending)
		}
	})
}

func TestHTTPFallbackActiveRequestsRespectRetryAfter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		activity := make(chan struct{}, 1)
		started := time.Now()
		polls := 0
		transport := Transport{Base: DefaultOrigin, Space: "busy-backoff-space", Token: "fixture", HTTP: &http.Client{Transport: retryRoundTrip(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/v1/notifications" {
				return retryResponse(501, "", ""), nil
			}
			polls++
			if polls == 1 {
				return retryResponse(429, "10", `{}`), nil
			}
			if time.Since(started) < 10*time.Second {
				t.Error("active request bypassed Retry-After", time.Since(started))
			}
			cancel()
			return retryResponse(200, "", `{"messages":[],"cursor":0}`), nil
		})}}
		done := make(chan error, 1)
		go func() {
			done <- (Listener{Transport: transport, RequestActivity: activity, ActiveRequests: func() bool { return true }}).Run(ctx)
		}()
		for range 9 {
			time.Sleep(time.Second)
			activity <- struct{}{}
		}
		<-done
		if polls != 2 {
			t.Fatal("unexpected retries", polls)
		}
	})
}
