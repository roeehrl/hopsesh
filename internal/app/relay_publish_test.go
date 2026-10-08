package app

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/observe"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestRelayLeaseNeedsFreshLocalEvidenceAndDigestIgnoresOnlyObservationTimes(t *testing.T) {
	now := time.Now().UTC()
	obs := Observation{InventoryComplete: true, Machine: "source", Entries: []Entry{{ObservedAt: now, Live: agent.LiveInfo{State: agent.Live}}}}
	data, _ := json.Marshal(obs)
	source := observe.Snapshot{Epoch: "source-incarnation-123", Sequence: 1, AttemptedAt: now, ObservedAt: now, ExpiresAt: now.Add(90 * time.Second), Data: data}
	grant := relay.Grant{Kind: "device", Methods: []string{"observe"}}
	projected, err := relaySnapshot(source, grant, true, now)
	if err != nil || !projected.ObservedAt.Equal(now) || !projected.ExpiresAt.Equal(now.Add(relay.ObservationLease)) {
		t.Fatal("source remote lease", projected, err)
	}
	for _, mode := range []string{"expired", "failed", "paused"} {
		old := source
		switch mode {
		case "expired":
			old.ExpiresAt = now.Add(-time.Second)
		case "failed":
			old.Error = "scan failed"
		case "paused":
			old.Paused = true
		}
		out, err := relaySnapshot(old, grant, true, now)
		if err != nil || out.ExpiresAt != old.ExpiresAt || out.ObservedAt != old.ObservedAt {
			t.Fatal("unavailable source evidence renewed", mode, out, err)
		}
	}
	digest, err := observationDigest(projected)
	if err != nil {
		t.Fatal(err)
	}
	obs.Entries[0].ObservedAt = now.Add(time.Minute)
	obs.Remotes = []RemoteObservation{{Status: "changed remote"}}
	obs.Problems = []string{"transport health changed"}
	source.Data, _ = json.Marshal(obs)
	source.Sequence++
	source.ObservedAt = now.Add(time.Minute)
	source.ExpiresAt = now.Add(2 * time.Minute)
	same, err := relaySnapshot(source, grant, true, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	next, err := observationDigest(same)
	if err != nil || digest != next {
		t.Fatal("timestamps or peer feedback causes relay echo", err)
	}
	obs.Entries[0].Live.State = agent.Unknown
	source.Data, _ = json.Marshal(obs)
	changed, _ := relaySnapshot(source, grant, true, now.Add(time.Minute))
	next, err = observationDigest(changed)
	if err != nil || digest == next {
		t.Fatal("actual presence change suppressed")
	}
	// Four mutually paired idle devices: one renewal per directed edge, not
	// request+reply every minute. This is idle frame use, not an active-work cap.
	frames := 4 * 3 * int(24*time.Hour/relay.ObservationRenew)
	if frames != 3456 || frames >= 4096 {
		t.Fatal("idle fleet exceeds experimental daily frame allowance", frames)
	}
}
