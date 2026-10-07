package gui

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
)

// MachineScan is separate from the last result: refreshing never erases what was
// previously found. Times are for presentation and scheduling, not transfer ordering.
type MachineScan struct {
	Phase    string `json:"phase"` // queued, scanning, done
	Started  string `json:"started,omitempty"`
	Finished string `json:"finished,omitempty"`
	Error    string `json:"error,omitempty"`
}

func scanProblem(m *app.Machine) string {
	var problems []string
	if m.Error != "" {
		problems = append(problems, m.Error)
	}
	for _, agent := range m.Agents {
		if agent.Error != "" {
			problems = append(problems, agent.Name+": "+agent.Error)
		}
	}
	return strings.Join(problems, "; ")
}

const MachineScanEvent = "hopsesh:machine-scan"

// MachineScans supplies a replayable snapshot without network discovery.
func (a *App) MachineScans() map[string]MachineScan {
	a.mu.Lock()
	defer a.mu.Unlock()
	return maps.Clone(a.scans)
}

func (a *App) scanPhase(name, phase, problem string) {
	a.mu.Lock()
	if a.scans == nil {
		a.scans = map[string]MachineScan{}
	}
	s := a.scans[name]
	s.Phase, s.Error = phase, problem
	if phase == "scanning" {
		s.Started = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if phase == "done" {
		s.Finished = time.Now().UTC().Format(time.RFC3339Nano)
	}
	a.scans[name] = s
	a.mu.Unlock()
	a.emit(MachineScanEvent, nil)
}

// ScanMachine reads just one allowed machine, merges its result into the inventory,
// and leaves all other connections and cloud results in place.
func (a *App) ScanMachine(name string) error {
	a.mu.Lock()
	h := a.core.Cfg.FindHost(name)
	if h == nil || !h.Allowed {
		a.mu.Unlock()
		return fmt.Errorf("machine %q is not enabled", name)
	}
	if s := a.scans[name]; s.Phase == "queued" || s.Phase == "scanning" {
		a.mu.Unlock()
		return nil
	}
	destination := h.Destination
	relayID := h.RelayID
	if a.scans == nil {
		a.scans = map[string]MachineScan{}
	}
	s := a.scans[name]
	s.Phase, s.Error = "queued", ""
	a.scans[name] = s
	a.mu.Unlock()
	a.emit(MachineScanEvent, nil)
	a.scanMu.Lock()
	defer a.scanMu.Unlock()
	core := a.snapshot()
	h = core.Cfg.FindHost(name)
	if h == nil || !h.Allowed || h.Destination != destination || h.RelayID != relayID {
		a.scanPhase(name, "done", "Machine settings changed before the scan started. Scan again.")
		return nil
	}
	a.scanPhase(name, "scanning", "")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	a.backend.mu.Lock()
	shared, client := a.backend.cancel != nil, a.backend.client
	a.backend.mu.Unlock()
	var fresh *app.Inventory
	if shared && !h.UsesPassword() {
		var observation app.RemoteObservation
		if err := client.Call(ctx, "machines.refresh", struct {
			Name string `json:"name"`
		}{name}, &observation); err != nil {
			a.scanPhase(name, "done", err.Error())
			return err
		}
		fresh = core.RemoteInventory(ctx, observation)
	} else {
		fresh = core.Scan(ctx, app.ScanOptions{Hosts: []string{name}, NoLocal: true, ForceAccounts: true})
	}
	problem := ""
	if m := fresh.Machine(name); m != nil {
		problem = scanProblem(m)
	} else {
		problem = "The scan returned no result. Try again."
	}
	a.mu.Lock()
	// Removing or editing a machine while SSH is running must not resurrect it.
	h = a.core.Cfg.FindHost(name)
	if h != nil && h.Allowed && h.Destination == destination && h.RelayID == relayID {
		if a.inv == nil {
			a.inv = &app.Inventory{}
		}
		if old := a.inv.Machine(name); old != nil && old.Host() != nil {
			old.Host().Close()
		}
		// Readers may still hold the previous inventory snapshot.
		next := *a.inv
		next.Machines = slices.DeleteFunc(slices.Clone(a.inv.Machines), func(m *app.Machine) bool { return m.Name == name })
		next.Entries = slices.DeleteFunc(slices.Clone(a.inv.Entries), func(e app.Entry) bool { return e.Machine == name })
		next.Machines = append(next.Machines, fresh.Machines...)
		next.Entries = append(next.Entries, fresh.Entries...)
		a.inv = &next
	} else {
		fresh.Close()
	}
	a.mu.Unlock()
	a.scanPhase(name, "done", problem)
	return nil
}
