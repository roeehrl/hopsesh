package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	localruntime "github.com/roeehrl/hopsesh/internal/core/runtime"
	"github.com/spf13/cobra"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"time"
)

func runtimeClient() (localruntime.Client, error) {
	n, err := localruntime.NewNamespace(config.Dir(), config.StateDir())
	return localruntime.Client{Namespace: n}, err
}
func runtimeCommands() []*cobra.Command {
	serve := &cobra.Command{Use: "serve", Short: "Run the shared headless runtime in this process", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		log, err := localruntime.OpenLog(config.StateDir())
		if err != nil {
			return err
		}
		defer log.Close()
		logger := slog.New(slog.NewJSONHandler(log, nil))
		ctx, cancel := signal.NotifyContext(cmd.Context(), runtimeSignals()...)
		defer cancel()
		source := app.New(cfg, modules, config.StateDir(), nil)
		source.Log = logger
		host, err := source.StartRuntime(ctx, "headless", func() error { return nil })
		if err != nil {
			logger.Error("runtime startup failed", "error", "owner or listener unavailable")
			return err
		}
		defer host.Close()
		logger.Info("runtime started", "namespace", host.Namespace.ID, "mode", "headless")
		if err = json.NewEncoder(cmd.OutOrStdout()).Encode(host.Status()); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
		case <-host.Done():
		}
		logger.Info("runtime stopped", "namespace", host.Namespace.ID)
		return nil
	}}
	status := &cobra.Command{Use: "status", Short: "Identify the actual owner of this settings/state namespace", Args: cobra.NoArgs, Annotations: map[string]string{"hopsesh.passive": "true"}, RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := runtimeClient()
		if err != nil {
			return err
		}
		var out localruntime.Status
		if err = c.Call(cmd.Context(), "status", nil, &out); err != nil {
			return fmt.Errorf("runtime unavailable for namespace %s: %w", c.Namespace.ID, err)
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
	}}
	stop := &cobra.Command{Use: "stop", Short: "Ask this runtime owner to stop safely", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := runtimeClient()
		if err != nil {
			return err
		}
		if err = c.Call(cmd.Context(), "stop", nil, nil); err != nil {
			return err
		}
		p, planErr := localruntime.CurrentServicePlan(c.Namespace)
		if planErr != nil {
			return planErr
		}
		if _, statErr := os.Stat(p.Path); statErr == nil {
			if err = p.StopNow(cmd.Context()); err != nil {
				return err
			}
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Runtime shutdown accepted.")
		return nil
	}}
	status.Flags().Bool("json", true, "print JSON")
	start := &cobra.Command{Use: "start", Short: "Start a shared headless runtime, or report the existing owner", Args: cobra.NoArgs, RunE: startRuntime}
	start.Flags().Bool("headless", true, "run without desktop libraries")
	doctor := &cobra.Command{Use: "doctor", Short: "Print redacted runtime health; excludes paths, conversations, credentials and log contents", Args: cobra.NoArgs, Annotations: map[string]string{"hopsesh.passive": "true"}, RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := config.Load()
		if err != nil {
			cfg = config.Defaults()
		}
		d, err := app.New(cfg, modules, config.StateDir(), nil).Diagnostics(cmd.Context())
		if err != nil {
			return err
		}
		out := json.NewEncoder(cmd.OutOrStdout())
		out.SetIndent("", "  ")
		return out.Encode(d)
	}}
	return append([]*cobra.Command{serve, status, stop, start, doctor}, runtimeServiceCommands()...)
}
func startRuntime(cmd *cobra.Command, _ []string) error {
	headless, _ := cmd.Flags().GetBool("headless")
	if !headless {
		return errors.New("runtime start supports the headless host; use hopsesh app open for the desktop")
	}
	client, err := runtimeClient()
	if err != nil {
		return err
	}
	var status localruntime.Status
	if err = client.Call(cmd.Context(), "status", nil, &status); err == nil {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(status)
	}
	// An existing lock with an incompatible/unresponsive listener is not replaced:
	// the child must acquire the same OS-held lock and will refuse a second owner.
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(config.StateDir(), 0700); err != nil {
		return err
	}
	log, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	child := exec.Command(exe, "runtime", "serve")
	child.Env = os.Environ()
	child.Stdout = log
	child.Stderr = log
	detach(child)
	if err = child.Start(); err != nil {
		return err
	}
	ended := make(chan error, 1)
	go func() { ended <- child.Wait() }()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	retry := time.NewTicker(50 * time.Millisecond)
	defer retry.Stop()
	for {
		select {
		case <-cmd.Context().Done():
			_ = child.Process.Kill()
			<-ended
			return cmd.Context().Err()
		case <-timer.C:
			_ = child.Process.Kill()
			<-ended
			return errors.New("runtime did not become ready within 10s; inspect runtime.log")
		case err := <-ended:
			if err == nil {
				err = errors.New("runtime exited before it was ready")
			}
			return fmt.Errorf("runtime startup: %w; inspect runtime.log", err)
		case <-retry.C:
			ctx, cancel := context.WithTimeout(cmd.Context(), 200*time.Millisecond)
			err = client.Call(ctx, "status", nil, &status)
			cancel()
			if err == nil {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(status)
			}
		}
	}
}
func settingsCmd() *cobra.Command {
	root := &cobra.Command{Use: "settings", Short: "Read or change validated shared settings", Args: cobra.NoArgs}
	var revision string
	get := &cobra.Command{Use: "get [key]", Short: "Read settings and their revision as JSON", Args: cobra.MaximumNArgs(1), Annotations: map[string]string{"hopsesh.passive": "true"}, RunE: func(cmd *cobra.Command, args []string) error {
		settings, err := config.ReadSettings()
		if err != nil {
			return err
		}
		if len(args) == 0 {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(settings)
		}
		value, err := config.SettingValue(settings, args[0])
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
			Revision string `json:"revision"`
			Value    any    `json:"value"`
		}{settings.Revision, value})
	}}
	get.Flags().Bool("json", true, "print JSON")
	set := &cobra.Command{Use: "set <key> <value>", Short: "Validate and save one setting; stale revisions are refused", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		value := json.RawMessage(args[1])
		if !json.Valid(value) {
			b, _ := json.Marshal(args[1])
			value = b
		}
		var expected *string
		if cmd.Flags().Changed("revision") {
			expected = &revision
		}
		out, err := config.SetSetting(args[0], value, expected)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
	}}
	set.Flags().StringVar(&revision, "revision", "", "require this settings revision before saving")
	root.AddCommand(get, set)
	return root
}
