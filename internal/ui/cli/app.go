package cli

import (
	"encoding/json"
	"errors"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/appinstall"
	"github.com/roeehrl/hopsesh/internal/update"
	"github.com/spf13/cobra"
	"os/exec"
	"runtime"
)

func appCmd() *cobra.Command {
	root := &cobra.Command{Use: "app", Short: "Detect, install or open the desktop app from the CLI", Args: cobra.NoArgs}
	status := &cobra.Command{Use: "status", Short: "Inspect installed app metadata without opening it", Args: cobra.NoArgs, Annotations: map[string]string{"hopsesh.passive": "true"}, RunE: func(cmd *cobra.Command, _ []string) error {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(appinstall.Detect())
	}}
	status.Flags().Bool("json", true, "print JSON")
	var plan bool
	var version string
	install := &cobra.Command{Use: "install", Short: "Install the missing desktop app from a verified release", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		target, err := update.AppTarget()
		if err != nil {
			return err
		}
		var rel *update.Release
		if version == "" {
			rel, err = update.Latest(cmd.Context())
		} else {
			rel, err = update.ByVersion(cmd.Context(), version)
		}
		if err != nil {
			return err
		}
		if plan {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
				Target    update.Target   `json:"target"`
				Release   *update.Release `json:"release"`
				Asset     string          `json:"asset"`
				CanVerify bool            `json:"canVerify"`
			}{target, rel, update.AssetName(target, rel.Version), update.PublicKey != ""})
		}
		if err = update.InstallApp(cmd.Context(), rel, target); err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(appinstall.Detect())
	}}
	install.Flags().BoolVar(&plan, "plan", false, "show release, destination and verification availability without installing")
	install.Flags().StringVar(&version, "version", "", "published version to install (default latest)")
	open := &cobra.Command{Use: "open", Short: "Open the installed desktop shell", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		s := appinstall.Detect()
		if !s.Installed {
			return errors.New(s.Reason)
		}
		var c *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			c = exec.CommandContext(cmd.Context(), "open", "-n", s.Path, "--args", "--config-dir", config.Dir(), "--state-dir", config.StateDir())
		case "windows":
			c = exec.Command(s.Path+`\hopsesh-app.exe`, "--config-dir", config.Dir(), "--state-dir", config.StateDir())
		default:
			return errors.New("no published desktop app for this OS")
		}
		return c.Start()
	}}
	root.AddCommand(status, install, open)
	return root
}
