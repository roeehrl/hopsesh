package app

import (
	"github.com/roeehrl/hopsesh/internal/core/relay"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Pairing shares the approved inventory and stable account binding IDs, never
// cached account labels, email, profile tags, process tables or watcher paths.
func relayObservation(obs Observation, grant relay.Grant) Observation {
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
		e.Live.PID = 0
		e.Live.Procs = nil
		entries = append(entries, e)
	}
	obs.Entries = entries
	return obs
}
