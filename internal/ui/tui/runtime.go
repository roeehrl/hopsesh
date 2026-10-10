package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/observe"
	localruntime "github.com/roeehrl/hopsesh/internal/core/runtime"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

type runtimeSnapshot struct{ observe.Snapshot }
type runtimeDisconnected struct{ err error }
type runtimeScanError struct{ err error }

func watchRuntime(ctx context.Context, c localruntime.Client, send func(tea.Msg)) {
	delay := time.Second
	for {
		err := c.Watch(ctx, func(s observe.Snapshot) error { delay = time.Second; send(runtimeSnapshot{s}); return nil })
		if ctx.Err() != nil {
			return
		}
		send(runtimeDisconnected{err})
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay = min(30*time.Second, delay*2)
	}
}

func sharedLocal(ctx context.Context, c localruntime.Client, refresh bool) (app.Observation, error) {
	var old observe.Snapshot
	method := "snapshot"
	if refresh {
		method = "refresh"
	}
	if err := c.Call(ctx, method, nil, &old); err != nil {
		return app.Observation{}, err
	}
	s := old
	if refresh || !old.Fresh(time.Now()) {
		if !refresh {
			if err := c.Call(ctx, "refresh", nil, &old); err != nil {
				return app.Observation{}, err
			}
		}
		received := errors.New("shared observation received")
		err := c.Watch(ctx, func(next observe.Snapshot) error {
			if next.Epoch == old.Epoch && next.Sequence <= old.Sequence {
				return nil
			}
			s = next
			return received
		})
		if !errors.Is(err, received) {
			return app.Observation{}, err
		}
	}
	if !s.Fresh(time.Now()) {
		return app.Observation{}, fmt.Errorf("shared local observation is paused or unavailable: %s", s.Error)
	}
	var o app.Observation
	err := json.Unmarshal(s.Data, &o)
	return o, err
}

func (m *model) invalidateRuntime(err error) {
	if m.inv == nil {
		return
	}
	for i := range m.inv.Entries {
		e := &m.inv.Entries[i]
		if e.Machine == app.LocalName() && !e.Location.IsCloud() {
			e.Live = agent.LiveInfo{State: agent.Unknown}
			e.ObservedAt = time.Time{}
		}
	}
	if err != nil {
		m.notice = "Local observation unavailable: " + err.Error()
	} else {
		m.notice = "Local observation is paused or expired."
	}
	m.rebuildRuntimeRows()
}

func (m *model) applyRuntime(s observe.Snapshot) {
	m.latestRuntime = nil
	if !s.Fresh(time.Now()) {
		m.invalidateRuntime(nil)
		return
	}
	var o app.Observation
	if json.Unmarshal(s.Data, &o) != nil {
		return
	}
	if m.inv == nil {
		return
	}
	if cfg, err := config.Load(); err == nil {
		m.deps.App.Cfg = cfg
	}
	fresh := m.deps.App.ObservationInventory(context.Background(), o)
	fresh.Clouds, fresh.Adopted, fresh.Waiting = m.inv.Clouds, m.inv.Adopted, m.inv.Waiting
	m.deps.App.MergeRemoteObservations(context.Background(), fresh, m.inv, o.Remotes, nil)
	for _, machine := range m.inv.Machines {
		if machine.Local && machine.Host() != nil {
			machine.Host().Close()
		}
	}
	for _, e := range m.inv.Entries {
		if e.Machine != o.Machine || e.Location.IsCloud() {
			if e.Location.IsCloud() {
				fresh.Entries = append(fresh.Entries, e)
			}
			continue
		}
		if !o.InventoryComplete {
			found := false
			for _, current := range o.Entries {
				if current.Session.Key == e.Session.Key {
					found = true
					break
				}
			}
			if !found {
				e.Live = agent.LiveInfo{State: agent.Unknown}
				e.ObservedAt = time.Time{}
				fresh.Entries = append(fresh.Entries, e)
			}
		}
	}
	m.inv = fresh
	m.notice = ""
	m.rebuildRuntimeRows()
}

func (m *model) rebuildRuntimeRows() {
	var selected *app.Entry
	if m.cursor < len(m.rows) && m.rows[m.cursor].item != nil {
		e := m.rows[m.cursor].item.Entry
		selected = &e
	}
	offset := m.offset
	m.buildRows()
	if selected != nil {
		index := -1
		for i, r := range m.rows {
			if r.item != nil && r.item.Entry.Machine == selected.Machine && r.item.Entry.Location == selected.Location && r.item.Entry.Session.Key == selected.Session.Key {
				index = i
				break
			}
		}
		if index < 0 {
			index = m.registeredDefaultRow(*selected)
		}
		if index >= 0 {
			m.cursor, m.offset = index, offset
		}
	}
	m.offset = min(m.offset, max(0, len(m.rows)-m.listHeight()))
	m.scroll()
}

// First-time default registration changes a browsing key, not the native file.
// Resolve only that local file, with a single current default binding. Plans and
// transfers still perform their own fresh scope checks.
func (m *model) registeredDefaultRow(selected app.Entry) int {
	local := m.inv.Machine(selected.Machine)
	if local == nil || !local.Local || selected.Location.IsCloud() || selected.Session.Path == "" || selected.Session.Key.Profile != "" || selected.Profile != nil {
		return -1
	}
	var key agent.SessionKey
	for _, e := range m.inv.Entries {
		p := e.Profile
		if e.Machine != selected.Machine || e.Location != selected.Location || e.Agent != selected.Agent || e.Session.Path != selected.Session.Path || e.Session.Key.Session != selected.Session.Key.Session || e.Session.Key.Agent != selected.Session.Key.Agent || e.Cached && !m.inv.Discovering || p == nil || !p.Default || p.ID == "" || p.Endpoint == "" || p.Root == "" || p.Agent != e.Agent || e.Session.Key.Profile != p.ID {
			continue
		}
		if key.Profile != "" && key != e.Session.Key {
			return -1
		}
		key = e.Session.Key
	}
	if key.Profile == "" {
		return -1
	}
	for i, r := range m.rows {
		if r.item != nil && r.item.Entry.Machine == selected.Machine && r.item.Entry.Location == selected.Location && r.item.Entry.Session.Key == key && r.item.Entry.Session.Path == selected.Session.Path {
			return i
		}
	}
	return -1
}
