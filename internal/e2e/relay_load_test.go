package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/relay"
)

// One hundred logical endpoint identities span four account spaces, respecting
// the 32-device boundary. This qualifies actual crypto/HTTPS/SQLite/R2 admission
// and drain; it is not a claim about 100 physical hosts or native GUI resources.
func TestRelayLoadSQLiteR2(t *testing.T) {
	if testing.Short() || os.Getenv("HOPSESH_RELAY_PLATFORM") != "1" {
		t.Skip("actual 100 endpoint platform load qualification")
	}
	ctx, _, origin, _, client := startSQLiteRelayFixture(t, 2*time.Minute)
	const admin = "fixture-admin-secret-with-32-bytes-minimum"
	type endpoint struct {
		identity  relay.Identity
		transport relay.Transport
		space     string
	}
	endpoints := make([]endpoint, 100)
	for i := range endpoints {
		id, err := relay.GenerateIdentity()
		if err != nil {
			t.Fatal(err)
		}
		space := fmt.Sprintf("load-account-space-%02d", i/32)
		body, _ := json.Marshal(map[string]any{"device": id.Public.ID, "ttl": 3600})
		req, err := http.NewRequestWithContext(ctx, "POST", origin+"/v1/enrollment/register", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+admin)
		req.Header.Set("X-Hopsesh-Space", space)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var c relay.Connection
		err = json.NewDecoder(res.Body).Decode(&c)
		_ = res.Body.Close()
		if err != nil || res.StatusCode != 201 {
			t.Fatal("100 endpoint enrollment", i, res.StatusCode, err)
		}
		endpoints[i] = endpoint{id, relay.Transport{Base: origin, Space: space, Token: c.Token, HTTP: client}, space}
	}
	for i, from := range endpoints {
		start := i / 32 * 32
		size := min(32, len(endpoints)-start)
		next := start + (i-start+1)%size
		op, err := relay.NewOperationID()
		if err != nil {
			t.Fatal(err)
		}
		e, err := relay.Seal(from.identity, endpoints[next].identity.Public, from.space, op, []byte(fmt.Sprintf("load-sentinel-%03d", i)), time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err = from.transport.Submit(ctx, e); err != nil {
			t.Fatal("100 endpoint publication", i, err)
		}
		if err = from.transport.Submit(ctx, e); err != nil {
			t.Fatal("100 endpoint duplicate retry", i, err)
		}
	}
	for i, to := range endpoints {
		start := i / 32 * 32
		size := min(32, len(endpoints)-start)
		previous := start + (i-start+size-1)%size
		batch, err := to.transport.Poll(ctx, 0)
		if err != nil || len(batch.Messages) != 1 {
			t.Fatal("100 endpoint mailbox cardinality", i, err)
		}
		plain, err := relay.Open(to.identity, endpoints[previous].identity.Public, batch.Messages[0].Envelope, to.space, time.Now())
		if err != nil || string(plain) != fmt.Sprintf("load-sentinel-%03d", previous) {
			t.Fatal("100 endpoint authenticated decryption", i, err)
		}
		if err = to.transport.Ack(ctx, batch.Cursor); err != nil {
			t.Fatal("100 endpoint drain", i, err)
		}
	}
	for group := range 4 {
		req, err := http.NewRequestWithContext(ctx, "GET", origin+"/v1/operator/stats", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+admin)
		req.Header.Set("X-Hopsesh-Space", endpoints[group*32].space)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var counts struct{ Devices, Messages, AdmittedFrames, CiphertextBytes int }
		err = json.NewDecoder(res.Body).Decode(&counts)
		_ = res.Body.Close()
		want := min(32, len(endpoints)-group*32)
		if err != nil || res.StatusCode != 200 || counts.Devices != want || counts.AdmittedFrames != want || counts.Messages != 0 || counts.CiphertextBytes != 0 {
			t.Fatal("100 endpoint retained counts or leaked ciphertext", group, counts, err)
		}
	}
	t.Log("100 logical endpoints / four account spaces: native age, signed envelopes, verified HTTPS, SQLite/R2, duplicate retry and full acknowledgment drain passed")
}
