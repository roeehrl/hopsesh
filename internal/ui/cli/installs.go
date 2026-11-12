package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/installs"
	"github.com/roeehrl/hopsesh/internal/version"
)

func installsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "installs",
		Short: "List every hopsesh on this machine (the app, commands, packages) and bring them to this version",
		Long: `Lists every copy of hopsesh on this machine: the app, a command you installed, one a
package manager owns, and the copy hopsesh puts in ~/.hopsesh/bin when another machine sets
this one up. They share hopsesh's settings and history, so they should be one version.

--align brings the copies hopsesh may replace to this one's version (the app with its own
updater) and points hopsesh's own copy at the machine's own hopsesh; a package is left to
its manager. On another machine, hopsesh hosts setup <machine> does the same.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			defer r.app.Catalog.Close()
			ctx, cancel := ctxTimeout(10)
			defer cancel()
			l := app.LocalInstalls(ctx)
			if align, _ := cmd.Flags().GetBool("align"); align {
				return r.align(ctx, l)
			}
			if r.jsonOut {
				return r.emitJSON(l)
			}
			r.printInstalls(l)
			return nil
		},
	}
	cmd.Flags().Bool("align", false, "bring the other copies to this version")
	cmd.Flags().Bool("yes", false, "do not ask for confirmation")
	cmd.Flags().Bool("json", false, "output JSON")
	return cmd
}

func (r *run) printInstalls(l installs.List) {
	if len(l.Installs) == 0 {
		r.printf("No hopsesh is installed here (this one runs from where it was unpacked).\n")
		return
	}
	for _, in := range l.Installs {
		var tags []string
		switch in.Kind {
		case installs.App:
			tags = append(tags, "the app ("+installs.Tilde(in.Bundle, l.Home)+")")
		case installs.Own:
			tags = append(tags, "set up by hopsesh")
		case installs.Package:
			tags = append(tags, "from "+in.Manager)
		default:
			tags = append(tags, "installed by you")
		}
		if in.Main {
			tags = append(tags, "the one this machine runs")
		}
		if in.Terminal {
			tags = append(tags, "your terminal runs it")
		}
		if in.Service {
			tags = append(tags, "starts at login")
		}
		for _, x := range in.Links {
			tags = append(tags, "also "+installs.Tilde(x, l.Home))
		}
		r.printf("%-8s %s\n         %s\n", nonEmpty(in.Version, "?"), installs.Tilde(in.Path, l.Home), strings.Join(tags, " · "))
	}
	if l.ServiceGone {
		r.printf("\nhopsesh's login service starts %s, which isn't there any more.\n", l.Service)
	}
	if l.Mixed() || l.ServiceGone {
		r.printf("\nThey aren't one version: hopsesh installs --align brings them to %s.\n", version.Version)
	}
}

func (r *run) align(ctx context.Context, l installs.List) error {
	steps := installs.Plan(l, version.Version, "")
	if len(steps) == 0 {
		r.printf("Every hopsesh here is %s already.\n", version.Version)
		return nil
	}
	if !r.jsonOut {
		for _, s := range steps {
			r.printf("• %s\n", s.Text)
		}
		if !r.confirm("Go ahead?") {
			return nil
		}
	}
	out, err := r.app.AlignHere(ctx, func(s app.SetupStep) {
		if !r.jsonOut {
			r.printf("%s %s · %s\n", map[string]string{"done": "✓", "skipped": "–", "waiting": "…", "failed": "✕"}[s.State], s.Name, s.Detail)
		}
	})
	if r.jsonOut {
		if e := r.emitJSON(out); e != nil {
			return e
		}
	}
	return err
}
