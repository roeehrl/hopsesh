package relay

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

// A response can be lost after the mailbox commits an operation. Exercise a
// real pooled connection, not a RoundTripper that invents a network error.
func TestHTTPMailboxReplayAfterReusedConnectionLoss(t *testing.T) {
	for _, method := range []string{"submit", "ack"} {
		for _, reused := range []bool{false, true} {
			name := method + "/fresh"
			if reused {
				name = method + "/reused"
			}
			t.Run(name, func(t *testing.T) {
				var mu sync.Mutex
				var first []byte
				requests := 0
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						_, _ = io.WriteString(w, `{"messages":[],"cursor":0}`)
						return
					}
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
						return
					}
					mu.Lock()
					defer mu.Unlock()
					requests++
					if r.Header.Get("Authorization") != "Bearer disposable-token" || r.Header.Get("X-Hopsesh-Space") != "disposable-space" {
						t.Error("replay changed authorization scope")
					}
					if _, sent := r.Header["Idempotency-Key"]; sent {
						t.Error("local replay hint leaked onto the wire")
					}
					if requests == 1 {
						first = body
						connection, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = connection.Close() // committed, but no HTTP response arrived
						return
					}
					if !bytes.Equal(first, body) {
						t.Error("replay changed the committed frame")
					}
					_, _ = io.WriteString(w, `{}`)
				}))
				defer server.Close()
				transport := Transport{Base: server.URL, Space: "disposable-space", Token: "disposable-token", HTTP: server.Client()}
				if reused {
					if _, err := transport.Poll(t.Context(), 0); err != nil {
						t.Fatal(err)
					}
				}
				var err error
				if method == "submit" {
					err = transport.Submit(t.Context(), Envelope{ID: "fixed-message-id", Operation: "fixed-operation", Ciphertext: []byte("unchanged-encrypted-frame")})
				} else {
					err = transport.Ack(t.Context(), 7)
				}
				mu.Lock()
				defer mu.Unlock()
				if reused && (err != nil || requests != 2) {
					t.Fatalf("reused connection did not recover the exact frame: requests=%d error=%v", requests, err)
				}
				if !reused && (err == nil || requests != 1) {
					t.Fatalf("fresh connection failure was retried: requests=%d error=%v", requests, err)
				}
			})
		}
	}
}

func TestHTTPMailboxReplayDoesNotRetryPolicyResponses(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = io.WriteString(w, `{"messages":[],"cursor":0}`)
					return
				}
				requests.Add(1)
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(status)
			}))
			defer server.Close()
			transport := Transport{Base: server.URL, Space: "disposable-space", Token: "disposable-token", HTTP: server.Client()}
			if _, err := transport.Poll(t.Context(), 0); err != nil {
				t.Fatal(err)
			}
			if err := transport.Ack(t.Context(), 7); err == nil || requests.Load() != 1 {
				t.Fatalf("policy response was hidden or retried: requests=%d error=%v", requests.Load(), err)
			}
		})
	}
}
