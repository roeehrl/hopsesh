package cli

import (
	"context"
	"os"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/proc"
	"github.com/roeehrl/hopsesh/internal/ui/tui"
)

func (r *run) runTUI() error {
	r.askPasswordsFirst()
	deps := tui.Deps{App: r.app, Describe: func(e app.Entry) string { return branchInfo(e.Git) }}
	for {
		exit, err := tui.Run(deps)
		if err != nil || exit == nil {
			return err
		}
		argv := exit.RunArgv
		if exit.Prompt != "" && len(argv) > 1 {
			if b, err := os.ReadFile(exit.Prompt); err == nil {
				argv = append(argv[:len(argv)-1:len(argv)-1], string(b))
			}
		}
		c := proc.Command(argv[0], argv[1:]...)
		c.Dir = exit.RunDir
		c.Env = host.Without(os.Environ(), exit.Unset)
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		runErr := c.Run()
		if exit.Adopt == "" {
			return runErr
		}
		// The agent's own command brought a session from a cloud: adopt what it wrote, and
		// show what came back.
		if _, err := r.app.Adopt(context.Background(), exit.Adopt, true); err != nil {
			return err
		}
		if exit.Hop != "" {
			// The first leg of a hop: take it on to the next cloud, and show where it stands.
			res, _ := r.app.ContinueHop(context.Background(), exit.Hop, nil)
			if res != nil && res.Hop != nil && res.Hop.Remembered {
				_ = config.Save(r.app.Cfg)
			}
			deps.Hop = exit.Hop
			continue
		}
		deps.Adopted = exit.Adopt
	}
}
