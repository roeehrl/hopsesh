package gui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Decision D2: a resume that would restart the agent's background service comes to the
// window marked, so it asks; the person's yes holds for that session's next resume only,
// and not for long.
func TestServiceRestartIsAsked(t *testing.T) {
	err := askRestart(fmt.Errorf("opening it: %w", agent.ErrRestartsService))
	if !strings.HasPrefix(err.Error(), restartAsk) || !errors.Is(err, agent.ErrRestartsService) {
		t.Fatalf("%v", err)
	}
	if other := errors.New("x"); askRestart(other) != other {
		t.Fatal("another error was marked")
	}
	a := &App{}
	if a.resumeOptions("m", "opencode/s", agent.ResumeOptions{}).RestartService {
		t.Fatal("agreed without asking")
	}
	a.AllowServiceRestart("m", "opencode/s")
	if a.resumeOptions("m", "opencode/other", agent.ResumeOptions{}).RestartService {
		t.Fatal("the yes reached another session")
	}
	if o := a.resumeOptions("m", "opencode/s", agent.ResumeOptions{Prompt: "p"}); !o.RestartService || o.Prompt != "p" {
		t.Fatalf("the yes was lost: %+v", o)
	}
	if a.resumeOptions("m", "opencode/s", agent.ResumeOptions{}).RestartService {
		t.Fatal("the yes held twice")
	}
	a.AllowServiceRestart("m", "opencode/s")
	a.restartOK["m\x00opencode/s"] = time.Now().Add(-restartWindow - time.Second)
	if a.resumeOptions("m", "opencode/s", agent.ResumeOptions{}).RestartService {
		t.Fatal("an old yes held")
	}
}
