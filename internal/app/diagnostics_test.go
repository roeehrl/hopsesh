package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/observe"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestDiagnosticObservationContainsOnlyHealthMetadata(t *testing.T) {
	secret := "SENSITIVE-title-path-account-transcript-token"
	now := time.Now()
	body, _ := json.Marshal(Observation{Machine: secret, Endpoint: secret, InventoryComplete: false, Problems: []string{secret}, WatchRoots: []string{secret}, Entries: []Entry{{Session: agent.Summary{Title: secret, Path: secret, CWD: secret}}}})
	d := summarizeObservation(observe.Snapshot{Sequence: 7, ObservedAt: now, ExpiresAt: now.Add(time.Minute), Error: secret, Data: body}, now)
	b, _ := json.Marshal(d)
	if strings.Contains(string(b), secret) || !d.Error || d.Fresh || d.Complete || d.Sessions != 1 || d.Problems != 1 {
		t.Fatalf("diagnostic privacy/freshness: %s", b)
	}
}
