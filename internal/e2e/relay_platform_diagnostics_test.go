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
// logger is empty. Query bounded spans plus the numeric HTTP status; never return
// arbitrary attributes, headers, console messages, bodies, records or credentials.
func relayPlatformDiagnostics(ctx context.Context, origin string, client *http.Client) ([]byte, error) {
	const query = `SELECT substr(service,1,80) AS service, substr(kind,1,80) AS kind, substr(outcome,1,80) AS outcome, duration_ms, substr(error,1,1024) AS error, CASE WHEN json_type(attributes, '$."http.response.status_code"') = 'integer' THEN json_extract(attributes, '$."http.response.status_code"') END AS http_status FROM spans WHERE kind IN ('http','fetch') OR error IS NOT NULL ORDER BY (error IS NOT NULL) DESC, start_ms DESC LIMIT 32`
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
	if !reply.Success || len(reply.Result.Columns) != 6 || len(reply.Result.Rows) > 32 {
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
	if !bytes.Contains(result, []byte(`"http_status"`)) || !bytes.Contains(result, []byte(`,200]`)) {
		t.Fatal("readiness HTTP status was not captured", string(result))
	}
	t.Logf("bounded local spans: %s", result)
}
