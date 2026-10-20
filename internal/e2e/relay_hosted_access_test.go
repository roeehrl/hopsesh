package e2e

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/relay"
)

// Interactive qualification uses the real Access login in an external browser.
// It never reads browser cookies or substitutes an operator-issued credential.
// First log in with the actual CLI in an isolated namespace, then pass that
// namespace's relay directory. The second login exercises the desktop PKCE flow.
func TestRelayHostedAccessBrowser(t *testing.T) {
	if testing.Short() || os.Getenv("HOPSESH_HOSTED_ACCESS") != "1" {
		t.Skip("explicit interactive Cloudflare Access qualification")
	}
	const origin = "https://relay.hopsesh.codonic.dev"
	directory := os.Getenv("HOPSESH_HOSTED_ACCESS_CLI_STATE")
	if !filepath.IsAbs(directory) {
		t.Fatal("provide the absolute disposable CLI relay state directory")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	qualify := func(c relay.Connection) {
		t.Helper()
		if c.URL != origin || c.CAFile != "" || c.Expires <= time.Now().Unix() || c.Expires > time.Now().Add(24*time.Hour+time.Minute).Unix() {
			t.Fatal("unexpected hosted enrollment origin or lease")
		}
		t.Cleanup(func() {
			cleanup, done := context.WithTimeout(context.Background(), 15*time.Second)
			defer done()
			req, err := http.NewRequestWithContext(cleanup, http.MethodPost, origin+"/v1/enrollment/revoke", nil)
			if err != nil {
				t.Error("could not create cleanup request")
				return
			}
			req.Header.Set("Authorization", "Bearer "+c.Token)
			req.Header.Set("X-Hopsesh-Space", c.Space)
			res, err := client.Do(req)
			if err != nil {
				t.Error("hosted credential cleanup request failed")
				return
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 8192))
			_ = res.Body.Close()
			if res.StatusCode != http.StatusOK {
				t.Error("hosted credential cleanup refused", res.StatusCode)
				return
			}
			req, _ = http.NewRequestWithContext(cleanup, http.MethodGet, origin+"/v1/messages", nil)
			req.Header.Set("Authorization", "Bearer "+c.Token)
			req.Header.Set("X-Hopsesh-Space", c.Space)
			res, err = client.Do(req)
			if err != nil {
				t.Error("could not verify credential revocation")
				return
			}
			_ = res.Body.Close()
			if res.StatusCode != http.StatusForbidden {
				t.Error("revoked enrollment retained mailbox access", res.StatusCode)
			}
		})
		if _, err := (relay.Transport{Base: origin, Space: c.Space, Token: c.Token, HTTP: client}).Poll(ctx, 0); err != nil {
			t.Fatal("browser-issued credential cannot read its own mailbox")
		}
	}
	store := relay.Store{Directory: directory}
	headless, err := store.Connection(ctx)
	if err != nil {
		t.Fatal("complete the disposable CLI login before running this test")
	}
	qualify(headless)
	public, err := store.Public()
	if err != nil || headless.Device != public.ID {
		t.Fatal("CLI credential does not match its native identity")
	}
	grants, err := store.Grants()
	if err != nil || len(grants) != 0 {
		t.Fatal("CLI enrollment unexpectedly granted peer permissions")
	}
	t.Log("real CLI credential passed mailbox access and has no peer grants")
	id, err := relay.GenerateIdentity("hosted-disposable-desktop-login")
	if err != nil {
		t.Fatal(err)
	}
	connection, err := (relay.Enrollment{Origin: origin, HTTP: client}).Browser(ctx, id, func(uri string) error {
		t.Logf("Review in external browser: %s; compare fingerprint %s", uri, id.Public.ID)
		return nil
	})
	if err != nil {
		t.Fatal("hosted desktop login failed", err)
	}
	qualify(connection)
	if connection.Device != id.Public.ID || connection.Space != headless.Space {
		t.Fatal("same Access user did not receive its own account space and device binding")
	}
	t.Log("desktop PKCE callback and token exchange passed; same user space, distinct device; cleanup revokes both credentials")
}
