package relay

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"syscall"
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

type failedResponseBody struct{ err error }

func (b failedResponseBody) Read([]byte) (int, error) { return 0, b.err }
func (failedResponseBody) Close() error               { return nil }

func TestHTTPFailureDiagnosticsIdentifyCauseWithoutPrivateDetails(t *testing.T) {
	const secret = "PRIVATE-PROXY-ERROR-with-credential"
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"cancel", context.Canceled, "canceled"},
		{"deadline", context.DeadlineExceeded, "timeout"},
		{"dns", &net.DNSError{Err: secret, Name: secret, Server: secret}, "dns"},
		{"certificate", x509.UnknownAuthorityError{Cert: &x509.Certificate{DNSNames: []string{secret}}}, "certificate"},
		{"reset", &net.OpError{Op: "read", Net: secret, Err: syscall.ECONNRESET}, "connection-reset"},
		{"refused", &net.OpError{Op: "dial", Net: secret, Err: syscall.ECONNREFUSED}, "connection-refused"},
		{"closed", io.EOF, "connection-closed"},
		{"truncated", io.ErrUnexpectedEOF, "connection-closed"},
		{"os-code", &net.OpError{Op: "read", Net: secret, Err: syscall.Errno(123456)}, "os-error-123456"},
		{"unknown", errors.New(secret), "network"},
	} {
		for _, responseBody := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/request", true: "/body"}[responseBody], func(t *testing.T) {
				cause := &url.Error{Op: "POST", URL: "https://" + secret, Err: tc.err}
				transport := Transport{Base: DefaultOrigin, Space: "diagnostic-space", Token: secret, HTTP: &http.Client{Transport: retryRoundTrip(func(*http.Request) (*http.Response, error) {
					if responseBody {
						return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: failedResponseBody{cause}}, nil
					}
					return nil, cause
				})}}
				_, err := transport.Poll(t.Context(), 0)
				if err == nil || !strings.Contains(err.Error(), "cause="+tc.want) || strings.Contains(err.Error(), secret) {
					t.Fatalf("missing safe failure classification: %v", err)
				}
				var privateCause *url.Error
				if errors.As(err, &privateCause) {
					t.Fatal("diagnostic retained private transport metadata")
				}
				phase := "request"
				if responseBody {
					phase = "response-body"
				}
				if !strings.Contains(err.Error(), "phase="+phase) || !strings.Contains(err.Error(), "operation=poll") {
					t.Fatal("lost failure phase", err)
				}
				if errors.Is(tc.err, context.Canceled) && !errors.Is(err, context.Canceled) || errors.Is(tc.err, context.DeadlineExceeded) && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("lost context cancellation identity", err)
				}
			})
		}
	}
}

func TestHTTPFailureOperationNeverIncludesRequestDetails(t *testing.T) {
	const secret = "private-token-and-path"
	for _, tc := range []struct{ method, path, want string }{
		{"GET", "/v1/messages?cursor=" + secret, "poll"},
		{"POST", "/v1/messages", "submit"},
		{"POST", "/v1/ack", "ack"},
		{"GET", "/" + secret, "unknown"},
		{secret, "/v1/messages", "unknown"},
	} {
		for _, status := range []int{0, http.StatusBadGateway} {
			transport := Transport{Base: DefaultOrigin, Space: "diagnostic-space", Token: secret, HTTP: &http.Client{Transport: retryRoundTrip(func(*http.Request) (*http.Response, error) {
				if status != 0 {
					return retryResponse(status, "", secret), nil
				}
				return nil, errors.New(secret)
			})}}
			err := transport.request(t.Context(), tc.method, tc.path, nil, nil)
			if err == nil || !strings.Contains(err.Error(), "operation="+tc.want) || strings.Contains(err.Error(), secret) {
				t.Fatalf("unsafe or missing operation diagnostic (status=%d): %v", status, err)
			}
		}
	}
}

func TestHTTPBudgetAndPauseDiagnosticsAreFixedAndBounded(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   error
	}{{429, `{"error":"traffic-budget"}`, ErrTrafficBudget}, {503, `{"error":"paused"}`, ErrOperatorPaused}, {503, `{"error":"private credential echoed by malicious proxy"}`, nil}, {429, strings.Repeat("private credential", 1000), nil}} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		transport := Transport{Base: s.URL, Space: "test-space-123456", Token: "private-test-credential", AllowLoopback: true}
		_, err := transport.Poll(t.Context(), 0)
		s.Close()
		if err == nil || tc.want != nil && !errors.Is(err, tc.want) || strings.Contains(err.Error(), "private credential") {
			t.Fatal("unsafe or unclear operator diagnostics", err)
		}
	}
}
