package e2e

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSQLiteRelayClientAllowsColdStartAndCancelsRequests(t *testing.T) {
	cert, key, _ := relayFixtureCertificate(t, t.TempDir())
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/slow":
			// The Windows platform fixture can finish a cold SQLite enrollment
			// after the former five-second test-only HTTP deadline.
			timer := time.NewTimer(6 * time.Second)
			defer timer.Stop()
			select {
			case <-timer.C:
				w.WriteHeader(http.StatusCreated)
			case <-r.Context().Done():
			}
		case "/cancel":
			close(started)
			<-r.Context().Done()
		default:
			t.Errorf("unexpected fixture request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	client := sqliteRelayFixtureClient(t, cert)
	defer client.CloseIdleConnections()
	t.Run("cold enrollment", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/slow", nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal("fixture enrollment ended before its scenario deadline", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusCreated {
			t.Fatal("fixture enrollment status", res.StatusCode)
		}
	})
	t.Run("scenario cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/cancel", nil)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			res, err := client.Do(req)
			if res != nil {
				_ = res.Body.Close()
			}
			done <- err
		}()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("fixture did not receive request")
		}
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal("scenario cancellation was not preserved", err)
			}
		case <-time.After(time.Second):
			t.Fatal("fixture client ignored scenario cancellation")
		}
	})
}
