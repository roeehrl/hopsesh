package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/observe"
	localruntime "github.com/roeehrl/hopsesh/internal/core/runtime"
	"github.com/spf13/cobra"
)

func runtimeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "runtime", Short: "Manage the shared local runtime", Args: cobra.NoArgs}
	var watch bool
	var interval time.Duration
	obs := &cobra.Command{Annotations: map[string]string{"hopsesh.passive": "true"}, Use: "observe", Short: "Print a passive snapshot, or watch shared changes as JSON lines", Args: cobra.NoArgs,
		Long: `Reads local sessions and cached registrations without adopting imports,
changing movement marks, probing login or starting an agent. With --watch,
connects to the existing runtime or hosts one until interrupted. Connecting
another watcher does not create another collector or renew source freshness.
This command does not enable remote receiving.`, RunE: func(cmd *cobra.Command, _ []string) error {
			if interval < 5*time.Second || interval > 24*time.Hour {
				return errors.New("reconciliation interval must be between 5s and 24h")
			}
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer cancel()
			encoder := json.NewEncoder(cmd.OutOrStdout())
			client, err := runtimeClient()
			if err != nil {
				return err
			}
			var status localruntime.Status
			probe, done := context.WithTimeout(ctx, 200*time.Millisecond)
			err = client.Call(probe, "status", nil, &status)
			done()
			if err == nil {
				if watch {
					return watchRuntime(ctx, client, encoder)
				}
				var s observe.Snapshot
				if err = client.Call(ctx, "snapshot", nil, &s); err != nil {
					return err
				}
				if err = encoder.Encode(s); err != nil {
					return err
				}
				if s.Error != "" {
					return fmt.Errorf("observation failed: %s", s.Error)
				}
				return nil
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			source := app.New(cfg, modules, config.StateDir(), nil)
			if watch {
				opts := observe.Defaults()
				opts.Reconcile = interval
				owner, err := source.StartRuntime(ctx, "headless", nil, opts)
				if errors.Is(err, localruntime.ErrOwned) {
					return watchRuntime(ctx, client, encoder)
				}
				if err != nil {
					return err
				}
				defer owner.Close()
				return watchRuntime(ctx, client, encoder)
			}
			opts := observe.Defaults()
			opts.Reconcile = interval
			engine, err := observe.New(opts, func(ctx context.Context) (json.RawMessage, error) {
				out, err := source.ObserveLocal(ctx)
				if err != nil {
					return nil, err
				}
				return json.Marshal(out)
			})
			if err != nil {
				return err
			}
			updates, unsubscribe := engine.Subscribe()
			defer unsubscribe()
			ended := make(chan error, 1)
			go func() { ended <- engine.Run(ctx) }()
			defer func() { cancel(); <-ended }()
			select {
			case <-ctx.Done():
				return nil
			case s := <-updates:
				if err = encoder.Encode(s); err != nil {
					return err
				}
				if s.Error != "" {
					return fmt.Errorf("observation failed: %s", s.Error)
				}
				return nil
			}
		}}
	obs.Flags().BoolVar(&watch, "watch", false, "watch shared changes until interrupted; stream JSON lines")
	obs.Flags().DurationVar(&interval, "reconcile", time.Minute, "fallback interval when this command hosts the runtime")
	cmd.AddCommand(obs)
	cmd.AddCommand(runtimeCommands()...)
	return cmd
}
func watchRuntime(ctx context.Context, c localruntime.Client, encoder *json.Encoder) error {
	err := c.Watch(ctx, func(s observe.Snapshot) error { return encoder.Encode(s) })
	if ctx.Err() != nil {
		return nil
	}
	return err
}
