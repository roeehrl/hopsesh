package relay

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDeviceLoginProofAndPacingNeverExposeOrWidenCredentials(t *testing.T) {
	id, err := GenerateIdentity("native-login-machine")
	if err != nil {
		t.Fatal(err)
	}
	var origin string
	var waits []time.Duration
	polls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.ParseForm() != nil {
			t.Error("invalid OAuth request")
		}
		if r.URL.Path == "/v1/device/code" {
			raw, nonce := r.Form.Get("identity"), r.Form.Get("nonce")
			proof, _ := base64.StdEncoding.DecodeString(r.Form.Get("proof"))
			if !ed25519.Verify(id.Public.Signing, loginProof(origin, r.Form), proof) {
				t.Error("device key possession was not proven")
			}
			if strings.Contains(raw, id.Encryption) || len(nonce) != 32 || r.Form.Get("scope") != "relay.routing" {
				t.Error("private state or widened scope")
			}
			_ = json.NewEncoder(w).Encode(DeviceAuthorization{DeviceCode: strings.Repeat("a", 64), UserCode: "ABCDE-12345", VerificationURI: origin + "/device", ExpiresIn: 600, Interval: 5})
			return
		}
		if r.Form.Get("device_code") != strings.Repeat("a", 64) || r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" {
			t.Error("invalid token grant")
		}
		polls++
		if polls <= 2 {
			w.WriteHeader(http.StatusBadRequest)
			code := "authorization_pending"
			if polls == 2 {
				code = "slow_down"
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
			return
		}
		_ = json.NewEncoder(w).Encode(enrollmentResponse{AccessToken: "scoped-routing-secret", TokenType: "Bearer", Scope: "relay.routing", Space: "test-login-space-123", Device: id.Public.ID, Expires: time.Now().Add(time.Hour).Unix()})
	}))
	defer server.Close()
	origin = server.URL
	login := Enrollment{Origin: origin, HTTP: server.Client(), wait: func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }}
	flow, err := login.Begin(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	c, err := login.Wait(t.Context(), flow, id.Public.ID)
	if err != nil || c.Device != id.Public.ID || c.Token != "scoped-routing-secret" || !reflect.DeepEqual(waits, []time.Duration{5 * time.Second, 5 * time.Second, 10 * time.Second}) {
		t.Fatalf("login pacing/result: %+v %v %v", c, waits, err)
	}
}

func TestDeviceLoginRejectsRedirectOtherVerificationOriginAndWidenedToken(t *testing.T) {
	id, _ := GenerateIdentity("native-login-machine")
	for _, mode := range []string{"redirect", "other-origin", "denied", "widened", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			var origin string
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if mode == "redirect" {
					http.Redirect(w, r, "https://other.invalid", http.StatusFound)
					return
				}
				if mode == "oversized" {
					_, _ = w.Write([]byte(strings.Repeat("x", 8193)))
					return
				}
				if r.URL.Path == "/v1/device/code" {
					uri := origin + "/device"
					if mode == "other-origin" {
						uri = "https://other.invalid/device"
					}
					_ = json.NewEncoder(w).Encode(DeviceAuthorization{DeviceCode: strings.Repeat("a", 64), UserCode: "ABCDE-12345", VerificationURI: uri, ExpiresIn: 600, Interval: 5})
					return
				}
				if mode == "denied" {
					w.WriteHeader(http.StatusBadRequest)
					_ = json.NewEncoder(w).Encode(map[string]string{"error": "access_denied"})
					return
				}
				_ = json.NewEncoder(w).Encode(enrollmentResponse{Scope: "relay.admin", TokenType: "Bearer", AccessToken: "secret", Device: id.Public.ID, Space: "test-login-space-123", Expires: time.Now().Add(time.Hour).Unix()})
			}))
			defer server.Close()
			origin = server.URL
			login := Enrollment{Origin: origin, HTTP: server.Client(), wait: func(context.Context, time.Duration) error { return nil }}
			flow, err := login.Begin(t.Context(), id)
			if err == nil {
				_, err = login.Wait(t.Context(), flow, id.Public.ID)
			}
			if err == nil || calls > 2 {
				t.Fatalf("invalid login accepted or repeated: %v, %d calls", err, calls)
			}
		})
	}
}

func TestDeviceLoginCancellationAndCloudKeysDoNotStartAuthorization(t *testing.T) {
	id, _ := GenerateIdentity("cloud/claude-hosted/test")
	if _, err := (Enrollment{Origin: DefaultOrigin}).Begin(t.Context(), id); err == nil {
		t.Fatal("cloud keys enrolled as a personal device")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	flow := DeviceAuthorization{DeviceCode: strings.Repeat("a", 64), ExpiresIn: 600, Interval: 5}
	if _, err := (Enrollment{Origin: DefaultOrigin}).Wait(ctx, flow, strings.Repeat("b", 64)); err != context.Canceled {
		t.Fatal("cancellation did not stop pending login", err)
	}
}

func TestEnrollmentCredentialMustMatchCurrentIdentityAndBoundedLease(t *testing.T) {
	store := Store{Directory: filepath.Join(t.TempDir(), "relay")}
	id, err := store.Identity(t.Context(), "native-endpoint")
	if err != nil {
		t.Fatal(err)
	}
	c := Connection{URL: DefaultOrigin, Space: "test-enrollment-space1", Device: id.Public.ID, Token: "scoped-secret", Expires: time.Now().Add(time.Hour).Unix()}
	other, _ := GenerateIdentity("other-endpoint")
	c.Device = other.Public.ID
	if err = store.SetConnection(t.Context(), c); err == nil {
		t.Fatal("another endpoint's enrollment was adopted")
	}
	c.Device = id.Public.ID
	c.Expires = time.Now().Add(48 * time.Hour).Unix()
	if err = store.SetConnection(t.Context(), c); err == nil {
		t.Fatal("unbounded enrollment lease accepted")
	}
	c.Expires = time.Now().Add(time.Hour).Unix()
	if err = store.SetConnection(t.Context(), c); err != nil {
		t.Fatal("bounded matching credential refused", err)
	}
}

func TestEnrollmentOriginUsesBrowserCanonicalization(t *testing.T) {
	for input, want := range map[string]string{"https://RELAY.EXAMPLE.INVALID:443/": "https://relay.example.invalid", "https://[::1]:443/": "https://[::1]", "https://relay.example.invalid:8443": "https://relay.example.invalid:8443"} {
		got, err := (Enrollment{Origin: input}).origin()
		if err != nil || got != want {
			t.Fatal("origin differs from browser proof binding", input, got, err)
		}
	}
	if _, err := (Enrollment{Origin: "https://relay.example.invalid?"}).origin(); err == nil {
		t.Fatal("empty query accepted as an origin")
	}
}
