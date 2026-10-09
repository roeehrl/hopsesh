package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

// Local Explorer captures workerd binding failures even when Wrangler's terminal
// logger is empty. Query only bounded span diagnostics; never request attributes,
// headers, console messages, bodies, stored records or credentials.
func relayPlatformDiagnostics(ctx context.Context, origin string, client *http.Client) ([]byte, error) {
	const query = `SELECT substr(service,1,80) AS service, substr(kind,1,80) AS kind, substr(outcome,1,80) AS outcome, duration_ms, substr(error,1,1024) AS error FROM spans ORDER BY (error IS NOT NULL) DESC, start_ms DESC LIMIT 32`
	body, _ := json.Marshal(map[string]string{"sql": query})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/cdn-cgi/local/explorer/api/local/observability/query", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("local span query HTTP %d", res.StatusCode)
	}
	var reply struct {
		Success bool `json:"success"`
		Result  struct {
			Columns []string `json:"columns"`
			Rows    [][]any  `json:"rows"`
		} `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&reply); err != nil {
		return nil, err
	}
	if !reply.Success || len(reply.Result.Columns) != 5 || len(reply.Result.Rows) > 32 {
		return nil, fmt.Errorf("invalid local span query result")
	}
	return json.Marshal(reply.Result)
}

func TestRelayPlatformDiagnosticsSQLiteR2(t *testing.T) {
	if testing.Short() || os.Getenv("HOPSESH_RELAY_PLATFORM") != "1" {
		t.Skip("requires disposable SQLite/R2 platform")
	}
	ctx, _, origin, _, client := startSQLiteRelayFixture(t, time.Minute)
	result, err := relayPlatformDiagnostics(ctx, origin, client)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(result, []byte(`"hopsesh-relay-experimental"`)) {
		t.Fatal("readiness request was not captured by local tracing", string(result))
	}
	t.Logf("bounded local spans: %s", result)
}
