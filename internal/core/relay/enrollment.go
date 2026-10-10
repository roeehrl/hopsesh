package relay

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultOrigin = "https://relay.hopsesh.codonic.dev"

var errEnrollmentNetwork = errors.New("relay login HTTPS connection failed; check network access and trust roots")
var errEnrollmentTemporary = errors.New("relay login temporarily unavailable")

// DeviceAuthorization never leaves the in-memory login operation. In particular
// its device code is not returned by settings, diagnostics or CLI output.
type DeviceAuthorization struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type Enrollment struct {
	Origin string
	HTTP   *http.Client                               // explicit test/private trust roots; never disables TLS validation
	wait   func(context.Context, time.Duration) error // deterministic pacing tests
}

type enrollmentResponse struct {
	Error       string `json:"error"`
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Space       string `json:"space"`
	Device      string `json:"device"`
	Expires     int64  `json:"expires"`
}

func (e Enrollment) origin() (string, error) {
	// Reuse the delivery origin validator; enrollment has no credential yet.
	raw, err := (Transport{Base: e.Origin, Space: "enrollment-origin", Token: "validation"}).endpoint("")
	if err != nil {
		return "", err
	}
	u, err := url.Parse(raw)
	if err != nil || u.ForceQuery {
		return "", errors.New("relay login requires a canonical HTTPS origin")
	}
	u.Host = strings.ToLower(u.Host)
	if u.Port() == "443" {
		u.Host = u.Hostname()
		if strings.Contains(u.Host, ":") {
			u.Host = "[" + u.Host + "]"
		}
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func (e Enrollment) request(ctx context.Context, path string, values url.Values, out any) (int, error) {
	return e.requestAuthorized(ctx, path, values, out, "")
}

func (e Enrollment) requestAuthorized(ctx context.Context, path string, values url.Values, out any, token string) (status int, err error) {
	origin, err := e.origin()
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+path, strings.NewReader(values.Encode()))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if token != "" {
		if len(token) > 4096 || strings.ContainsAny(token, "\r\n") {
			return 0, errors.New("invalid relay routing credential")
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := http.Client{Timeout: 30 * time.Second}
	if e.HTTP != nil {
		client = *e.HTTP
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	r, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, errEnrollmentNetwork
	}
	defer r.Body.Close()
	if r.StatusCode == http.StatusTooManyRequests || r.StatusCode >= 500 {
		defer func() { err = withRetryAfter(errEnrollmentTemporary, r.Header.Get("Retry-After"), time.Now()) }()
	}
	if r.StatusCode >= 300 && r.StatusCode < 400 {
		return r.StatusCode, errors.New("relay login redirect refused")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8193))
	if err != nil {
		return r.StatusCode, err
	}
	if len(body) > 8192 || json.Unmarshal(body, out) != nil {
		return r.StatusCode, errors.New("relay login response is invalid or exceeds limit")
	}
	return r.StatusCode, nil
}

func (e Enrollment) Begin(ctx context.Context, identity Identity) (DeviceAuthorization, error) {
	var flow DeviceAuthorization
	if err := identity.Check(); err != nil {
		return flow, err
	}
	if identity.Public.Endpoint == "" || strings.HasPrefix(identity.Public.Endpoint, "cloud/") {
		return flow, errors.New("device login needs an initialized native machine identity")
	}
	origin, err := e.origin()
	if err != nil {
		return flow, err
	}
	nonce, err := NewOperationID()
	if err != nil {
		return flow, err
	}
	public, err := json.Marshal(identity.Public)
	if err != nil {
		return flow, err
	}
	values := url.Values{"client_id": {"hopsesh-headless-v1"}, "scope": {"relay.routing"}, "identity": {string(public)}, "nonce": {nonce}}
	values.Set("proof", base64.StdEncoding.EncodeToString(ed25519.Sign(identity.Signing, loginProof(origin, values))))
	status, err := e.request(ctx, "/v1/device/code", values, &flow)
	if err != nil {
		return flow, err
	}
	if status != http.StatusOK {
		return DeviceAuthorization{}, fmt.Errorf("relay login unavailable (HTTP %d); start again when the service is ready", status)
	}
	if !validDeviceCode(flow.DeviceCode) || len(flow.UserCode) != 11 || flow.UserCode[5] != '-' || strings.Trim(flow.UserCode[:5]+flow.UserCode[6:], "ABCDEF0123456789") != "" || flow.VerificationURI != origin+"/device" || flow.ExpiresIn < 1 || flow.ExpiresIn > 600 || flow.Interval < 5 || flow.Interval > 60 {
		return DeviceAuthorization{}, errors.New("relay login returned an invalid code, expiry or verification origin")
	}
	return flow, nil
}

func validDeviceCode(code string) bool {
	return len(code) == 64 && strings.Trim(code, "abcdef0123456789") == ""
}

// Wait follows RFC 8628 pacing. It stops on denial/expiry and does not initiate
// another login automatically. Only an explicit user action begins enrollment.
func (e Enrollment) Wait(ctx context.Context, flow DeviceAuthorization, device string) (Connection, error) {
	if !validDeviceCode(flow.DeviceCode) || flow.ExpiresIn < 1 || flow.ExpiresIn > 600 || flow.Interval < 5 || flow.Interval > 60 || !opaque(device) {
		return Connection{}, errors.New("invalid relay login operation")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(flow.ExpiresIn)*time.Second)
	defer cancel()
	interval := time.Duration(flow.Interval) * time.Second
	for {
		if err := e.pause(ctx, interval); err != nil {
			return Connection{}, err
		}
		var result enrollmentResponse
		status, err := e.request(ctx, "/v1/device/token", url.Values{"client_id": {"hopsesh-headless-v1"}, "grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "device_code": {flow.DeviceCode}}, &result)
		if err != nil {
			if ctx.Err() != nil {
				return Connection{}, ctx.Err()
			}
			if !errors.Is(err, errEnrollmentNetwork) && !errors.Is(err, errEnrollmentTemporary) {
				return Connection{}, err
			}
			// Back off on transport failures; never busy-loop a degraded service.
			interval = max(interval, min(interval*2, time.Minute), retryFloor(err))
			continue
		}
		if status == http.StatusOK {
			return e.connection(result, device)
		}
		if status != http.StatusBadRequest {
			return Connection{}, fmt.Errorf("relay login refused (HTTP %d)", status)
		}
		switch result.Error {
		case "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
		case "access_denied":
			return Connection{}, errors.New("relay login denied; no delivery access granted")
		case "expired_token", "invalid_grant":
			return Connection{}, errors.New("relay login code expired or invalid; start a new login explicitly")
		default:
			return Connection{}, errors.New("relay login authorization failed")
		}
	}
}

func loginProof(origin string, values url.Values) []byte {
	message := "hopsesh-relay-login-v1\x00" + origin + "\x00" + values.Get("nonce") + "\x00" + values.Get("identity")
	for _, key := range []string{"client_id", "scope", "redirect_uri", "code_challenge", "state"} {
		message += "\x00" + values.Get(key)
	}
	return []byte(message)
}

func (e Enrollment) connection(result enrollmentResponse, device string) (Connection, error) {
	if result.TokenType != "Bearer" || result.Scope != "relay.routing" || result.Device != device || !opaque(result.Space) || result.AccessToken == "" || len(result.AccessToken) > 4096 || strings.ContainsAny(result.AccessToken, "\r\n") || result.Expires <= time.Now().Unix() || result.Expires > time.Now().Add(MaxLifetime+time.Minute).Unix() {
		return Connection{}, errors.New("relay login returned an invalid or widened credential")
	}
	origin, err := e.origin()
	return Connection{URL: origin, Device: device, Space: result.Space, Token: result.AccessToken, Expires: result.Expires}, err
}

func (e Enrollment) pause(ctx context.Context, duration time.Duration) error {
	if e.wait != nil {
		return e.wait(ctx, duration)
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
