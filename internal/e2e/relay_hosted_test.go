package e2e

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/localstate"
)

// Explicit opt-in only: this runs real native endpoints against the isolated
// deployed service. The operator secret stays in a bounded private file and is
// sent only to the fixed HTTPS enrollment route, with redirects refused.
func TestRelayHostedStaging(t *testing.T) {
	if testing.Short() || os.Getenv("HOPSESH_HOSTED_RELAY") != "1" {
		t.Skip("explicit hosted staging qualification")
	}
	admin, err := localstate.ReadPrivateFile(os.Getenv("HOPSESH_HOSTED_RELAY_ADMIN_FILE"), 256)
	if err != nil || len(admin) < 32 || strings.ContainsAny(string(admin), "\r\n") {
		t.Fatal("hosted qualification requires a bounded private operator secret file")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	f := newRelayFleetWithAdmission(t, ctx, buildHopsesh(t), "https://relay.hopsesh.codonic.dev", "", client, string(admin), true)
	current, sourceAgent := f.seed(t, "claude"), "claude"
	route := "ABCA"
	var sentinels []string
	for hop := 0; hop < len(route)-1; hop++ {
		from, to := route[hop], route[hop+1]
		sentinel := fmt.Sprintf("HOSTED-DISPOSABLE-WORK-%d", hop)
		sentinels = append(sentinels, sentinel)
		f.appendWork(t, current, sourceAgent, sentinel)
		target, mode := "codex", "push"
		if hop%2 == 1 {
			target, mode = "claude", "pull"
		}
		if to == 'A' {
			target = "claude" // Return to the original machine and agent.
		}
		operation := fmt.Sprintf("hosted-route-hop-%d-12345678", hop)
		previous := current
		current = f.transfer(t, from, to, current, target, mode, false, operation)
		f.assertText(t, to, current, target, sentinels, nil)
		if hop == 1 {
			before, err := os.ReadFile(current.Path)
			if err != nil {
				t.Fatal(err)
			}
			f.stop[to]()
			f.start(t, to)
			repeated := f.transfer(t, from, to, previous, target, mode, false, operation)
			after, err := os.ReadFile(repeated.Path)
			if err != nil || !bytes.Equal(before, after) || repeated.Key != current.Key {
				t.Fatal("hosted owner restart retry duplicated the native outcome", err)
			}
		}
		sourceAgent = target
	}
	graph, err := lineage.Read(host.LocalFS(), current.Path)
	if err != nil || graph == nil {
		t.Fatal("hosted route lost lineage", err)
	}
	if got := graph.Journey(); got.Transfers != 3 || got.RoundTrips != 1 {
		t.Fatal("hosted route lost transfer or round-trip count", got)
	}
	original := current
	child := f.transfer(t, 'A', 'B', original, "claude", "push", true, "hosted-independent-fork-12345678")
	f.appendWork(t, child, "claude", "HOSTED-FORK-ONLY")
	child = f.transfer(t, 'B', 'C', child, "codex", "pull", false, "hosted-fork-travel-123456789")
	f.assertText(t, 'C', child, "codex", append(sentinels, "HOSTED-FORK-ONLY"), []string{"HOSTED-ORIGINAL-ONLY"})
	f.appendWork(t, original, sourceAgent, "HOSTED-ORIGINAL-ONLY")
	moved := f.transfer(t, 'A', 'C', original, "claude", "push", false, "hosted-original-travel-12345678")
	f.assertText(t, 'C', moved, "claude", append(sentinels, "HOSTED-ORIGINAL-ONLY"), []string{"HOSTED-FORK-ONLY"})
	if f.lastResult.Journal == "" {
		t.Fatal("hosted native transfer has no undo journal")
	}
	f.run(t, f.homes['A'], "undo", f.lastResult.Journal, "--yes", "--json")
	if _, err = os.Stat(moved.Path); !os.IsNotExist(err) {
		t.Fatal("hosted peer undo left the newly written native session", err)
	}
	f.assertText(t, 'C', child, "codex", []string{"HOSTED-FORK-ONLY"}, []string{"HOSTED-ORIGINAL-ONLY"})
}
