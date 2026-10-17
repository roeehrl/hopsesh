package codex

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// The Codex app runs one app-server for every thread: hopsesh reports such a thread as
// open in the app and never signals that process (or any shared app-server).
func TestStopNeverClosesTheApp(t *testing.T) {
	m := New()
	ctx := context.Background()
	lock := "/home/u/.codex/thread-writer-locks/" + t1 + ".lock"
	const server = "/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex -c features.code_mode_host=true app-server --analytics-default-enabled"
	for _, tc := range []struct {
		name, proc, command, originator string
		app, others                     bool
		want                            error
	}{
		{"app-server inside the bundle", "codex", server, "codex_cli_rs", true, false, ErrInApp},
		{"app process", "Codex", "", "codex_cli_rs", true, false, ErrInApp},
		{"desktop thread, commands unknown", "codex", "", "Codex Desktop", true, false, ErrInApp},
		{"work thread", "codex", "", "codex_work_desktop", true, false, ErrInApp},
		{"shared daemon holding other threads", "codex", "/opt/homebrew/bin/codex app-server --listen unix://", "codex_cli_rs", false, true, ErrSharedServer},
		{"a CLI's daemon holding only this thread", "codex", "/opt/homebrew/bin/codex app-server --listen unix://", "codex_cli_rs", false, false, nil},
		{"extension client", "codex", "", "codex_vscode", false, false, ErrSharedServer},
		{"CLI, any case", "CODEX", "/opt/homebrew/bin/codex resume " + t1, "codex_cli_rs", false, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fh := newHost(t)
			fh.LockHolders[lock] = []int{4242}
			fh.PIDs[4242], fh.PIDNames[4242] = true, tc.proc
			fh.PIDCommands = map[int]string{}
			if tc.command != "" {
				fh.PIDCommands[4242] = tc.command
			}
			if tc.others {
				other := "/home/u/.codex/thread-writer-locks/other-thread.lock"
				fh.Put(other, nil, time.Now())
				fh.LockHolders[other], fh.LocksHeld[other] = []int{4242}, true
			}
			in, _ := m.Detect(ctx, fh)
			in.Desktop = "/Applications/ChatGPT.app"
			h := agent.Confine(fh, m.Spec(), in)
			l, _ := m.List(ctx, h, in)
			var s agent.Summary
			for _, x := range l.Sessions {
				if string(x.Key.Session) == t1 {
					s = x
				}
			}
			b, _ := fh.Get(s.Path)
			fh.Put(s.Path, bytes.Replace(b, []byte(`"originator":"codex_cli_rs"`), []byte(`"originator":"`+tc.originator+`"`), 1), time.Now())
			live, err := m.Live(ctx, h, in, []agent.SessionID{t1})
			if err != nil || live[t1].App != tc.app || len(live[t1].Procs) != 1 || live[t1].Procs[0].App != tc.app {
				t.Fatalf("live: %+v %v", live[t1], err)
			}
			err = m.Stop(ctx, h, in, s, time.Second)
			if !errors.Is(err, tc.want) || (tc.want != nil) != fh.PIDs[4242] {
				t.Fatalf("stop: %v (alive %v)", err, fh.PIDs[4242])
			}
			if tc.want == ErrInApp && !strings.Contains(err.Error(), "open it in ChatGPT desktop") {
				t.Fatalf("message: %v", err)
			}
		})
	}
}
