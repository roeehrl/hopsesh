package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/transport"
	"github.com/roeehrl/hopsesh/internal/inventory"
)

func hostsHelperCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "helper",
		Short: "Optional: install a hopsesh helper on a machine so listing its sessions takes one SSH command",
		Long: `The helper is a copy of hopsesh placed at ~/.local/share/hopsesh/bin/hopsesh on the other
machine. hopsesh runs it as "hopsesh agent --json" over SSH to list sessions, after checking
its SHA-256 against the copy it uploaded. It listens on nothing and changes nothing there.
Without it, hopsesh reads the files over SFTP, which is slower on large histories.
macOS and Linux machines only.`,
	}
	install := &cobra.Command{
		Use:   "install <machine>",
		Short: "Upload the helper (asks first)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := newApp(cmd)
			if err != nil {
				return err
			}
			h := a.cfg.FindHost(args[0])
			if h == nil || !h.Allowed {
				return fmt.Errorf("allow the machine first: hopsesh hosts allow %s", args[0])
			}
			ctx, cancel := ctxTimeout(3)
			defer cancel()
			conn, facts, err := connectHost(ctx, a, h)
			if err != nil {
				return err
			}
			defer conn.Close()
			explicit, _ := cmd.Flags().GetString("binary")
			bin, err := inventory.HelperBinary(explicit, facts)
			if err != nil {
				return err
			}
			if !a.confirm(fmt.Sprintf("Upload %s to %s:%s?", bin, h.Name, inventory.HelperPath(facts))) {
				return nil
			}
			sum, err := inventory.InstallHelper(ctx, conn, facts, bin)
			if err != nil {
				return err
			}
			h.Helper, h.HelperSHA256 = true, sum
			if err := config.Save(a.cfg); err != nil {
				return err
			}
			a.log.Write(audit.Entry{Action: "helper.install", Host: h.Name, Detail: map[string]any{"sha256": sum}})
			a.printf("Installed the helper on %s (sha256 %s…). Remove it with: hopsesh hosts helper remove %s\n", h.Name, sum[:12], h.Name)
			return nil
		},
	}
	install.Flags().String("binary", "", "hopsesh binary built for that machine (default: this one, when it can run there)")
	install.Flags().Bool("yes", false, "do not ask for confirmation")
	remove := &cobra.Command{
		Use:   "remove <machine>",
		Short: "Delete the helper from a machine and stop using it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := newApp(cmd)
			if err != nil {
				return err
			}
			h := a.cfg.FindHost(args[0])
			if h == nil {
				return fmt.Errorf("unknown machine %q", args[0])
			}
			ctx, cancel := ctxTimeout(1)
			defer cancel()
			h.Helper, h.HelperSHA256 = false, ""
			if err := config.Save(a.cfg); err != nil {
				return err
			}
			conn, facts, err := connectHost(ctx, a, h)
			if err != nil {
				return fmt.Errorf("stopped using the helper, but could not reach %s to delete it: %w", h.Name, err)
			}
			defer conn.Close()
			if err := inventory.RemoveHelper(ctx, conn, facts); err != nil {
				return err
			}
			a.log.Write(audit.Entry{Action: "helper.remove", Host: h.Name})
			a.printf("Removed the helper from %s.\n", h.Name)
			return nil
		},
	}
	cmd.AddCommand(install, remove)
	return cmd
}

// connectHost opens a connection to a configured machine and reads its basic facts.
func connectHost(ctx context.Context, a *app, h *config.Host) (*transport.Conn, *transport.Facts, error) {
	conn, err := transport.NewConn(h.Destination, config.StateDir(), a.log)
	if err != nil {
		return nil, nil, err
	}
	if h.TailscaleName != "" && h.TailscaleName != h.Destination {
		conn.Fallbacks = []string{h.TailscaleName}
	}
	facts, err := conn.Probe(ctx)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	return conn, facts, nil
}
