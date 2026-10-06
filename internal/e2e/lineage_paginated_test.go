package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func numberCodexRecords(t *testing.T, body string, start int, promote bool) string {
	t.Helper()
	var out strings.Builder
	for i, raw := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		var row map[string]any
		if err := json.Unmarshal([]byte(raw), &row); err != nil {
			t.Fatal(err)
		}
		if promote && i == 0 {
			row["payload"].(map[string]any)["history_mode"] = "paginated"
		}
		row["ordinal"] = start + i
		encoded, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		out.Write(encoded)
		out.WriteByte('\n')
	}
	return out.String()
}

// Only controlled test seeds are promoted, before any receipts or authored hops.
func promoteCodexPaginated(t *testing.T, file string) {
	t.Helper()
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(numberCodexRecords(t, string(body), 0, true)), 0600); err != nil {
		t.Fatal(err)
	}
}

func numberPaginatedTurn(t *testing.T, file, turn string) string {
	t.Helper()
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	var first struct {
		Payload struct {
			Mode string `json:"history_mode"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(rows[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first.Payload.Mode != "paginated" {
		return turn
	}
	return numberCodexRecords(t, turn, len(rows), false)
}

func TestLineagePaginatedOriginalRepeatedAndMultiPartyReturns(t *testing.T) {
	for _, route := range []string{"ABABA", "ABCA", "ABCBCAB"} {
		for mask := 0; mask < 8; mask++ {
			t.Run(fmt.Sprintf("%s/%03b", route, mask), func(t *testing.T) { runLineageRoute(t, route, "codex", mask, "paginated") })
		}
	}
}
