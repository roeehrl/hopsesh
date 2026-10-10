package e2e

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync"
	"testing"
	"time"
)

// Trace only the fresh, read-only failure probe. Callbacks can race with request
// cancellation, so retain a bounded synchronized snapshot. Never retain callback
// addresses, certificate data, headers, response bodies or raw error strings.
func relayProbeTrace(ctx context.Context) (context.Context, func() string) {
	started := time.Now()
	var mu sync.Mutex
	var events []string
	record := func(phase string) {
		mu.Lock()
		defer mu.Unlock()
		if len(events) == 32 {
			copy(events, events[1:])
			events = events[:31]
		}
		events = append(events, fmt.Sprintf("%s %s", time.Since(started).Round(time.Millisecond), phase))
	}
	trace := &httptrace.ClientTrace{
		GetConn:           func(string) { record("connection-requested") },
		DNSStart:          func(httptrace.DNSStartInfo) { record("dns-start") },
		DNSDone:           func(info httptrace.DNSDoneInfo) { record(fmt.Sprintf("dns-done-error=%t", info.Err != nil)) },
		ConnectStart:      func(string, string) { record("tcp-start") },
		ConnectDone:       func(_, _ string, err error) { record(fmt.Sprintf("tcp-done-error=%t", err != nil)) },
		TLSHandshakeStart: func() { record("tls-start") },
		TLSHandshakeDone:  func(_ tls.ConnectionState, err error) { record(fmt.Sprintf("tls-done-error=%t", err != nil)) },
		GotConn:           func(info httptrace.GotConnInfo) { record(fmt.Sprintf("connected-reused=%t", info.Reused)) },
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			record(fmt.Sprintf("request-written-error=%t", info.Err != nil))
		},
		GotFirstResponseByte: func() { record("first-response-byte") },
	}
	return httptrace.WithClientTrace(ctx, trace), func() string {
		mu.Lock()
		defer mu.Unlock()
		return strings.Join(events, "\n")
	}
}

func TestRelayProbeTrace(t *testing.T) {
	for _, stalled := range []bool{false, true} {
		t.Run(fmt.Sprintf("stalled=%t", stalled), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if stalled {
					// Cancel only after TLS and the complete request reached the server.
					cancel()
					<-r.Context().Done()
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			client := server.Client()
			defer client.CloseIdleConnections()
			ctx, snapshot := relayProbeTrace(ctx)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/private-route", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer PRIVATE-PROBE-TOKEN")
			res, err := client.Do(req)
			if res != nil {
				_ = res.Body.Close()
			}
			if (err != nil) != stalled {
				t.Fatalf("stalled=%t err=%v", stalled, err)
			}
			events := snapshot()
			for _, phase := range []string{"tcp-start", "tcp-done-error=false", "tls-start", "tls-done-error=false", "connected-reused=false", "request-written-error=false"} {
				if !strings.Contains(events, phase) {
					t.Errorf("missing %s in %s", phase, events)
				}
			}
			if strings.Contains(events, "first-response-byte") == stalled {
				t.Errorf("incorrect response stage: %s", events)
			}
			for _, private := range []string{server.URL, "private-route", "PRIVATE-PROBE-TOKEN"} {
				if strings.Contains(events, private) {
					t.Fatal("probe trace leaked request data")
				}
			}
		})
	}
}
