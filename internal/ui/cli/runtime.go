package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/observe"
	"github.com/spf13/cobra"
)

func runtimeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "runtime", Short: "Observe local sessions without changing them", Args: cobra.NoArgs}
	var watch bool
	var interval time.Duration
	obs := &cobra.Command{
		Annotations: map[string]string{"hopsesh.passive": "true"},
		Use:         "observe", Short: "Print a passive local snapshot, or watch changes as JSON lines", Args: cobra.NoArgs,
		Long: `Reads local sessions and cached profile registrations without adopting imports,
changing movement marks, probing login, starting an agent, or writing state.
With --watch, filesystem notifications trigger shared, coalesced observations;
bounded reconciliation covers missed events and process exits. Output includes
source freshness and errors. This command does not enable remote receiving.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if interval < 5*time.Second || interval > 24*time.Hour {
				return fmt.Errorf("reconciliation interval must be between 5s and 24h")
			}
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer cancel()
			opts := observe.Defaults()
			opts.Reconcile = interval
			var files *observe.Files
			var notificationProblem string
			collect := func(ctx context.Context) (json.RawMessage, error) {
				cfg, err := config.Load()
				if err != nil {
					return nil, err
				}
				a := app.New(cfg, modules, config.StateDir(), nil)
				o, err := a.ObserveLocal(ctx)
				if err != nil {
					return nil, err
				}
				if files != nil {
					roots := append(append([]string{}, o.WatchRoots...), config.Dir(), config.StateDir())
					if err := files.SetRoots(roots); err != nil {
						o.Problems = append(o.Problems, "Change notifications degraded: "+err.Error())
					}
				}
				if notificationProblem != "" {
					o.Problems = append(o.Problems, notificationProblem)
				}
				b, err := json.Marshal(o)
				return b, err
			}
			engine, err := observe.New(opts, collect)
			if err != nil {
				return err
			}
			if watch {
				files, err = observe.NewFiles(4096, engine.Notify)
				if err != nil {
					notificationProblem = fmt.Sprintf("Change notifications unavailable: %v; reconciliation remains active", err)
				} else {
					defer files.Close()
				}
			}
			updates, unsubscribe := engine.Subscribe()
			defer unsubscribe()
			done := make(chan error, 1)
			go func() { done <- engine.Run(ctx) }()
			defer func() { cancel(); <-done }()
			encoder := json.NewEncoder(cmd.OutOrStdout())
			var problems <-chan error
			if files != nil {
				problems = files.Problems()
			}
			for {
				select {
				case <-ctx.Done():
					return nil
				case err, ok := <-problems:
					if !ok {
						problems = nil
						continue
					}
					fmt.Fprintf(cmd.ErrOrStderr(), "Change notifications degraded: %v; reconciliation remains active\n", err)
				case snapshot, ok := <-updates:
					if !ok {
						return nil
					}
					if err := encoder.Encode(snapshot); err != nil {
						return err
					}
					if !watch {
						if snapshot.Error != "" {
							return fmt.Errorf("observation failed: %s", snapshot.Error)
						}
						return nil
					}
				}
			}
		},
	}
	obs.Flags().BoolVar(&watch, "watch", false, "watch changes until interrupted; stream JSON lines")
	obs.Flags().DurationVar(&interval, "reconcile", time.Minute, "fallback interval for missed changes and process exits")
	cmd.AddCommand(obs)
	return cmd
}
