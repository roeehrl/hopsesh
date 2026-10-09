package relay

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const MaxWireBytes = (MaxEnvelope * 4 / 3) + 65536

var ErrTrafficBudget = errors.New("relay daily traffic budget exhausted; existing deliveries can drain, and the operator can review its counters")
var ErrOperatorPaused = errors.New("relay operator paused new delivery; existing admitted work can still drain")
var ErrAuthorizationRefused = errors.New("relay authorization refused or revoked")

type Delivery struct {
	Sequence uint64   `json:"sequence"`
	Envelope Envelope `json:"envelope"`
}
type Batch struct {
	Messages []Delivery `json:"messages"`
	Cursor   uint64     `json:"cursor"`
}
type Transport struct {
	Base          string
	Token         string
	Space         string
	HTTP          *http.Client
	AllowLoopback bool
}

func (t Transport) endpoint(path string) (string, error) {
	u, err := url.Parse(t.Base)
	if err != nil {
		return "", errors.New("invalid relay URL")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return "", errors.New("relay URL must be an origin without credentials, query or path")
	}
	if u.Scheme != "https" && !(t.AllowLoopback && u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1")) {
		return "", errors.New("relay requires verified HTTPS")
	}
	if u.Host == "" || !opaque(t.Space) || t.Token == "" || strings.ContainsAny(t.Token, "\r\n") {
		return "", errors.New("invalid relay authorization")
	}
	return strings.TrimRight(u.String(), "/") + path, nil
}
func (t Transport) request(ctx context.Context, method, path string, body any, out any) (err error) {
	u, err := t.endpoint(path)
	if err != nil {
		return err
	}
	var data []byte
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
		if len(data) > MaxWireBytes {
			return errors.New("relay HTTP frame exceeds limit")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+t.Token)
	req.Header.Set("X-Hopsesh-Space", t.Space)
	req.Header.Set("Content-Type", "application/json")
	c := http.Client{Timeout: 30 * time.Second}
	if t.HTTP != nil {
		c = *t.HTTP
	}
	// Never forward credentials to a redirect target, including another hostname.
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	r, err := c.Do(req)
	if err != nil {
		return relayHTTPFailure("request", err)
	}
	defer r.Body.Close()
	if r.StatusCode == 429 || r.StatusCode >= 500 {
		defer func() { err = withRetryAfter(err, r.Header.Get("Retry-After"), time.Now()) }()
	}
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		// Only fixed protocol codes affect diagnostics. Never reflect a proxy's
		// arbitrary response body, headers, credentials or URLs into the UI/log.
		if r.StatusCode == 429 || r.StatusCode == 503 {
			body, _ := io.ReadAll(io.LimitReader(r.Body, 4097))
			var status struct {
				Error string `json:"error"`
			}
			if len(body) <= 4096 && json.Unmarshal(body, &status) == nil {
				if r.StatusCode == 429 && status.Error == "traffic-budget" {
					return ErrTrafficBudget
				}
				if r.StatusCode == 503 && status.Error == "paused" {
					return ErrOperatorPaused
				}
			}
		}
		switch r.StatusCode {
		case 401, 403:
			return ErrAuthorizationRefused
		case 405:
			return errors.New("provider proxy or relay refuses this HTTP method; POST access is required")
		case 413, 429:
			return errors.New("relay quota or frame limit exceeded")
		case 409:
			return errors.New("relay message ID reused with different content")
		}
		return fmt.Errorf("relay request failed with HTTP %d", r.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, MaxWireBytes+1))
	if err != nil {
		return relayHTTPFailure("response-body", err)
	}
	if len(b) > MaxWireBytes {
		return errors.New("relay response exceeds limit")
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(b, out)
}

// Keep diagnostics actionable without retaining the raw transport error, which
// can contain a proxy URL, certificate names or other private infrastructure.
// Only context sentinels survive as causes; classification does not add retries.
func relayHTTPFailure(phase string, err error) error {
	kind := "network"
	var cause error
	var dns *net.DNSError
	var certificate *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	var network net.Error
	var errno syscall.Errno
	switch {
	case errors.Is(err, context.Canceled):
		kind, cause = "canceled", context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		kind, cause = "timeout", context.DeadlineExceeded
	case errors.As(err, &certificate), errors.As(err, &unknown), errors.As(err, &invalid), errors.As(err, &hostname):
		kind = "certificate"
	case errors.As(err, &dns):
		kind = "dns"
	case errors.Is(err, syscall.ECONNRESET):
		kind = "connection-reset"
	case errors.Is(err, syscall.ECONNREFUSED):
		kind = "connection-refused"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		kind = "connection-closed"
	case errors.As(err, &network) && network.Timeout():
		kind = "timeout"
	case errors.As(err, &errno):
		// Native Windows socket errors use Winsock codes rather than the
		// portable syscall constants above. Numeric OS codes contain no URLs.
		kind = fmt.Sprintf("os-error-%d", uint64(errno))
	}
	message := fmt.Sprintf("relay HTTPS connection failed; check proxy, trust roots and network access (phase=%s cause=%s)", phase, kind)
	if cause != nil {
		return fmt.Errorf("%s: %w", message, cause)
	}
	return errors.New(message)
}
func (t Transport) Submit(ctx context.Context, e Envelope) error {
	return t.request(ctx, http.MethodPost, "/v1/messages", e, nil)
}
func (t Transport) Poll(ctx context.Context, cursor uint64) (Batch, error) {
	var b Batch
	err := t.request(ctx, http.MethodGet, "/v1/messages?cursor="+strconv.FormatUint(cursor, 10), nil, &b)
	return b, err
}
func (t Transport) Ack(ctx context.Context, cursor uint64) error {
	return t.request(ctx, http.MethodPost, "/v1/ack", struct {
		Cursor uint64 `json:"cursor"`
	}{cursor}, nil)
}
