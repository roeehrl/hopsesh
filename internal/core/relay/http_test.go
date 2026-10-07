package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPTransportRefusesRedirectCredentialLeaksAndReportsProxyMethods(t *testing.T) {
	ctx := context.Background()
	var received bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received = true }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer s.Close()
	transport := Transport{Base: s.URL, Space: "test-space-123456", Token: "private-test-credential", AllowLoopback: true}
	if _, err := transport.Poll(ctx, 0); err == nil {
		t.Fatal("redirect followed")
	}
	if received {
		t.Fatal("credential sent to redirect target")
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(405)
		w.Write([]byte("credential echoed by malicious proxy"))
	}))
	defer proxy.Close()
	transport.Base = proxy.URL
	if err := transport.Submit(ctx, Envelope{}); err == nil || !strings.Contains(err.Error(), "POST access is required") || strings.Contains(err.Error(), "credential") {
		t.Fatal("unsafe or unclear proxy error", err)
	}
	for _, base := range []string{"http://relay.example", "https://user:password@relay.example", "https://relay.example?token=secret", "https://relay.example/path"} {
		transport.Base = base
		if _, err := transport.endpoint("/v1/messages"); err == nil {
			t.Fatal("unsafe URL accepted", base)
		}
	}
}
