package e2e

import (
	"bytes"
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
	for i := range 6 {
		time.Sleep(5 * time.Second)
		for _, size := range []int{1200000, 64} {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, origin+"/v1/messages", bytes.NewReader(bytes.Repeat([]byte("x"), size)))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("X-Hopsesh-Space", "unapproved-space-12345")
			reply, err := client.Do(req)
			if err != nil && size == 1200000 {
				// An early refusal may close the large upload while the client
				// is still writing. The following small request must survive.
				t.Logf("early upload close on round %d: %v", i, err)
				continue
			}
			if err != nil {
				t.Fatalf("request %d size %d: %v", i, size, err)
			}
			body, err := io.ReadAll(io.LimitReader(reply.Body, 4096))
			_ = reply.Body.Close()
			if err != nil || reply.StatusCode != http.StatusForbidden {
				t.Fatalf("request %d size %d: HTTP %d: %s: %v", i, size, reply.StatusCode, body, err)
			}
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
