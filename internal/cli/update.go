package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/update"
	"github.com/roeehrl/hopsesh/internal/version"
)

func updateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Check for a new release and install it (verified: signature, checksum, code signature)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := newApp(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := ctxTimeout(5)
			defer cancel()
			rel, err := update.Latest(ctx)
			if err != nil {
				return fmt.Errorf("could not check for updates: %w", err)
			}
			newer := update.Newer(rel.Version, version.Version)
			if check, _ := cmd.Flags().GetBool("check"); check || a.jsonOut {
				if a.jsonOut {
					return a.emitJSON(map[string]any{"current": version.Version, "latest": rel.Version, "newer": newer, "url": rel.URL})
				}
				if newer {
					a.printf("hopsesh %s is available (you have %s): %s\n", rel.Version, version.Version, rel.URL)
				} else {
					a.printf("hopsesh %s is the latest release (you have %s).\n", rel.Version, version.Version)
				}
				return nil
			}
			if !newer {
				a.printf("You have the latest release (%s).\n", version.Version)
				return nil
			}
			if who, how := update.ManagedBy(); who != "" {
				a.printf("hopsesh %s is available. This copy came from %s; update it with:\n  %s\n", rel.Version, who, how)
				return nil
			}
			if !a.confirm(fmt.Sprintf("Install hopsesh %s (you have %s)?", rel.Version, version.Version)) {
				return nil
			}
			path, err := update.Install(ctx, rel)
			if err != nil {
				return err
			}
			a.log.Write(audit.Entry{Action: "update", Detail: map[string]any{"from": version.Version, "to": rel.Version}})
			a.printf("Installed hopsesh %s at %s.\n", rel.Version, path)
			return nil
		},
	}
	cmd.Flags().Bool("check", false, "only report whether a newer release exists")
	cmd.Flags().Bool("yes", false, "do not ask for confirmation")
	cmd.Flags().Bool("json", false, "output JSON (implies --check)")
	return cmd
}
