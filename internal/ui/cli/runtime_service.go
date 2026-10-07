package cli

import (
	"encoding/json"
	"errors"
	"github.com/roeehrl/hopsesh/internal/config"
	localruntime "github.com/roeehrl/hopsesh/internal/core/runtime"
	"github.com/spf13/cobra"
	"os"
)

func runtimeServiceCommands() []*cobra.Command {
	var plan bool
	var mode string
	enable := &cobra.Command{Use: "enable", Short: "Register the headless runtime for this user's login", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		atLogin, _ := cmd.Flags().GetBool("at-login")
		if !atLogin {
			return errors.New("runtime enable registers login startup; use runtime start for this login only")
		}
		if mode != "headless" {
			return errors.New("use the installed app's Desktop settings for desktop login startup; runtime enable manages the headless host")
		}
		c, err := runtimeClient()
		if err != nil {
			return err
		}
		p, err := localruntime.CurrentServicePlan(c.Namespace)
		if err != nil {
			return err
		}
		if plan {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(p)
		}
		var status localruntime.Status
		if err = c.Call(cmd.Context(), "status", nil, &status); err == nil && status.Mode == "desktop" {
			return errors.New("the desktop currently owns this runtime; quit it after finishing transfers, then enable the headless host; integrated terminals stay owned by the app")
		}
		if err = os.MkdirAll(config.StateDir(), 0700); err != nil {
			return err
		}
		if err = p.EnableAtLogin(cmd.Context()); err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(p)
	}}
	enable.Flags().BoolVar(&plan, "plan", false, "show the exact OS registration without changing it")
	enable.Flags().StringVar(&mode, "mode", "headless", "host mode: headless")
	enable.Flags().Bool("at-login", true, "start for the current OS user at login")
	disable := &cobra.Command{Use: "disable", Short: "Remove this namespace's headless login registration", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := runtimeClient()
		if err != nil {
			return err
		}
		p, err := localruntime.CurrentServicePlan(c.Namespace)
		if err != nil {
			return err
		}
		// Obtain the owner's guarded shutdown decision before a supervisor can stop it.
		var status localruntime.Status
		if err = c.Call(cmd.Context(), "status", nil, &status); err == nil {
			if status.Mode != "headless" {
				return errors.New("desktop runtime is running; manage desktop startup in the app")
			}
			if err = c.Call(cmd.Context(), "stop", nil, nil); err != nil {
				return err
			}
		}
		return p.DisableAtLogin(cmd.Context())
	}}
	return []*cobra.Command{enable, disable}
}
