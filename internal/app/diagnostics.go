package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/observe"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	localruntime "github.com/roeehrl/hopsesh/internal/core/runtime"
	"github.com/roeehrl/hopsesh/internal/version"
)

// RuntimeDiagnostics is deliberately an allowlist of health metadata. It contains
// no raw settings, log contents, local paths, hostnames or conversation evidence.
type RuntimeDiagnostics struct {
	Schema           int                        `json:"schema"`
	Version          string                     `json:"version"`
	OS               string                     `json:"os"`
	Created          time.Time                  `json:"created"`
	Namespace        string                     `json:"namespace"`
	SettingsRevision string                     `json:"settingsRevision,omitempty"`
	SettingsReadable bool                       `json:"settingsReadable"`
	Connected        bool                       `json:"connected"`
	Mode             string                     `json:"mode,omitempty"`
	OwnerVersion     string                     `json:"ownerVersion,omitempty"`
	Protocol         int                        `json:"protocol,omitempty"`
	Login            localruntime.ServiceStatus `json:"login"`
	Observation      DiagnosticObservation      `json:"observation"`
	Relay            DiagnosticRelay            `json:"relay"`
	Logs             DiagnosticLogs             `json:"logs"`
	Scheduler        observe.Metrics            `json:"scheduler"`
}
type DiagnosticObservation struct {
	Sequence       uint64    `json:"sequence"`
	Attempted      time.Time `json:"attempted"`
	Observed       time.Time `json:"observed"`
	Expires        time.Time `json:"expires"`
	Fresh          bool      `json:"fresh"`
	Paused         bool      `json:"paused"`
	Error          bool      `json:"error"`
	Complete       bool      `json:"complete"`
	Sessions       int       `json:"sessions"`
	Agents         int       `json:"agents"`
	Problems       int       `json:"problems"`
	RemoteSources  int       `json:"remoteSources"`
	RemoteFresh    int       `json:"remoteFresh"`
	RemoteFailures int       `json:"remoteFailures"`
	RemoteScanning int       `json:"remoteScanning"`
}
type DiagnosticRelay struct {
	Enabled     bool      `json:"enabled"`
	HealthKnown bool      `json:"healthKnown"`
	Connected   bool      `json:"connected"`
	LastSuccess time.Time `json:"lastSuccess"`
	Error       bool      `json:"error"`
	Rejected    uint64    `json:"rejected"`
}
type DiagnosticLogs struct {
	Files  int   `json:"files"`
	Bytes  int64 `json:"bytes"`
	Unsafe bool  `json:"unsafe"`
}

func summarizeObservation(s observe.Snapshot, now time.Time) DiagnosticObservation {
	d := DiagnosticObservation{Sequence: s.Sequence, Attempted: s.AttemptedAt, Observed: s.ObservedAt, Expires: s.ExpiresAt, Fresh: s.Fresh(now), Paused: s.Paused, Error: s.Error != ""}
	var o Observation
	if len(s.Data) > 0 && json.Unmarshal(s.Data, &o) == nil {
		d.Complete = o.InventoryComplete
		d.Sessions = len(o.Entries)
		d.Agents = len(o.Agents)
		d.Problems = len(o.Problems)
		d.RemoteSources = len(o.Remotes)
		for _, remote := range o.Remotes {
			if remote.Snapshot.Fresh(now) {
				d.RemoteFresh++
			}
			if remote.Error != "" {
				d.RemoteFailures++
			}
			if remote.Phase == "scanning" || remote.Phase == "queued" {
				d.RemoteScanning++
			}
		}
	}
	return d
}

func (a *App) Diagnostics(ctx context.Context) (RuntimeDiagnostics, error) {
	n, err := localruntime.NewNamespace(config.Dir(), a.StateDir)
	if err != nil {
		return RuntimeDiagnostics{}, err
	}
	now := time.Now().UTC()
	d := RuntimeDiagnostics{Schema: 1, Version: version.Version, OS: runtime.GOOS, Created: now, Namespace: n.ID}
	if cfg, e := config.Load(); e == nil {
		d.SettingsReadable = true
		d.SettingsRevision = cfg.Revision()
		d.Relay.Enabled = cfg.Relay.Enabled
	}
	c := localruntime.Client{Namespace: n}
	probe, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var owner localruntime.Status
	if c.Call(probe, "status", nil, &owner) == nil {
		d.Connected, d.Mode, d.OwnerVersion, d.Protocol = true, owner.Mode, owner.Version, owner.Protocol
		var s observe.Snapshot
		_ = c.Call(probe, "metrics", nil, &d.Scheduler)
		if c.Call(probe, "snapshot", nil, &s) == nil {
			d.Observation = summarizeObservation(s, now)
		}
		var health relay.Health
		if c.Call(probe, "relay.status", nil, &health) == nil {
			d.Relay.HealthKnown = true
			d.Relay.Connected = health.Connected
			d.Relay.LastSuccess = health.LastSuccess
			d.Relay.Error = health.Error != ""
			d.Relay.Rejected = health.Rejected
		}
	}
	service, cancelService := context.WithTimeout(ctx, 3*time.Second)
	defer cancelService()
	if p, e := localruntime.CurrentServicePlan(n); e == nil {
		d.Login = p.Status(service)
	} else {
		d.Login.Error = "Login service plan is unavailable"
	}
	for _, name := range []string{"runtime.log", "runtime.log.1", "runtime.log.2", "runtime.log.3"} {
		st, e := os.Lstat(filepath.Join(a.StateDir, name))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil || !st.Mode().IsRegular() || st.Size() > localruntime.LogBytes {
			d.Logs.Unsafe = true
			continue
		}
		d.Logs.Files++
		d.Logs.Bytes += st.Size()
	}
	return d, nil
}
