package host

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestUnknownLockHoldersAreNotReportedFree(t *testing.T) {
	p := filepath.Join(t.TempDir(), "thread.lock")
	l := localLocks{}
	states, err := l.Probe(t.Context(), []string{p})
	if err != nil || states[p] != agent.LockUnknown {
		t.Fatalf("Windows lock probe must remain unknown: %v, %v", states, err)
	}
	holders, err := l.Holders(t.Context(), []string{p})
	if !errors.Is(err, agent.ErrUnsupported) || holders != nil {
		t.Fatalf("unsupported lock lookup reported as free: holders=%v, error=%v", holders, err)
	}
}
