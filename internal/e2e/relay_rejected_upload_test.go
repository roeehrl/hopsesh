package e2e

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/relay"
)

func TestRelayRejectedUploadSQLiteR2(t *testing.T) {
	if testing.Short() || os.Getenv("HOPSESH_RELAY_PLATFORM") != "1" {
		t.Skip("requires disposable SQLite/R2 platform")
	}
	_, _, origin, _, client := startSQLiteRelayFixture(t, time.Minute)
	// Exercise idle connection reuse as well as early rejection before reading
	// a body. A dev proxy failure must not be mistaken for a policy refusal.
	// The follow-up uses the production submit path: a listener may close a
	// pooled connection after refusing an unread body, and the native client
	// replays message submissions only on such a reused connection.
	transport := relay.Transport{Base: origin, Token: "unapproved-token", Space: "unapproved-space-12345", HTTP: client}
	for i := range 6 {
		time.Sleep(5 * time.Second)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, origin+"/v1/messages", bytes.NewReader(bytes.Repeat([]byte("x"), 1200000)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Hopsesh-Space", "unapproved-space-12345")
		reply, err := client.Do(req)
		if err != nil {
			// An early refusal may close the large upload while the client
			// is still writing. The following small request must survive.
			t.Logf("early upload close on round %d: %v", i, err)
		} else {
			body, err := io.ReadAll(io.LimitReader(reply.Body, 4096))
			_ = reply.Body.Close()
			if reply.StatusCode != http.StatusForbidden {
				t.Fatalf("request %d large upload: HTTP %d: %s: %v", i, reply.StatusCode, body, err)
			}
			if err != nil {
				// The refusal arrived; the listener then closed the connection it
				// never read the body from, as it may while the client still writes.
				t.Logf("refused upload closed early on round %d: %v", i, err)
			}
		}
		if err := transport.Submit(t.Context(), relay.Envelope{}); !errors.Is(err, relay.ErrAuthorizationRefused) {
			t.Fatalf("request %d small submission: %v", i, err)
		}
	}
}

func TestRelayAdmissionIdleSQLiteR2(t *testing.T) {
	if testing.Short() || os.Getenv("HOPSESH_RELAY_PLATFORM") != "1" {
		t.Skip("requires disposable SQLite/R2 platform")
	}
	bin := buildHopsesh(t)
	ctx, _, origin, cert, client := startSQLiteRelayFixture(t, 2*time.Minute)
	f := newRelayFleet(t, ctx, bin, origin, cert, client)
	root := filepath.Join(f.homes['A'].home, "state")
	store := relay.Store{Directory: filepath.Join(root, "relay")}
	owner, err := store.Identity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := store.Connection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	admissions := relay.AdmissionStore{Directory: filepath.Join(root, "cloud-admissions")}
	// Native owner WebSockets stay open while authorization requests arrive at
	// a slower cadence. Local-dev proxy disconnects must not consume a request.
	for i := range 6 {
		time.Sleep(5 * time.Second)
		if _, err := admissions.IssueTask(ctx, owner, connection, "codex-current", "idle-issuance", time.Minute, ""); err != nil {
			t.Fatalf("issuance %d after idle: %v", i, err)
		}
	}
}
