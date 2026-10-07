package gui

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/observe"
	localruntime "github.com/roeehrl/hopsesh/internal/core/runtime"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"slices"
	"strings"
	"sync"
	"time"
)

const RuntimeEvent = "hopsesh:runtime"

type runtimeLink struct {
	mu              sync.Mutex
	owner           *localruntime.Host
	client          localruntime.Client
	cancel          context.CancelFunc
	done            chan struct{}
	status          localruntime.Status
	snapshot        observe.Snapshot
	problem         string
	appliedEpoch    string
	appliedSequence uint64
}
type RuntimeDTO struct {
	Connected bool                `json:"connected"`
	Owner     localruntime.Status `json:"owner"`
	Snapshot  observe.Snapshot    `json:"snapshot"`
	Error     string              `json:"error,omitempty"`
}

func (a *App) RuntimeStatus() RuntimeDTO {
	a.backend.mu.Lock()
	defer a.backend.mu.Unlock()
	snapshot := a.backend.snapshot
	snapshot.Data = nil
	return RuntimeDTO{Connected: a.backend.cancel != nil && a.backend.problem == "", Owner: a.backend.status, Snapshot: snapshot, Error: a.backend.problem}
}
func (a *App) connectRuntime() error {
	core := a.snapshot()
	a.backend.mu.Lock()
	defer a.backend.mu.Unlock()
	if a.backend.cancel != nil {
		return nil
	}
	n, err := localruntime.NewNamespace(config.Dir(), core.StateDir)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	owner, err := core.StartRuntime(ctx, "desktop", func() error {
		return errors.New("the desktop owns this runtime; finish transfers and quit the app to stop it safely")
	})
	if err != nil && !errors.Is(err, localruntime.ErrOwned) {
		cancel()
		return err
	}
	client := localruntime.Client{Namespace: n}
	var status localruntime.Status
	probe, done := context.WithTimeout(ctx, 5*time.Second)
	err = client.Call(probe, "status", nil, &status)
	done()
	if err != nil {
		cancel()
		if owner != nil {
			owner.Close()
		}
		return err
	}
	a.backend.owner, a.backend.client, a.backend.cancel, a.backend.status = owner, client, cancel, status
	a.backend.done = make(chan struct{})
	ended := a.backend.done
	go func() {
		defer close(ended)
		delay := time.Second
		for {
			err := client.Watch(ctx, func(s observe.Snapshot) error { a.acceptRuntimeSnapshot(s); return nil })
			if ctx.Err() != nil {
				return
			}
			a.backend.mu.Lock()
			a.backend.problem = err.Error()
			a.backend.snapshot.ExpiresAt = time.Time{}
			a.backend.mu.Unlock()
			a.emit(RuntimeEvent, nil)
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			delay = min(30*time.Second, delay*2)
			var status localruntime.Status
			if err = client.Call(ctx, "status", nil, &status); err == nil {
				a.backend.mu.Lock()
				a.backend.status = status
				a.backend.mu.Unlock()
				delay = time.Second
			}
		}
	}()
	return nil
}
func (a *App) stopRuntime() {
	a.backend.mu.Lock()
	cancel, done, owner := a.backend.cancel, a.backend.done, a.backend.owner
	a.backend.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	if owner != nil {
		owner.Close()
	}
}
func (a *App) acceptRuntimeSnapshot(s observe.Snapshot) {
	a.backend.mu.Lock()
	a.backend.snapshot = s
	a.backend.problem = s.Error
	a.backend.mu.Unlock()
	if !s.Fresh(time.Now()) {
		a.quick.mu.Lock()
		a.quick.err = s.Error
		if s.Paused {
			a.quick.err = "Observation paused while this computer is asleep or locked."
		}
		a.quick.mu.Unlock()
		a.emit(RuntimeEvent, nil)
		a.emit(QuickEvent, nil)
		return
	}
	var out app.Observation
	if err := json.Unmarshal(s.Data, &out); err != nil {
		return
	}
	a.scanMu.Lock()
	defer a.scanMu.Unlock()
	a.backend.mu.Lock()
	if a.backend.appliedEpoch == s.Epoch && a.backend.appliedSequence >= s.Sequence {
		a.backend.mu.Unlock()
		return
	}
	a.backend.appliedEpoch, a.backend.appliedSequence = s.Epoch, s.Sequence
	a.backend.mu.Unlock()
	core := a.snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fresh := core.ObservationInventory(ctx, out)
	a.mu.Lock()
	old := a.inv
	if old != nil {
		fresh.Clouds, fresh.Adopted, fresh.Waiting = old.Clouds, old.Adopted, old.Waiting
		for _, m := range old.Machines {
			if !m.Local {
				fresh.Machines = append(fresh.Machines, m)
			} else if m.Host() != nil {
				m.Host().Close()
			}
		}
		for _, e := range old.Entries {
			if e.Machine != out.Machine || e.Location.IsCloud() {
				fresh.Entries = append(fresh.Entries, e)
				continue
			}
			if !out.InventoryComplete && !slices.ContainsFunc(out.Entries, func(current app.Entry) bool { return current.Session.Key == e.Session.Key }) {
				e.Live = agent.LiveInfo{State: agent.Unknown}
				e.ObservedAt = time.Time{}
				fresh.Entries = append(fresh.Entries, e)
			}
		}
	}
	a.inv = fresh
	elsewhere := a.invAt
	a.mu.Unlock()
	a.bindTerminalSessions(fresh)
	scan := scanDTO(core, fresh, s.ObservedAt, elsewhere, out.Processes)
	a.publishQuick(scan)
	if len(out.Problems) > 0 {
		a.quick.mu.Lock()
		a.quick.err = strings.Join(out.Problems, "; ")
		a.quick.mu.Unlock()
	}
	a.emit(RuntimeEvent, nil)
	// Settings written through the CLI are adopted before the next GUI save.
	cfg, err := config.Load()
	if err == nil {
		a.mu.Lock()
		changed := a.core.Cfg.AppearanceMode() != cfg.AppearanceMode()
		a.core.Cfg = cfg
		a.mu.Unlock()
		if changed {
			a.publishAppearance()
		}
	}
}
func (a *App) RuntimeRefresh() error {
	if err := a.connectRuntime(); err != nil {
		return err
	}
	a.backend.mu.Lock()
	client := a.backend.client
	a.backend.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return client.Call(ctx, "refresh", nil, nil)
}
func (a *App) runtimeSleep(paused bool) {
	a.backend.mu.Lock()
	client := a.backend.client
	a.backend.mu.Unlock()
	method := "resume"
	if paused {
		method = "pause"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Call(ctx, method, nil, nil); err != nil {
		a.backend.mu.Lock()
		a.backend.problem = err.Error()
		a.backend.snapshot.ExpiresAt = time.Time{}
		a.backend.mu.Unlock()
		a.emit(RuntimeEvent, nil)
	}
}

// RefreshHere requests the shared source and waits for one newer observation.
// It keeps remote results and any transfer under review; it never adopts imports.
func (a *App) RefreshHere() (*ScanDTO, error) {
	a.mu.Lock()
	cfgErr := a.cfgErr
	a.mu.Unlock()
	if cfgErr != nil {
		return nil, cfgErr
	}
	if err := a.connectRuntime(); err != nil {
		return nil, err
	}
	a.backend.mu.Lock()
	client := a.backend.client
	a.backend.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var previous observe.Snapshot
	if err := client.Call(ctx, "refresh", nil, &previous); err != nil {
		return nil, err
	}
	var received observe.Snapshot
	complete := errors.New("observation received")
	err := client.Watch(ctx, func(s observe.Snapshot) error {
		if s.Epoch == previous.Epoch && s.Sequence <= previous.Sequence {
			return nil
		}
		received = s
		return complete
	})
	if !errors.Is(err, complete) {
		return nil, err
	}
	if received.Error != "" {
		return nil, errors.New(received.Error)
	}
	if !received.Fresh(time.Now()) {
		return nil, errors.New("observation is paused or expired")
	}
	a.acceptRuntimeSnapshot(received)
	a.quick.mu.Lock()
	scan := a.quick.scan
	a.quick.mu.Unlock()
	return scan, nil
}

func (a *App) SaveRuntimePreferences(seconds int) error {
	a.mu.Lock()
	before := a.core.Cfg.Runtime
	a.core.Cfg.Runtime.ReconcileSeconds = seconds
	err := a.save()
	if err != nil {
		a.core.Cfg.Runtime = before
	}
	a.mu.Unlock()
	if err != nil {
		return err
	}
	return a.RuntimeRefresh()
}
