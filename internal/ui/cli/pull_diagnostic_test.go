package cli

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestMissingSessionPreservesScanFailure(t *testing.T) {
	missing := fmt.Errorf("%w: session-123", agent.ErrNotFound)
	inv := &app.Inventory{Machines: []*app.Machine{
		{Name: "local", Status: app.StatusOK, Error: "ignore healthy machine"},
		{Name: "mac", Status: app.StatusError, Error: "SFTP: connection closed", Hint: "Enable the SFTP subsystem"},
		{Name: "linux", Status: app.StatusAuth, Error: "Permission denied (publickey)"},
	}}
	got := explainMissing(missing, inv)
	if !errors.Is(got, agent.ErrNotFound) {
		t.Fatalf("lost original error identity: %v", got)
	}
	for _, want := range []string{"session-123", "mac: error", "SFTP: connection closed", "Enable the SFTP subsystem", "linux: auth", "Permission denied (publickey)"} {
		if !strings.Contains(got.Error(), want) {
			t.Errorf("missing diagnostic %q in %v", want, got)
		}
	}
	if strings.Contains(got.Error(), "ignore healthy") {
		t.Fatalf("included healthy machine: %v", got)
	}
	other := errors.New("invalid target")
	if explainMissing(other, inv) != other || explainMissing(missing, &app.Inventory{}) != missing {
		t.Fatal("changed an unrelated error or a complete empty scan")
	}
}

func TestMissingSessionBoundsAndQuotesRemoteDiagnostics(t *testing.T) {
	inv := &app.Inventory{Machines: []*app.Machine{{Name: "mac", Status: app.StatusError,
		Error: "SFTP:\n\x1b[2J" + strings.Repeat("x", 10000), Hint: strings.Repeat("y", 10000)}}}
	got := explainMissing(agent.ErrNotFound, inv).Error()
	if len(got) > 2500 || strings.ContainsAny(got, "\n\x1b") || !strings.Contains(got, "SFTP:") {
		t.Fatalf("unbounded or unsafe diagnostic (%d bytes): %.100q", len(got), got)
	}
}
