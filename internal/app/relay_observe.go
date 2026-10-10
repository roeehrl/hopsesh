package app

import (
	"encoding/json"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/observe"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func relaySnapshot(snap observe.Snapshot, grant relay.Grant, receive bool, now time.Time) (observe.Snapshot, error) {
	var obs Observation
	if err := json.Unmarshal(snap.Data, &obs); err != nil {
		return observe.Snapshot{}, err
	}
	obs = relayObservation(obs, grant)
	obs.Receive = receive && grant.Allows("plan", now) && grant.Allows("apply", now) && len(grant.Roots) > 0
	// Only a currently fresh source collection can issue a remote lease. Cached
	// failed/paused evidence keeps its original expiry and cannot be refreshed.
	if snap.Fresh(now) {
		snap.ExpiresAt = snap.ObservedAt.Add(relay.ObservationLease)
	}
	if grant.Expires != 0 && snap.ExpiresAt.After(time.Unix(grant.Expires, 0)) {
		snap.ExpiresAt = time.Unix(grant.Expires, 0)
	}
	var err error
	snap.Data, err = json.Marshal(obs)
	return snap, err
}

// Pairing shares the approved inventory and stable account binding IDs, never
// conversation previews, cached account labels, email, profile tags, process
// tables or watcher paths. Preview/export requires a separate authorized call.
func relayObservation(obs Observation, grant relay.Grant) Observation {
	obs.Remotes = nil
	obs.Processes, obs.WatchRoots = nil, nil
	obs.Problems = nil
	profile := func(p *agent.RuntimeProfile) *agent.RuntimeProfile {
		if p == nil {
			return nil
		}
		copy := *p
		copy.Name, copy.Tags, copy.Account, copy.Error = "", nil, nil, ""
		return &copy
	}
	for i := range obs.Agents {
		obs.Agents[i].Install.Profile = profile(obs.Agents[i].Install.Profile)
	}
	entries := make([]Entry, 0, len(obs.Entries))
	for _, e := range obs.Entries {
		if len(grant.Roots) > 0 && withinRelayRoots(e.Session.CWD, grant.Roots) != nil {
			continue
		}
		e.Profile = profile(e.Profile)
		e.Session.LastPrompt = ""
		e.Live.PID = 0
		e.Live.Procs = nil
		entries = append(entries, e)
	}
	obs.Entries = entries
	return obs
}
