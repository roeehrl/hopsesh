package relay

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Browser uses the external browser, S256 PKCE and an ephemeral literal loopback
// callback. Unlike the headless device flow, it needs no polling while the user
// reviews approval. No verifier, code or routing token crosses the GUI bridge.
func (e Enrollment) Browser(ctx context.Context, identity Identity, open func(string) error) (Connection, error) {
	if err := identity.Check(); err != nil {
		return Connection{}, err
	}
	if identity.Public.Endpoint == "" || strings.HasPrefix(identity.Public.Endpoint, "cloud/") || open == nil {
		return Connection{}, errors.New("browser login requires a native endpoint and an external browser")
	}
	origin, err := e.origin()
	if err != nil {
		return Connection{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		listener, err = net.Listen("tcp", "[::1]:0")
	}
	if err != nil {
		return Connection{}, errors.New("could not open the local login callback")
	}
	defer listener.Close()
	redirect := "http://" + listener.Addr().String() + "/callback"
	state, err := loginRandom()
	if err != nil {
		return Connection{}, err
	}
	verifier, err := loginRandom()
	if err != nil {
		return Connection{}, err
	}
	hash := sha256.Sum256([]byte(verifier))
	nonce, err := NewOperationID()
	if err != nil {
		return Connection{}, err
	}
	public, err := json.Marshal(identity.Public)
	if err != nil {
		return Connection{}, err
	}
	values := url.Values{"client_id": {"hopsesh-desktop-v1"}, "scope": {"relay.routing"}, "response_type": {"code"}, "redirect_uri": {redirect}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "state": {state}, "identity": {string(public)}, "nonce": {nonce}}
	values.Set("proof", base64.StdEncoding.EncodeToString(ed25519.Sign(identity.Signing, loginProof(origin, values))))
	var result struct {
		URI       string `json:"authorization_uri"`
		UserCode  string `json:"user_code"`
		ExpiresIn int    `json:"expires_in"`
	}
	status, err := e.request(ctx, "/v1/authorization/request", values, &result)
	if err != nil {
		return Connection{}, err
	}
	if status != http.StatusOK || result.ExpiresIn < 1 || result.ExpiresIn > 600 || len(result.UserCode) != 11 || result.UserCode[5] != '-' || strings.Trim(result.UserCode[:5]+result.UserCode[6:], "ABCDEF0123456789") != "" || result.URI != origin+"/device?user_code="+result.UserCode {
		return Connection{}, errors.New("relay browser login is unavailable or returned an invalid approval origin")
	}
	callback := make(chan string, 1)
	var once sync.Once
	server := &http.Server{ReadHeaderTimeout: 3 * time.Second, IdleTimeout: 3 * time.Second, MaxHeaderBytes: 8192, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || r.Method != http.MethodGet || r.Host != listener.Addr().String() || r.URL.Path != "/callback" || len(r.URL.RawQuery) > 4096 || len(query["state"]) != 1 || subtle.ConstantTimeCompare([]byte(query.Get("state")), []byte(state)) != 1 {
			http.Error(w, "Invalid login callback", http.StatusBadRequest)
			return
		}
		code := query.Get("code")
		denied := query.Get("error") == "access_denied" && len(query["error"]) == 1 && len(query["code"]) == 0
		if !denied && (len(query["code"]) != 1 || len(query["error"]) != 0 || !validDeviceCode(code)) {
			http.Error(w, "Invalid login callback", http.StatusBadRequest)
			return
		}
		if denied {
			code = "denied"
		}
		once.Do(func() { callback <- code })
		_, _ = w.Write([]byte("Approval received. Return to Hopsesh; you can close this tab."))
	})}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(listener) }()
	defer func() { _ = server.Close(); <-done }()
	if err = open(result.URI); err != nil {
		return Connection{}, err
	}
	var code string
	select {
	case <-ctx.Done():
		return Connection{}, ctx.Err()
	case <-done:
		return Connection{}, errors.New("local login callback closed before approval")
	case code = <-callback:
	}
	if code == "denied" {
		return Connection{}, errors.New("relay login denied; no delivery access granted")
	}
	exchange, stop := context.WithTimeout(ctx, time.Minute)
	defer stop()
	for delay := time.Second; ; delay = min(delay*2, 8*time.Second) {
		var grant enrollmentResponse
		status, err = e.request(exchange, "/v1/authorization/token", url.Values{"client_id": {"hopsesh-desktop-v1"}, "grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {redirect}}, &grant)
		if err == nil && status == http.StatusOK {
			return e.connection(grant, identity.Public.ID)
		}
		if err != nil && !errors.Is(err, errEnrollmentNetwork) && !errors.Is(err, errEnrollmentTemporary) {
			return Connection{}, err
		}
		if err == nil && status < 500 && status != http.StatusTooManyRequests {
			return Connection{}, errors.New("relay browser approval expired or was refused; start a new login explicitly")
		}
		if err = e.pause(exchange, retryDelay(delay, err)); err != nil {
			return Connection{}, err
		}
	}
}

func loginRandom() (string, error) {
	a, err := NewOperationID()
	if err != nil {
		return "", err
	}
	b, err := NewOperationID()
	return a + b, err
}
