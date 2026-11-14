package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Decision D2 in hopsesh open: a resume that would restart the agent's background
// service is asked about on a terminal (only y or yes agrees, and then it resumes with
// the agreement), refused elsewhere with how to agree, and not asked again after
// --restart-service.
func TestOpenAsksBeforeRestartingTheService(t *testing.T) {
	refusing := func(calls *[]bool) func(bool) (agent.Command, error) {
		return func(restart bool) (agent.Command, error) {
			*calls = append(*calls, restart)
			if !restart {
				return agent.Command{}, fmt.Errorf("the service runs 2.2.7: %w", agent.ErrRestartsService)
			}
			return agent.Command{Argv: []string{"opencode", "-s", "ses_x"}}, nil
		}
	}
	old := isTerminal
	defer func() { isTerminal = old }()
	ask := func(answer string, tty bool) (agent.Command, []bool, string, error) {
		in, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer in.Close()
		fmt.Fprint(w, answer)
		w.Close()
		isTerminal = func(int) bool { return tty }
		var out bytes.Buffer
		var calls []bool
		c, err := (&run{in: in, out: &out}).resumeAsking("Fix it", false, refusing(&calls))
		return c, calls, out.String(), err
	}
	for _, yes := range []string{"y\n", "Yes\n", " YES \n"} {
		c, calls, out, err := ask(yes, true)
		if err != nil || len(c.Argv) == 0 || len(calls) != 2 || !calls[1] || !strings.Contains(out, `Restart it and open "Fix it"? [y/N]`) {
			t.Errorf("%q: %v %v %v %q", yes, c, err, calls, out)
		}
	}
	for _, no := range []string{"\n", "n\n", "yep\n", ""} {
		if _, calls, _, err := ask(no, true); err == nil || err.Error() != "nothing was opened" || len(calls) != 1 {
			t.Errorf("%q: %v %v", no, err, calls)
		}
	}
	// Not on a terminal: refused, saying how to agree; nothing read, nothing resumed.
	if _, calls, out, err := ask("y\n", false); !errors.Is(err, agent.ErrRestartsService) || !strings.Contains(err.Error(), "--restart-service") || len(calls) != 1 || out != "" {
		t.Errorf("no terminal: %v %v %q", err, calls, out)
	}
	// --restart-service: resumed with the agreement, nothing asked.
	var calls []bool
	var out bytes.Buffer
	if c, err := (&run{out: &out}).resumeAsking("Fix it", true, refusing(&calls)); err != nil || len(c.Argv) == 0 || len(calls) != 1 || !calls[0] || out.Len() != 0 {
		t.Errorf("agreed: %v %v %v %q", c, err, calls, out.String())
	}
}
