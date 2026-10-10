package tui

import (
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// c on a session opens Hand off to ▸ (a cloud hopsesh cannot use says why), enter plans
// it, the plan pane says what goes and what stays, u carries the untracked files, L lists
// the loss, and the done view gives the session's link as an OSC 8 link and its id.
func TestHandoffFromTheList(t *testing.T) {
	m, _ := cloudModel(t)
	steps := make(chan tea.Msg, 1)
	m.deps.App.Steps = stepper(func(msg tea.Msg) { steps <- msg })
	m.steps = steps
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
	for _, want := range []string{"Hand off to ▸", "claude-cloud", "Gets a briefing and the code on a branch", "codex-cloud", "✕ turned off. Turn it on in Machines."} {
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
		"Claude Code starts the session in a terminal", "The folder: ",
		"Also commit the conversation as .hopsesh/handoff.md", "Cloud sessions use your plan's allowance.",
		"enter hand off · b briefing in $EDITOR · u untracked files · L loss list · esc back"} {
		if !strings.Contains(v, want) {
			t.Errorf("the plan pane lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "Mark this session") || strings.Contains(v, "[m]") {
		t.Errorf("the plan pane offers to label the title:\n%s", v)
	}
	m.key("L")
	if v = ansi.Strip(m.View().Content); !strings.Contains(v, "No tool output goes up") {
		t.Errorf("L lists the loss:\n%s", v)
	}
	_, cmd = m.key("enter")
	if cmd == nil {
		t.Fatal("enter hands it off")
	}
	m.Update(handOff(t, m, cmd, ""))
	if m.mode != modeDone || m.result == nil || m.result.Handoff == nil {
		t.Fatalf("done: %v %v", m.mode, m.err)
	}
	raw := m.View().Content
	v = ansi.Strip(raw)
	r := m.result.Handoff
	for _, want := range []string{"✓ Handed off to Claude Code cloud", "id      " + r.Session,
		"When it finishes: select the claude-cloud row and press enter to bring it here.", "o open in browser · y copy link · u undo",
		"hopsesh can't send a Claude Code cloud session a message"} {
		if !strings.Contains(v, want) {
			t.Errorf("the done view lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "marked") {
		t.Errorf("the done view says the title was labelled:\n%s", v)
	}
	if !strings.Contains(raw, "\x1b]8;;"+r.URL) {
		t.Error("the session's link is an OSC 8 link")
	}
	_, cmd = m.key("u")
	if msg := cmd(); msg.(undoDone).err != nil {
		t.Fatalf("undo: %v", msg.(undoDone).err)
	}
}

// handOff runs the hand-off cmd as the program would: the hand-off asks the UI for the
// terminal step, which the relay runs (here with nothing typed, its screen discarded, as
// tea.Exec would with the real terminal), and the UI answers it, typing paste at the
// link prompt when the step showed no link. It returns the hand-off's end.
func handOff(t *testing.T, m *model, cmd tea.Cmd, paste string) tea.Msg {
	t.Helper()
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	run := (<-m.steps).(stepRun)
	r, fn := m.stepExec(run)
	r.SetStdin(strings.NewReader(""))
	r.SetStdout(io.Discard)
	m.Update(fn(r.Run()))
	if m.stepPaste != nil {
		v := ansi.Strip(m.View().Content)
		if !strings.Contains(v, "hopsesh saw no Claude Code cloud link") || !strings.Contains(v, "paste its link and press enter") {
			t.Errorf("the link prompt:\n%s", v)
		}
		m.Update(tea.PasteMsg{Content: paste})
		m.key("enter")
	}
	return <-done
}

// A step that shows no link (the user said no to Claude Code's question): the UI asks for
// the link; one pasted finishes the hand-off with it, and none stops it at the start step
// with the branch for undo.
func TestHandoffStepAsksForTheLink(t *testing.T) {
	m, s := cloudModel(t)
	steps := make(chan tea.Msg, 1)
	m.deps.App.Steps = stepper(func(msg tea.Msg) { steps <- msg })
	m.steps = steps
	m.width, m.height = 180, 60
	m.Update(m.Init()())
	defer m.inv.Close()
	t.Setenv("FAKE_CLOUD_FAIL", "trust-no")
	for _, paste := range []string{"https://claude.ai/code/" + s.ID, ""} {
		for i, r := range m.rows {
			if r.item != nil && r.item.Entry.Session.Title == "Find the codeword" {
				m.cursor = i
			}
		}
		m.mode = modeBrowse
		m.key("c")
		_, cmd := m.key("enter")
		m.Update(cmd())
		_, cmd = m.key("enter")
		if cmd == nil {
			t.Fatalf("enter hands it off: %v", m.plan.Blockers)
		}
		m.Update(handOff(t, m, cmd, paste))
		r := m.result.Handoff
		if paste != "" {
			if r.Failed != "" || r.Session != s.ID || !r.Pasted || !strings.Contains(ansi.Strip(m.View().Content), "(from the link you pasted)") {
				t.Fatalf("pasted: %+v", r)
			}
			m.key("u")
			continue
		}
		if r.Failed != "start" || !strings.Contains(r.Message, "No session started: Claude Code asked whether you trust") {
			t.Fatalf("no link: %+v", r)
		}
	}
}
