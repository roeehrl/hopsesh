package gui

import (
	"testing"

	"github.com/roeehrl/hopsesh/internal/testkit"
)

// OpenCode counts a session's messages, not its bytes: the window gets the count as
// such, never as a size in kilobytes.
func TestOpenCodeSizesInMessages(t *testing.T) {
	a, _, _ := openCodeWindow(t, "")
	scan, err := a.Scan()
	if err != nil {
		t.Fatal(err)
	}
	oc := findEntry(t, scan, "opencode/"+testkit.DemoOpenCode[0])
	if oc.Messages <= 0 || oc.SizeKB != 0 {
		t.Fatalf("OpenCode's size: %d messages, %d KB", oc.Messages, oc.SizeKB)
	}
	cc := findEntry(t, scan, "claude/"+sid)
	if cc.Messages != 0 || cc.SizeKB <= 0 {
		t.Fatalf("Claude Code's size: %d messages, %d KB", cc.Messages, cc.SizeKB)
	}
}
