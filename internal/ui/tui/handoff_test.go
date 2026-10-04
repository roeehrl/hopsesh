package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// c on a session opens Hand off to ▸ (a cloud hopsesh cannot use says why), enter plans
// it, the plan pane says what goes and what stays, u carries the untracked files, L lists
// the loss, and the done view gives the session's link as an OSC 8 link and its id.
func TestHandoffFromTheList(t *testing.T) {
	m, _ := cloudModel(t)
	m.width, m.height = 180, 60
	m.Update(m.Init()())
	defer m.inv.Close()
	for i, r := range m.rows {
		if r.item != nil && r.item.Entry.Session.Title == "Find the codeword" {
			m.cursor = i
		}
	}
	m.key("c")
	if !m.ho.picking {
		t.Fatal("c opens the picker")
	}
	v := ansi.Strip(m.View().Content)
	for _, want := range []string{"Hand off to ▸", "claude-cloud", "Gets a briefing and the code on a branch", "codex-cloud", "✕ hopsesh does not reach Codex cloud yet"} {
		if !strings.Contains(v, want) {
			t.Errorf("the picker lacks %q:\n%s", want, v)
		}
	}
	_, cmd := m.key("enter")
	if cmd == nil {
		t.Fatal("enter plans the hand-off")
	}
	m.Update(cmd())
	if m.mode != modePlan || m.plan.Handoff == nil {
		t.Fatalf("plan: %v %v", m.mode, m.err)
	}
	v = ansi.Strip(m.View().Content)
	for _, want := range []string{"Hand off to Claude Code cloud", "The cloud agent receives a briefing, not this conversation.", "Briefing ", "tokens", "[b] view/edit",
		"Also commit the conversation as .hopsesh/handoff.md", "Mark this session \"continued in Claude Code on claude-cloud\"", "Cloud sessions use your plan's allowance.",
		"enter hand off · b briefing in $EDITOR · u untracked files · L loss list · esc back"} {
		if !strings.Contains(v, want) {
			t.Errorf("the plan pane lacks %q:\n%s", want, v)
		}
	}
	m.key("L")
	if v = ansi.Strip(m.View().Content); !strings.Contains(v, "No tool output goes up") {
		t.Errorf("L lists the loss:\n%s", v)
	}
	_, cmd = m.key("enter")
	if cmd == nil {
		t.Fatal("enter hands it off")
	}
	m.Update(cmd())
	if m.mode != modeDone || m.result == nil || m.result.Handoff == nil {
		t.Fatalf("done: %v %v", m.mode, m.err)
	}
	raw := m.View().Content
	v = ansi.Strip(raw)
	r := m.result.Handoff
	for _, want := range []string{"✓ Handed off to Claude Code cloud", "id      " + r.Session, "↪ this session is now marked “continued in Claude Code on claude-cloud”",
		"When it finishes: select the claude-cloud row and press enter to bring it here.", "o open in browser · y copy link · u undo"} {
		if !strings.Contains(v, want) {
			t.Errorf("the done view lacks %q:\n%s", want, v)
		}
	}
	if !strings.Contains(raw, "\x1b]8;;"+r.URL) {
		t.Error("the session's link is an OSC 8 link")
	}
	_, cmd = m.key("u")
	if msg := cmd(); msg.(undoDone).err != nil {
		t.Fatalf("undo: %v", msg.(undoDone).err)
	}
}
