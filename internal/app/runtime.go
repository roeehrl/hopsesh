package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/observe"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	localruntime "github.com/roeehrl/hopsesh/internal/core/runtime"
	"github.com/roeehrl/hopsesh/internal/version"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// StartRuntime hosts the single shared observer. GUI and headless hosts use the
// same source; connecting clients never initializes profiles or starts agents.
func (a *App) StartRuntime(ctx context.Context, mode string, guard func() error, overrides ...observe.Options) (*localruntime.Host, error) {
	n, err := localruntime.NewNamespace(config.Dir(), a.StateDir)
	if err != nil {
		return nil, err
	}
	opts := observe.Defaults()
	opts.Reconcile = a.Cfg.Runtime.Interval()
	if len(overrides) > 0 {
		opts = overrides[0]
	}
	var engine *observe.Engine
	var files *observe.Files
	var watchProblem string
	var watchMu sync.Mutex
	var relayMu sync.Mutex
	var relayService *relay.Service
	var relayContext context.Context
	var relayProblem string
	var tasks []func(context.Context)
	remotes := newRemoteObserver(a)
	tasks = append(tasks, func(ctx context.Context) { remotes.run(ctx, engine) })
	// Always supervise relay configuration, including enrollment or enablement
	// after the owner has started. Clients never create additional listeners.
	tasks = append(tasks, func(ctx context.Context) {
		updates, unsubscribe := engine.Subscribe()
		defer unsubscribe()
		var connection relay.Connection
		var stop func()
		halt := func() {
			relayMu.Lock()
			relayService = nil
			relayContext = nil
			relayMu.Unlock()
			if stop != nil {
				stop()
				stop = nil
			}
		}
		defer halt()
		problem := func(message string) {
			relayMu.Lock()
			changed := relayProblem != message
			relayProblem = message
			relayMu.Unlock()
			if changed {
				engine.Notify()
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-updates:
				if !ok {
					return
				}
				cfg, err := config.Load()
				if err != nil {
					halt()
					problem("Relay settings unavailable")
					continue
				}
				if !cfg.Relay.Enabled {
					halt()
					problem("")
					continue
				}
				store := relay.Store{Directory: filepath.Join(a.StateDir, "relay")}
				current, err := store.Connection(ctx)
				if err != nil {
					halt()
					problem("Relay unavailable: " + err.Error())
					continue
				}
				if stop != nil && current == connection {
					continue
				}
				halt()
				identity, err := store.Identity(ctx)
				if err != nil || identity.Public.ID != current.Device {
					problem("Relay identity or routing credential does not match this device")
					continue
				}
				httpClient, err := current.HTTPClient()
				if err != nil {
					problem("Relay trust configuration is invalid; check the explicitly configured certificate file")
					continue
				}
				service := &relay.Service{Transport: relay.Transport{Base: current.URL, Space: current.Space, Token: current.Token, HTTP: httpClient}, Processor: relay.Processor{Identity: identity, Space: current.Space, Store: store, Handle: a.relayReceiver(engine.Latest, engine.Refresh)}}
				service.Processor.Recover = service.Processor.Handle
				service.Notify = func(err error) {
					message := ""
					if err != nil {
						message = "Relay disconnected: " + err.Error()
					}
					relayMu.Lock()
					active := relayService == service
					changed := active && relayProblem != message
					if active {
						relayProblem = message
					}
					relayMu.Unlock()
					if changed {
						engine.Notify()
					}
				}
				relayMu.Lock()
				relayService = service
				relayMu.Unlock()
				connection = current
				child, cancel := context.WithCancel(ctx)
				relayMu.Lock()
				relayContext = child
				relayMu.Unlock()
				childStop := startRelayTask(child, service)
				stop = func() {
					cancel()
					childStop()
					if httpClient != nil {
						httpClient.CloseIdleConnections()
					}
				}
			}
		}
	})

	collect := func(ctx context.Context) (json.RawMessage, error) {
		cfg, err := config.Load()
		if err != nil {
			return nil, err
		}
		if engine != nil && len(overrides) == 0 {
			updated := engine.Options()
			updated.Reconcile = cfg.Runtime.Interval()
			if err := engine.UpdateOptions(updated); err != nil {
				return nil, err
			}
		}
		source := *a
		source.Cfg = cfg
		out, err := source.ObserveLocal(ctx)
		if err != nil {
			return nil, err
		}
		out.Remotes = remotes.latest()
		if files != nil {
			roots := runtimeWatchRoots(out.WatchRoots, a.StateDir)
			if err = files.SetRoots(roots); err != nil {
				out.Problems = append(out.Problems, "Change notifications degraded: "+err.Error())
			}
		}
		watchMu.Lock()
		if watchProblem != "" {
			out.Problems = append(out.Problems, watchProblem)
		}
		watchMu.Unlock()
		relayMu.Lock()
		if relayProblem != "" {
			out.Problems = append(out.Problems, relayProblem)
		}
		relayMu.Unlock()
		return json.Marshal(out)
	}
	engine, err = observe.New(opts, collect)
	if err != nil {
		return nil, err
	}
	files, err = observe.NewFiles(4096, engine.Notify)
	if err != nil {
		watchProblem = fmt.Sprintf("Change notifications unavailable: %v; reconciliation remains active", err)
	}
	if files != nil {
		tasks = append(tasks, func(ctx context.Context) {
			for {
				select {
				case <-ctx.Done():
					return
				case err, ok := <-files.Problems():
					if !ok {
						return
					}
					watchMu.Lock()
					watchProblem = "Change notifications degraded: " + err.Error() + "; reconciliation remains active"
					watchMu.Unlock()
					engine.Notify()
				}
			}
		})
	}
	handler := func(ctx context.Context, method string, p json.RawMessage) (any, error) {
		switch method {
		case "machines.refresh":
			var in struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(p, &in); err != nil {
				return nil, err
			}
			return remotes.refresh(ctx, in.Name)
		case "relay.status":
			relayMu.Lock()
			service := relayService
			relayMu.Unlock()
			if service == nil {
				return relay.Health{Error: "relay listener is not running"}, nil
			}
			return service.Health(), nil
		case "relay.call":
			cfg, err := config.Load()
			if err != nil {
				return nil, err
			}
			if !cfg.Relay.Enabled {
				return nil, errors.New("relay is disabled")
			}
			relayMu.Lock()
			service := relayService
			serviceContext := relayContext
			relayMu.Unlock()
			if service == nil || serviceContext == nil {
				return nil, errors.New("relay is not connected; enroll this device and enable relay first")
			}
			var in struct {
				Peer      string          `json:"peer"`
				Operation string          `json:"operation"`
				Method    string          `json:"method"`
				Params    json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(p, &in); err != nil {
				return nil, err
			}
			// Passive scheduler traffic must not make safe shutdown look like a
			// user transfer. Owner cancellation joins its observation task.
			if in.Method != "observe" && in.Method != "preview" && in.Method != "hello" {
				finished, err := a.beginRuntimeAction()
				if err != nil {
					return nil, err
				}
				defer finished()
			}
			bounded, cancel := context.WithTimeout(ctx, 30*time.Minute)
			defer cancel()
			unlink := context.AfterFunc(serviceContext, cancel)
			defer unlink()
			return service.Call(bounded, in.Peer, in.Operation, in.Method, in.Params)
		case "settings.get":
			return config.ReadSettings()
		case "settings.set":
			var in struct {
				Key      string          `json:"key"`
				Value    json.RawMessage `json:"value"`
				Revision string          `json:"revision"`
			}
			if err := json.Unmarshal(p, &in); err != nil {
				return nil, err
			}
			out, err := config.SetSetting(in.Key, in.Value, &in.Revision)
			if err == nil {
				engine.Notify()
			}
			return out, err
		default:
			return nil, errors.New("unknown local runtime method")
		}
	}
	host, err := localruntime.Start(ctx, n, engine, mode, version.Version, handler, func() error {
		if guard == nil {
			return errors.New("runtime shutdown is not enabled")
		}
		if err := guard(); err != nil {
			return err
		}
		return a.drainRuntime()
	}, tasks...)
	if err != nil {
		if files != nil {
			files.Close()
		}
		return nil, err
	}
	if files != nil {
		go func() { <-host.Done(); files.Close() }()
	}
	return host, nil
}

func startRelayTask(ctx context.Context, service *relay.Service) func() {
	child, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); _ = service.Run(child) }()
	return func() { cancel(); <-done }
}

// Delivery queues, ciphertext, staging, downloads and logs do not change local
// session evidence. Watching the entire state tree made those writes repeatedly
// wake the passive collector and recursively enumerate unrelated large folders.
func runtimeWatchRoots(agentRoots []string, state string) []string {
	roots := append([]string(nil), agentRoots...)
	roots = append(roots, config.Path(), filepath.Join(config.Dir(), "endpoint-id"))
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, ".hopsesh", "endpoint-id"))
	}
	for _, rel := range []string{"accounts.json", "pending-marks.jsonl", "movement", "journal", filepath.Join("terminal", "records"), filepath.Join("relay", "connection.json"), filepath.Join("relay", "identity.json")} {
		roots = append(roots, filepath.Join(state, rel))
	}
	return roots
}
