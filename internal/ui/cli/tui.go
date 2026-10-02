package cli

import (
	"os"
	"os/exec"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/ui/tui"
)

func (r *run) runTUI() error {
	r.askPasswordsFirst()
	exit, err := tui.Run(tui.Deps{App: r.app, Describe: func(e app.Entry) string { return branchInfo(e.Git) }})
	if err != nil || exit == nil {
		return err
	}
	argv := exit.RunArgv
	if exit.Prompt != "" && len(argv) > 1 {
		if b, err := os.ReadFile(exit.Prompt); err == nil {
			argv = append(argv[:len(argv)-1:len(argv)-1], string(b))
		}
	}
	c := exec.Command(argv[0], argv[1:]...)
	c.Dir = exit.RunDir
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}
