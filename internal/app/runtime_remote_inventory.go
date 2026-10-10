package app

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// MergeRemoteObservations installs owner evidence while preserving missing rows
// from incomplete or failed attempts. Complete empty evidence can remove rows.
// Explicit scans newer than a scheduled attempt remain authoritative until a
// later source attempt. Cached SSH handles are reused only with identical facts.
func (a *App) MergeRemoteObservations(ctx context.Context, next, previous *Inventory, states []RemoteObservation, newerThan map[string]time.Time) {
	byName := map[string]RemoteObservation{}
	for _, s := range states {
		byName[s.Binding.Name] = s
	}
	for _, h := range a.Cfg.Hosts {
		if !h.Allowed {
			continue
		}
		var old *Machine
		var oldEntries []Entry
		if previous != nil {
			old = previous.Machine(h.Name)
			for _, e := range previous.Entries {
				if e.Machine == h.Name && !e.Location.IsCloud() {
					oldEntries = append(oldEntries, e)
				}
			}
		}
		state, ok := byName[h.Name]
		if !ok || state.Binding != h {
			continue
		}
		fresh := a.RemoteInventory(ctx, state)
		m := fresh.Machine(h.Name)
		if old != nil && (h.RelayID == "" || m.Status == StatusOK) && ((state.Phase != "done" && len(state.Snapshot.Data) == 0) || newerThan[h.Name].After(state.Started)) {
			fresh.Close()
			next.Machines = append(next.Machines, old)
			next.Entries = append(next.Entries, oldEntries...)
			continue
		}
		if old != nil && old.host != nil && m.host != nil && reflect.DeepEqual(old.host.Facts, m.host.Facts) {
			m.host.Close()
			m.host = old.host
		} else if old != nil && old.host != nil {
			old.host.Close()
		}
		next.Machines = append(next.Machines, fresh.Machines...)
		next.Entries = append(next.Entries, fresh.Entries...)
		var obs Observation
		complete := m.Status == StatusOK && state.Snapshot.Fresh(time.Now()) && json.Unmarshal(state.Snapshot.Data, &obs) == nil && obs.InventoryComplete
		if !complete {
			for _, e := range oldEntries {
				if slices.ContainsFunc(fresh.Entries, func(current Entry) bool { return current.Session.Key == e.Session.Key }) {
					continue
				}
				e.Live = agent.LiveInfo{State: agent.Unknown}
				e.ObservedAt = time.Time{}
				next.Entries = append(next.Entries, e)
			}
		}
	}
}
