package relay

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestBrowserLoginPKCERejectsForgedCallbacksAndClosesListener(t *testing.T) {
	id, _ := GenerateIdentity("native-browser-login")
	var origin, redirect, state, challenge string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ParseForm() != nil {
			t.Error("invalid form")
		}
		if r.URL.Path == "/v1/authorization/request" {
			proof, _ := base64.StdEncoding.DecodeString(r.Form.Get("proof"))
			if !ed25519.Verify(id.Public.Signing, loginProof(origin, r.Form), proof) || r.Form.Get("code_challenge_method") != "S256" {
				t.Error("unsigned or non-PKCE approval request")
			}
			redirect, state, challenge = r.Form.Get("redirect_uri"), r.Form.Get("state"), r.Form.Get("code_challenge")
			_ = json.NewEncoder(w).Encode(map[string]any{"authorization_uri": origin + "/device?user_code=ABCDE-12345", "user_code": "ABCDE-12345", "expires_in": 600})
			return
		}
		verifier := r.Form.Get("code_verifier")
		sum := sha256.Sum256([]byte(verifier))
		if len(verifier) < 43 || challenge != base64.RawURLEncoding.EncodeToString(sum[:]) || r.Form.Get("code") != strings.Repeat("c", 64) || r.Form.Get("redirect_uri") != redirect {
			t.Error("PKCE or callback binding changed")
		}
		_ = json.NewEncoder(w).Encode(enrollmentResponse{AccessToken: "scoped-routing-secret", TokenType: "Bearer", Scope: "relay.routing", Space: "test-browser-space-123", Device: id.Public.ID, Expires: time.Now().Add(time.Hour).Unix()})
	}))
	defer server.Close()
	origin = server.URL
	login := Enrollment{Origin: origin, HTTP: server.Client()}
	connection, err := login.Browser(t.Context(), id, func(uri string) error {
		if uri != origin+"/device?user_code=ABCDE-12345" {
			t.Fatal("browser opened unverified origin")
		}
		for _, suffix := range []string{"?state=forged&code=" + strings.Repeat("c", 64), "?state=" + state + "&code=" + strings.Repeat("c", 64) + "&code=other"} {
			res, err := http.Get(redirect + suffix)
			if err != nil {
				return err
			}
			_ = res.Body.Close()
			if res.StatusCode != 400 {
				t.Error("forged callback accepted")
			}
		}
		res, err := http.Get(redirect + "?state=" + state + "&code=" + strings.Repeat("c", 64))
		if err != nil {
			return err
		}
		_ = res.Body.Close()
		if res.StatusCode != 200 {
			t.Error("valid callback refused")
		}
		return nil
	})
	if err != nil || connection.Token != "scoped-routing-secret" {
		t.Fatal("browser login failed", err)
	}
	u, _ := url.Parse(redirect)
	conn, err := net.DialTimeout("tcp", u.Host, time.Second)
	if err == nil {
		conn.Close()
		t.Fatal("login callback listener survived completion")
	}
}

func TestBrowserLoginCancellationClosesCallbackWithoutEnrolling(t *testing.T) {
	id, _ := GenerateIdentity("native-browser-login")
	var origin, redirect string
	exchanges := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/authorization/request" {
			exchanges++
		}
		_ = r.ParseForm()
		redirect = r.Form.Get("redirect_uri")
		_ = json.NewEncoder(w).Encode(map[string]any{"authorization_uri": origin + "/device?user_code=ABCDE-12345", "user_code": "ABCDE-12345", "expires_in": 600})
	}))
	defer server.Close()
	origin = server.URL
	ctx, cancel := context.WithCancel(t.Context())
	_, err := (Enrollment{Origin: origin, HTTP: server.Client()}).Browser(ctx, id, func(string) error { cancel(); return nil })
	if err != context.Canceled || exchanges != 0 {
		t.Fatal("canceled browser login exchanged credential", err)
	}
	u, _ := url.Parse(redirect)
	conn, err := net.DialTimeout("tcp", u.Host, time.Second)
	if err == nil {
		conn.Close()
		t.Fatal("canceled login retained listener")
	}
}
