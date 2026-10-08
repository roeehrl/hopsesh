package gui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/presence"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// A title without a model: the one it was given, the running agent's name, the agent's
// own, the first prompt or reply (clipped at a word), the last prompt, then the folder.
func TestTitleFor(t *testing.T) {
	long := strings.Repeat("word ", 30)
	for _, tc := range []struct {
		s          agent.Summary
		live, want string
		source     string
	}{
		{agent.Summary{Title: "Fix it", TitleSource: "custom"}, "app name", "Fix it", "custom"},
		{agent.Summary{Title: "AI title", TitleSource: "generated"}, "app name", "app name", "live"},
		{agent.Summary{Title: "AI title", TitleSource: "generated"}, "", "AI title", "generated"},
		{agent.Summary{Title: "thread name", TitleSource: "custom"}, "", "thread name", "custom"},
		{agent.Summary{Title: long, TitleSource: "prompt"}, "", strings.TrimSpace(strings.Repeat("word ", 15)) + "…", "prompt"},
		{agent.Summary{Title: "First sentence of the reply", TitleSource: "reply"}, "", "First sentence of the reply", "reply"},
		{agent.Summary{LastPrompt: "only a last prompt\nand more"}, "", "only a last prompt", "prompt"},
		{agent.Summary{CWD: "/home/u/git/hopsesh", GitBranch: "main"}, "", "Untitled · hopsesh (main)", "none"},
		{agent.Summary{CWD: `C:\Users\u\git\api\`}, "", "Untitled · api", "none"},
		{agent.Summary{}, "", "Untitled", "none"},
	} {
		got, src := titleFor(tc.s, tc.live)
		if got != tc.want || src != tc.source {
			t.Errorf("titleFor(%+v, %q) = %q, %q; want %q, %q", tc.s, tc.live, got, src, tc.want, tc.source)
		}
	}
	if r := []rune(clipWords(long, 80)); len(r) > 80 {
		t.Errorf("clipped to %d characters", len(r))
	}
}

// Where a live session's processes run: each by its nearest known ancestor, counted by
// place; hopsesh's own tabs are left to the window; the Claude app from the registry.
func TestPlacesOf(t *testing.T) {
	const self = 100
	table := presence.FromProcs([]presence.Proc{
		{PID: 1, PPID: 0, Name: "launchd"},
		{PID: 10, PPID: 1, Name: "iTerm2"}, {PID: 11, PPID: 10, Name: "zsh"}, {PID: 12, PPID: 11, Name: "claude"},
		{PID: 13, PPID: 10, Name: "zsh"}, {PID: 14, PPID: 13, Name: "claude"},
		{PID: 20, PPID: 1, Name: "Code"}, {PID: 21, PPID: 20, Name: "Code Helper (Plu"}, {PID: 22, PPID: 21, Name: "claude"},
		{PID: self, PPID: 1, Name: "hopsesh-app"}, {PID: 101, PPID: self, Name: "claude"},
		{PID: 30, PPID: 1, Name: "Claude"}, {PID: 31, PPID: 30, Name: "claude"},
	})
	li := agent.LiveInfo{State: agent.Live, PID: 12, Procs: []agent.LiveProc{{PID: 12}, {PID: 14, Waiting: true}, {PID: 22}, {PID: 101}, {PID: 31, App: true}}}
	got := placesOf(li, table, self)
	want := []PlaceDTO{{Kind: "iterm2", App: "iTerm2", Count: 2, Waiting: true}, {Kind: "ide", App: "VS Code", Count: 1}, {Kind: "claude-app", Count: 1}}
	if len(got) != len(want) {
		t.Fatalf("places %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].Kind != want[i].Kind || got[i].Count != want[i].Count || got[i].Waiting != want[i].Waiting || (want[i].App != "" && got[i].App != want[i].App) {
			t.Errorf("place %d: %+v, want %+v", i, got[i], want[i])
		}
	}
	// No process list: the main process, or a generic place.
	if p := placesOf(agent.LiveInfo{State: agent.Live, PID: 14}, table, self); len(p) != 1 || p[0].Kind != "iterm2" {
		t.Errorf("by PID: %+v", p)
	}
	if p := placesOf(agent.LiveInfo{State: agent.Live}, nil, self); len(p) != 1 || p[0].Kind != "unknown" {
		t.Errorf("nothing known: %+v", p)
	}
	if p := placesOf(agent.LiveInfo{State: agent.Ended, PID: 12}, table, self); len(p) != 0 {
		t.Errorf("an ended session is open nowhere: %+v", p)
	}
}

// One turn's tool calls in words.
func TestToolWords(t *testing.T) {
	for _, tc := range []struct {
		tools map[string]int
		want  string
	}{
		{map[string]int{"execute": 4, "edit": 2, "move": 1, "read": 6}, "Ran 4 commands · edited 3 files · read 6 files"},
		{map[string]int{"read": 1}, "Read 1 file"},
		{map[string]int{"search": 2, "other": 1, "plan": 1}, "Searched 2 times · 2 other tool calls"},
		{map[string]int{}, ""},
	} {
		if got := toolWords(tc.tools); got != tc.want {
			t.Errorf("toolWords(%v) = %q, want %q", tc.tools, got, tc.want)
		}
	}
}

// The list's display is saved as the window sends it, with the defaults left out, values
// the window does not know refused, and the remembered groups capped.
func TestSaveList(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	a.core.Cfg.List.TerminalCollapsed = []string{"family-one"}
	defer a.Shutdown()
	if l := a.Info().List; l.Decided || l.GroupBy != "repository" || l.SortBy != "last-active" || l.Density != "comfortable" {
		t.Fatalf("defaults: %+v", l)
	}
	many := make([]string, config.ListKeysMax+20)
	for i := range many {
		many[i] = "repository:r" + string(rune('a'+i%26)) + strings.Repeat("x", i)
	}
	in := ListDTO{GroupBy: "status", SortBy: "title", SortReverse: true, Density: "compact", CollapseInactive: true, Collapsed: many,
		Filter: FilterDTO{Status: []string{"working", "idle"}, Location: []string{"clouds"}, LocationNot: true, LastActive: "7d", Has: []string{"tab"}}}
	if err := a.SaveList(in); err != nil {
		t.Fatal(err)
	}
	if len(a.core.Cfg.List.TerminalCollapsed) != 1 {
		t.Fatal("session display overwrote terminal collapse choices")
	}
	l := a.Info().List
	if !l.Decided || l.GroupBy != "status" || !l.SortReverse || l.Density != "compact" || !l.CollapseInactive || len(l.Collapsed) != config.ListKeysMax ||
		l.Collapsed[0] != many[20] || !slices.Equal(l.Filter.Status, []string{"working", "idle"}) || !l.Filter.LocationNot || l.Filter.LastActive != "7d" {
		t.Fatalf("saved: %+v", l)
	}
	if err := a.SaveList(ListDTO{GroupBy: "folder"}); err == nil {
		t.Fatal("an unknown grouping was saved")
	}
	if err := a.SaveList(ListDTO{GroupBy: "repository", SortBy: "last-active"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(config.Path())
	if strings.Contains(string(b), "group_by") || !strings.Contains(string(b), `density = "comfortable"`) {
		t.Fatalf("defaults are left out, and the choice is kept:\n%s", b)
	}
}

// Where an agent's sessions resume is remembered per agent; other agent settings stay.
func TestSetPlace(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	defer a.Shutdown()
	if err := a.SetAgent("codex", true, true, false); err != nil {
		t.Fatal(err)
	}
	if err := a.SetPlace("claude", config.PlaceTerminal); err != nil {
		t.Fatal(err)
	}
	if err := a.SetPlace("codex", config.PlaceApp); err != nil {
		t.Fatal(err)
	}
	if err := a.SetPlace("codex", "browser"); err == nil {
		t.Fatal("an unknown place was saved")
	}
	i := a.Info()
	if i.Places["claude"] != "terminal" || i.Places["codex"] != "app" {
		t.Fatalf("places: %v", i.Places)
	}
	if err := a.SetAgent("codex", true, false, false); err != nil {
		t.Fatal(err)
	}
	if a.Info().Places["codex"] != "app" {
		t.Fatal("changing an agent's options forgot its place")
	}
	if !a.Info().Previews {
		t.Fatal("previews are on by default")
	}
	if err := a.SetPreviews(false); err != nil || a.Info().Previews {
		t.Fatalf("previews off: %v", err)
	}
}

// The inspector's preview of the fixture session, renaming it (its own title, and undo
// from Activity), and the ⋯ menu's copy of its resume command.
func TestPreviewAndRename(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	defer a.Shutdown()
	s, err := a.Scan()
	if err != nil {
		t.Fatal(err)
	}
	var e EntryDTO
	for _, g := range s.Groups {
		for _, x := range g.Entries {
			if x.Agent == "claude" {
				e = x
			}
		}
	}
	if e.Key == "" || !e.CanPreview || !e.CanRename || e.Session == "" || e.Path == "" || len(e.Places) != 0 {
		t.Fatalf("the fixture session: %+v", e)
	}
	p, err := a.Preview(e.Machine, e.Key, 4)
	if err != nil || p.Note != "" || len(p.Items) == 0 {
		t.Fatalf("preview: %+v %v", p, err)
	}
	for _, it := range p.Items {
		if it.Role != "user" && it.Role != "agent" && it.Role != "tools" && it.Role != "compacted" {
			t.Errorf("an item of role %q", it.Role)
		}
	}
	if again, _ := a.Preview(e.Machine, e.Key, 4); len(again.Items) != len(p.Items) {
		t.Fatal("the cached preview differs")
	}
	// A running file changes between scans; the old inventory must not pin the cache.
	f, err := os.OpenFile(e.Path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("\n" + `{"type":"user","uuid":"cache-new","parentUuid":null,"message":{"role":"user","content":"fresh preview without a scan"}}` + "\n")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := a.Preview(e.Machine, e.Key, 4)
	if err != nil || fresh.Note != "" || len(fresh.Items) != 1 || fresh.Items[0].Text != "fresh preview without a scan" {
		t.Fatalf("stale preview: %+v %v", fresh, err)
	}
	if err := a.Rename(e.Machine, e.Key, "Renamed here"); err != nil {
		t.Fatal(err)
	}
	if err := a.Rename(e.Machine, e.Key, "two\nlines"); err == nil {
		t.Fatal("a title with a line break was written")
	}
	s, _ = a.Scan()
	title := ""
	for _, g := range s.Groups {
		for _, x := range g.Entries {
			if x.Key == e.Key {
				title = x.Title
			}
		}
	}
	if title != "Renamed here" {
		t.Fatalf("after renaming: %q", title)
	}
	acts, err := a.Activity()
	if err != nil || len(acts.Items) == 0 || acts.Items[0].Kind != "rename" {
		t.Fatalf("activity: %+v %v", acts, err)
	}
	if err := a.Undo(acts.Items[0].ID, false); err != nil {
		t.Fatal(err)
	}
	cmd, err := a.ResumeCommand(e.Machine, e.Key)
	if err != nil || !strings.Contains(cmd, e.Session) {
		t.Fatalf("resume command: %q %v", cmd, err)
	}
	var revealed string
	SetRevealHook(func(p string) error { revealed = p; return nil })
	defer SetRevealHook(nil)
	if err := a.RevealEntry(e.Machine, e.Key); err != nil || revealed != filepath.FromSlash(e.Path) {
		t.Fatalf("reveal: %q %v", revealed, err)
	}
	// Previews off: a note, nothing read.
	if err := a.SetPreviews(false); err != nil {
		t.Fatal(err)
	}
	if p, _ := a.Preview(e.Machine, e.Key, 4); p.Note == "" || len(p.Items) != 0 {
		t.Fatalf("previews off: %+v", p)
	}
	// Presence: nothing of the fixture runs.
	pr, err := a.Presence()
	if err != nil || len(pr.Entries) == 0 {
		t.Fatalf("presence: %+v %v", pr, err)
	}
	for k, d := range pr.Entries {
		if d.Live || len(d.Places) != 0 {
			t.Errorf("%q runs: %+v", k, d)
		}
	}
	_ = time.Second
}

func TestPlacesOfDuplicatePID(t *testing.T) {
	li := agent.LiveInfo{State: agent.Live, Procs: []agent.LiveProc{{PID: 123}, {PID: 123, Waiting: true}}}
	got := placesOf(li, nil, 0)
	if len(got) != 1 || got[0].Count != 1 || !got[0].Waiting {
		t.Fatalf("%+v", got)
	}
	got = placesOf(agent.LiveInfo{State: agent.Live, Status: "waiting for input"}, nil, 0)
	if len(got) != 1 || !got[0].Waiting {
		t.Fatalf("%+v", got)
	}
}

func TestShowPlaceRejectsUnknown(t *testing.T) {
	if err := new(App).ShowPlace("not an editor"); err == nil {
		t.Fatal("unknown app accepted")
	}
}
