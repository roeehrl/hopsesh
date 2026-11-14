package move

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// resumer is a module whose resume command depends on the machine as it is now.
type resumer struct {
	agent.Module
	cmd agent.Command
	err error
}

func (r resumer) ResumeOn(context.Context, agent.Host, agent.Install, agent.SessionKey, agent.Placement, agent.ResumeOptions) (agent.Command, error) {
	return r.cmd, r.err
}

// Once the session is there, the target gives the resume command. A command it refuses
// (OpenCode's would restart a background service of another version) is shown with the
// reason but is not runnable: neither the result nor the plan carries it to a launch.
func TestFinishResume(t *testing.T) {
	planned := agent.Command{Argv: []string{"opencode", "-s", "ses_1"}}
	now := agent.Command{Argv: []string{"/Applications/OpenCode.app/opencode-cli", "-s", "ses_1"}}

	p := &Plan{Resume: planned}
	res := &Result{}
	finishResume(context.Background(), p, Side{Module: resumer{cmd: now}}, nil, res)
	if len(res.Run.Argv) == 0 || res.Run.Argv[0] != now.Argv[0] || p.Resume.Argv[0] != now.Argv[0] || !strings.Contains(res.Command, "opencode-cli") || len(res.Warnings) != 0 {
		t.Fatalf("the target's command: %+v %+v %q %v", res.Run, p.Resume, res.Command, res.Warnings)
	}

	p = &Plan{Resume: planned}
	res = &Result{}
	finishResume(context.Background(), p, Side{Module: resumer{err: errors.New("the service runs another version")}}, nil, res)
	if len(res.Run.Argv) != 0 || len(p.Resume.Argv) != 0 {
		t.Fatalf("a refused command is runnable: %+v %+v", res.Run, p.Resume)
	}
	if !strings.Contains(res.Command, "opencode") || !strings.Contains(res.Command, "ses_1") || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "another version") {
		t.Fatalf("a refused command: %q %v", res.Command, res.Warnings)
	}
	if res.RestartsService {
		t.Fatal("another refusal was taken for a service restart")
	}
	// A refusal because it would restart the agent's background service is marked, so an
	// interface asks before opening it (with the plan's options).
	p = &Plan{Resume: planned, resumeOpts: agent.ResumeOptions{Prompt: "go on"}}
	res = &Result{}
	finishResume(context.Background(), p, Side{Module: resumer{err: fmt.Errorf("the service runs 2.2.5: %w", agent.ErrRestartsService)}}, nil, res)
	if len(res.Run.Argv) != 0 || !res.RestartsService || p.ResumeOptions().Prompt != "go on" {
		t.Fatalf("a restart: %+v %v", res, p.ResumeOptions())
	}

	// The desktop app's command is the module's own: not asked again.
	p = &Plan{Resume: planned, resumeOpts: agent.ResumeOptions{App: true}}
	res = &Result{}
	finishResume(context.Background(), p, Side{Module: resumer{err: errors.New("refused")}}, nil, res)
	if len(res.Run.Argv) == 0 || len(res.Warnings) != 0 {
		t.Fatalf("the app's command: %+v %v", res.Run, res.Warnings)
	}
}
