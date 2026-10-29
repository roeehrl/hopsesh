package move

import (
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Remote Control asked for an opening in the desktop app is not a blocker: the launcher
// can't start it, so the plan says so once and the window checks after the app opens.
func TestAppRemoteControl(t *testing.T) {
	p := &Plan{resumeOpts: agent.ResumeOptions{App: true, RemoteControl: true}}
	p.appRemoteControl()
	p.appRemoteControl() // checked again when writing: still one note
	if p.resumeOpts.RemoteControl || !p.RemoteControlInApp || len(p.Warnings) != 1 {
		t.Fatalf("%+v %v", p.resumeOpts, p.Warnings)
	}
	terminal := &Plan{resumeOpts: agent.ResumeOptions{RemoteControl: true}}
	terminal.appRemoteControl()
	if !terminal.resumeOpts.RemoteControl || terminal.RemoteControlInApp || len(terminal.Warnings) != 0 {
		t.Fatalf("a terminal opening keeps --remote-control: %+v", terminal)
	}
}
