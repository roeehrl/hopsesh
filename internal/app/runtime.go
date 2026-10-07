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
	var relayMu sync.Mutex
	var relayService *relay.Service
	var relayProblem string
	var tasks []func(context.Context)
	if a.Cfg.Relay.Enabled {
		store := relay.Store{Directory: filepath.Join(a.StateDir, "relay")}
		connection, e := store.Connection(ctx)
		if e != nil {
			relayProblem = "Relay unavailable: " + e.Error()
		} else {
			identity, e := store.Identity(ctx)
			if e != nil {
				relayProblem = "Relay identity unavailable"
			} else if identity.Public.ID != connection.Device {
				relayProblem = "Relay credential belongs to another device"
			} else {
				httpClient, err := connection.HTTPClient()
				if err != nil {
					return nil, err
				}
				relayService = &relay.Service{Transport: relay.Transport{Base: connection.URL, Space: connection.Space, Token: connection.Token, HTTP: httpClient}, Processor: relay.Processor{Identity: identity, Space: connection.Space, Store: store, Handle: a.RelayReceiver(func() observe.Snapshot { return engine.Latest() })}}
				relayService.Processor.Recover = relayService.Processor.Handle
				relayService.Notify = func(err error) {
					problem := ""
					if err != nil {
						problem = "Relay disconnected: " + err.Error()
					}
					relayMu.Lock()
					changed := relayProblem != problem
					relayProblem = problem
					relayMu.Unlock()
					if changed && engine != nil {
						engine.Notify()
					}
				}
				tasks = append(tasks, func(ctx context.Context) {
					updates, unsubscribe := engine.Subscribe()
					defer unsubscribe()
					var stop func()
					halt := func() {
						if stop != nil {
							stop()
							stop = nil
						}
					}
					defer halt()
					for {
						select {
						case <-ctx.Done():
							return
						case _, ok := <-updates:
							if !ok {
								return
							}
							cfg, err := config.Load()
							enabled := err == nil && cfg.Relay.Enabled
							if !enabled {
								halt()
								continue
							}
							if stop == nil {
								stop = startRelayTask(ctx, relayService)
							}
						}
					}
				})
			}
		}
	}

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
		if files != nil {
			roots := append(append([]string{}, out.WatchRoots...), config.Dir(), a.StateDir)
			if err = files.SetRoots(roots); err != nil {
				out.Problems = append(out.Problems, "Change notifications degraded: "+err.Error())
			}
		}
		if watchProblem != "" {
			out.Problems = append(out.Problems, watchProblem)
		}
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
	handler := func(ctx context.Context, method string, p json.RawMessage) (any, error) {
		switch method {
		case "relay.status":
			if relayService == nil {
				return relay.Health{Error: "relay listener is not running"}, nil
			}
			return relayService.Health(), nil
		case "relay.call":
			cfg, err := config.Load()
			if err != nil {
				return nil, err
			}
			if !cfg.Relay.Enabled {
				return nil, errors.New("relay is disabled")
			}
			if relayService == nil {
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
			finished, err := a.beginRuntimeAction()
			if err != nil {
				return nil, err
			}
			defer finished()
			bounded, cancel := context.WithTimeout(ctx, 30*time.Minute)
			defer cancel()
			return relayService.Call(bounded, in.Peer, in.Operation, in.Method, in.Params)
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
