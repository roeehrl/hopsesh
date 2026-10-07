package cli

import (
	"context"
	"errors"
	"fmt"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/proc"
	localruntime "github.com/roeehrl/hopsesh/internal/core/runtime"
	"os"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/termapp"
	"github.com/roeehrl/hopsesh/internal/ui/tui"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func (r *run) runTUI() error {
	r.askPasswordsFirst()
	deps := tui.Deps{App: r.app, Describe: func(e app.Entry) string { return branchInfo(e.Git) }}
	if client, err := runtimeClient(); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		var status any
		probeErr := client.Call(ctx, "status", nil, &status)
		if probeErr == nil {
			deps.Runtime = &client
		}
		cancel()
		if deps.Runtime == nil {
			owner, startErr := r.app.StartRuntime(context.Background(), "headless", func() error {
				return errors.New("an interactive terminal owns this runtime; finish transfers and exit the terminal UI to stop it")
			})
			if startErr != nil && !errors.Is(startErr, localruntime.ErrOwned) {
				return fmt.Errorf("start shared session observation: %w", startErr)
			}
			if owner != nil {
				defer owner.Close()
			}
			deps.Runtime = &client
		}
	} else {
		return err
	}
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
		if exit.Desktop && len(argv) > 0 {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			command := proc.CommandContext(ctx, argv[0], argv[1:]...)
			command.Dir = exit.RunDir
			// Claude desktop opening requires terminal stdin and stdout. Bubble Tea
			// has restored this terminal before returning the launch.
			command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
			command.Env = append(host.Without(os.Environ(), exit.Unset), exit.Env...)
			err := command.Run()
			cancel()
			return err
		}
		// The agent runs attached to this terminal: a resume labels the tab and is recorded
		// while it runs (so "Show" finds it); a driver's command is a step.
		l := app.Launch{Kind: termapp.KindStep, Run: agent.Command{Argv: argv, Dir: exit.RunDir, Unset: exit.Unset, Env: exit.Env},
			Labels: termapp.Labels{Title: exit.Title, Agent: exit.Agent, Machine: app.LocalName()}}
		if exit.Key.Session != "" {
			l.Kind, l.Key = termapp.KindSession, exit.Key
		}
		runErr := r.runInThisTerminal(l)
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
			res, err := r.app.ContinueHop(context.Background(), exit.Hop, nil)
			if err != nil {
				return err
			}
			if res != nil && res.Hop != nil && res.Hop.Remembered {
				if err := config.Save(&r.app.Cfg); err != nil {
					return err
				}
			}
			deps.Hop = exit.Hop
			continue
		}
		deps.Adopted = exit.Adopt
	}
}
