package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/observe"
	"github.com/roeehrl/hopsesh/internal/core/relay"
)

type publishedObservation struct {
	digest    [32]byte
	observed  time.Time
	attempted time.Time
	problem   string
}

// Ignore only collection timestamps when deciding whether content changed.
// Source state, completeness and authorization changes must still publish.
func observationDigest(snap observe.Snapshot) ([32]byte, error) {
	var obs Observation
	if err := json.Unmarshal(snap.Data, &obs); err != nil {
		return [32]byte{}, err
	}
	for i := range obs.Entries {
		obs.Entries[i].ObservedAt = time.Time{}
	}
	body, err := json.Marshal(struct {
		Data   Observation
		Paused bool
		Error  string
	}{obs, snap.Paused, snap.Error})
	return sha256.Sum256(body), err
}

// One subscriber and one event-driven coalescing timer per relay owner. There
// is no extra idle timer: renewal is driven by the shared collector's successful
// observations. Remote rows and relay health are excluded from the projection,
// preventing peers' observations from echoing back to one another.
func publishRelayObservations(ctx context.Context, engine *observe.Engine, service *relay.Service) {
	updates, unsubscribe := engine.Subscribe()
	defer unsubscribe()
	last := map[string]publishedObservation{}
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	var due <-chan time.Time
	var attempted time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-updates:
			if !ok {
				return
			}
			if wait := time.Until(attempted.Add(10 * time.Second)); wait > 0 {
				if due == nil {
					timer.Reset(wait)
					due = timer.C
				}
				continue
			}
		case <-due:
		}
		timer.Stop()
		due = nil
		attempted = time.Now()
		cfg, err := config.Load()
		if err != nil || !cfg.Relay.Enabled {
			continue
		}
		grants, err := service.Processor.Store.Grants()
		if err != nil {
			service.ObservationProblem(err.Error())
			continue
		}
		wanted := map[string]bool{}
		for _, grant := range grants {
			if grant.Kind != "device" || !grant.Allows("observe", time.Now()) {
				continue
			}
			wanted[grant.Peer.ID] = true
			old := last[grant.Peer.ID]
			// Failed sends retry from the newest source, at most once a minute.
			if !old.attempted.IsZero() && time.Since(old.attempted) < time.Minute {
				continue
			}
			snapshot, err := relaySnapshot(engine.Latest(), grant, cfg.Peer.Receive, time.Now())
			if err != nil {
				continue
			}
			digest, err := observationDigest(snapshot)
			if err != nil {
				continue
			}
			if digest == old.digest && snapshot.ObservedAt.Sub(old.observed) < relay.ObservationRenew {
				continue
			}
			child, cancel := context.WithTimeout(ctx, 5*time.Second)
			err = service.PublishObservation(child, grant, snapshot)
			cancel()
			service.ObservationDelivery(err)
			if err != nil {
				old.attempted = time.Now()
				old.problem = err.Error()
			} else {
				old = publishedObservation{digest: digest, observed: snapshot.ObservedAt}
			}
			last[grant.Peer.ID] = old
		}
		problem := ""
		for id, state := range last {
			if !wanted[id] {
				delete(last, id)
			} else if state.problem != "" {
				problem = state.problem
			}
		}
		service.ObservationProblem(problem)
	}
}
