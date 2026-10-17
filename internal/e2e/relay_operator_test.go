package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/relay"
)

func TestRelayOperatorBudgetSQLiteR2(t *testing.T) {
	if testing.Short() || os.Getenv("HOPSESH_RELAY_PLATFORM") != "1" {
		t.Skip("actual operator SQLite/R2 qualification")
	}
	ctx, _, origin, _, client := startSQLiteRelayFixture(t, time.Minute, "DAILY_FRAMES_PER_SPACE:2")
	space := "operator-budget-123456"
	admin := "fixture-admin-secret-with-32-bytes-minimum"
	request := func(path, method, token string, value any) (int, []byte, error) {
		var body []byte
		if value != nil {
			body, _ = json.Marshal(value)
		}
		r, err := http.NewRequestWithContext(ctx, method, origin+path, bytes.NewReader(body))
		if err != nil {
			return 0, nil, err
		}
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Hopsesh-Space", space)
		res, err := client.Do(r)
		if err != nil {
			return 0, nil, err
		}
		defer res.Body.Close()
		var out json.RawMessage
		err = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out, err
	}
	a, err := relay.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	b, err := relay.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	enroll := func(id string) string {
		t.Helper()
		status, body, err := request("/v1/enrollment/register", "POST", admin, map[string]any{"device": id, "ttl": 3600})
		if err != nil || status != 201 {
			t.Fatal("operator fixture enrollment", status, err)
		}
		var c relay.Connection
		if err = json.Unmarshal(body, &c); err != nil {
			t.Fatal(err)
		}
		return c.Token
	}
	ta, tb := enroll(a.Public.ID), enroll(b.Public.ID)
	var wg sync.WaitGroup
	statuses := make(chan int, 12)
	for range 12 {
		wg.Go(func() {
			operation, err := relay.NewOperationID()
			if err != nil {
				statuses <- 0
				return
			}
			e, err := relay.Seal(a, b.Public, space, operation, []byte("disposable encrypted budget fixture"), time.Now(), time.Minute)
			if err != nil {
				statuses <- 0
				return
			}
			status, _, err := request("/v1/messages", "POST", ta, e)
			if err != nil {
				status = 0
			}
			statuses <- status
		})
	}
	wg.Wait()
	close(statuses)
	accepted, blocked := 0, 0
	for s := range statuses {
		if s == 201 {
			accepted++
		}
		if s == 429 {
			blocked++
		}
	}
	if accepted != 2 || blocked != 10 {
		t.Fatalf("actual serialized admission: accepted=%d blocked=%d", accepted, blocked)
	}
	status, body, err := request("/v1/operator/stats", "GET", admin, nil)
	if err != nil || status != 200 {
		t.Fatal("operator stats", status, err)
	}
	var metrics map[string]any
	if err = json.Unmarshal(body, &metrics); err != nil || metrics["admittedFrames"] != float64(2) || metrics["messages"] != float64(2) {
		t.Fatal("operator counts lost accepted traffic", err)
	}
	if bytes.Contains(body, []byte(ta)) || bytes.Contains(body, []byte(a.Public.ID)) {
		t.Fatal("operator metrics exposed routing identity")
	}
	if status, _, _ := request("/v1/operator/stats", "GET", ta, nil); status != 403 {
		t.Fatal("device credential gained operator access")
	}
	for range 2 {
		status, body, err := request("/v1/messages", "GET", tb, nil)
		if err != nil || status != 200 {
			t.Fatal("draining admitted ciphertext", status, err)
		}
		var batch struct {
			Cursor int64 `json:"cursor"`
		}
		if err = json.Unmarshal(body, &batch); err != nil {
			t.Fatal(err)
		}
		if status, _, err = request("/v1/ack", "POST", tb, map[string]any{"cursor": batch.Cursor}); err != nil || status != 200 {
			t.Fatal("draining budget-limited mailbox", status, err)
		}
	}
	status, body, err = request("/v1/operator/stats", "GET", admin, nil)
	if err != nil || status != 200 {
		t.Fatal(err)
	}
	if err = json.Unmarshal(body, &metrics); err != nil || metrics["messages"] != float64(0) || metrics["admittedFrames"] != float64(2) {
		t.Fatal("ack refunded daily traffic budget", err)
	}
}
