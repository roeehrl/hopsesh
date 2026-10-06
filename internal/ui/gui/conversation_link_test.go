package gui

import "testing"

func TestConversationLinkRejectsNonWebTargets(t *testing.T) {
	a := &App{}
	for _, raw := range []string{"javascript:alert(1)", "file:///tmp/example", "claude://open", "//example.com", "https://", "https://example.com/\x00"} {
		if err := a.OpenConversationLink(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}
