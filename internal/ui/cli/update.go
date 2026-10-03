package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/integrate"
	"github.com/roeehrl/hopsesh/internal/core/proc"
	"github.com/roeehrl/hopsesh/internal/update"
	"github.com/roeehrl/hopsesh/internal/version"
)

func updateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Check for a new release and install it (verified: signature, checksum, code signature)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := newRun(cmd)
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
			if check, _ := cmd.Flags().GetBool("check"); check || r.jsonOut {
				if r.jsonOut {
					return r.emitJSON(map[string]any{"current": version.Version, "latest": rel.Version, "newer": newer, "url": rel.URL})
				}
				if newer {
					r.printf("hopsesh %s is available (you have %s): %s\n", rel.Version, version.Version, rel.URL)
				} else {
					r.printf("hopsesh %s is the latest release (you have %s).\n", rel.Version, version.Version)
				}
				return nil
			}
			if !newer {
				r.printf("You have the latest release (%s).\n", version.Version)
				return nil
			}
			if who, how := update.ManagedBy(); who != "" {
				r.printf("hopsesh %s is available. This copy came from %s; update it with:\n  %s\n", rel.Version, who, how)
				return nil
			}
			if !r.confirm(fmt.Sprintf("Install hopsesh %s (you have %s)?", rel.Version, version.Version)) {
				return nil
			}
			t, err := update.Install(ctx, rel)
			if err != nil {
				return err
			}
			r.app.Audit.Write(audit.Entry{Action: "update", Detail: map[string]any{"from": version.Version, "to": rel.Version, "what": string(t.Kind)}})
			r.printf("Installed hopsesh %s at %s.\n", rel.Version, t.Path)
			if t.Kind != update.KindCLI {
				r.printf("If the hopsesh app is open, quit and reopen it to use the new version.\n")
			}
			path := update.CLIPath(t)
			// The new binary renders the skill; ask it to refresh unedited copies.
			files, bin := r.skillFiles()
			if rep := r.app.Skill(ctx, files, bin); rep.State != integrate.Absent {
				if out, err := proc.Command(path, "skill", "install").CombinedOutput(); err == nil {
					r.printf("Refreshed the hopsesh skill.\n")
				} else if len(out) > 0 {
					r.printf("The hopsesh skill was not refreshed: %s", out)
				}
			}
			return nil
		},
	}
	cmd.Flags().Bool("check", false, "only report whether a newer release exists")
	cmd.Flags().Bool("yes", false, "do not ask for confirmation")
	cmd.Flags().Bool("json", false, "output JSON (implies --check)")
	return cmd
}
