package tui

import (
	"strings"
	"testing"
)

func TestWrapCommandKeepsQuotesWhole(t *testing.T) {
	cmd := `cd /home/alice/src/infra && claude --resume 7bb8b694-d138-43fd-9779-ece6507aea70 --remote-control terraform@laptop "$(cat /home/alice/.local/state/hopsesh/prompts/7bb8.md)"`
	lines := wrapCommand(cmd, 60, "posix")
	if len(lines) < 3 {
		t.Fatalf("expected wrapping: %q", lines)
	}
	for i, l := range lines {
		if len(l) > 60 && !strings.Contains(l, "$(cat") {
			t.Errorf("line %d too long: %q", i, l)
		}
		if i < len(lines)-1 && !strings.HasSuffix(l, ` \`) {
			t.Errorf("line %d lacks a continuation: %q", i, l)
		}
	}
	joined := strings.ReplaceAll(strings.Join(lines, "\n"), " \\\n    ", " ")
	if joined != cmd {
		t.Errorf("rejoined command differs:\n%s\n%s", joined, cmd)
	}
	if got := wrapCommand("claude --resume x", 80, "powershell"); len(got) != 1 {
		t.Errorf("short command must stay on one line: %q", got)
	}
}
