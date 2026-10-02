package cli

import (
	"context"
	"os"
	"os/exec"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/inventory"
	"github.com/roeehrl/hopsesh/internal/tui"
)

func (a *app) runTUI() error {
	exit, err := tui.Run(tui.Deps{
		Config: a.cfg, StateDir: config.StateDir(), Log: a.log, Roots: a.localRoots(),
		Describe: func(s *inventory.Session) string { return branchInfo(s.Git) },
	})
	if err != nil || exit == nil {
		return err
	}
	bin := "claude"
	if p, _ := inventory.LocalClaude(context.Background()); p != "" {
		bin = p
	}
	c := exec.Command(bin, exit.RunArgv[1:]...)
	c.Dir = exit.RunDir
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}
