package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/clientapi"
	"github.com/roeehrl/hopsesh/internal/core/move"
)

func TestPlanNotes(t *testing.T) {
	for _, c := range []struct {
		text, want, fix string
		action          clientapi.PlanAction
	}{
		{"this session is already here, in this folder; to move it to another folder on this machine use --to <dir>",
			"this session is already here, in this folder", "choose another folder on this machine",
			clientapi.PlanAction{Kind: "plan", Ask: []string{"dir"}}},
		{"hopsesh could not read the session's git checkout on demo2 (timeout); try again, or choose a folder here with --to",
			"hopsesh could not read the session's git checkout on demo2 (timeout)", "try again, or choose a folder for it here",
			clientapi.PlanAction{Kind: "plan"}},
		{"the session's folder is not a git repository with a remote, so hopsesh cannot match or clone it, and /w is not on this machine; choose a folder here with --to",
			"the session's folder is not a git repository with a remote, so hopsesh cannot match or clone it, and /w is not on this machine", "choose a folder for it here",
			clientapi.PlanAction{Kind: "plan", Ask: []string{"dir"}}},
		{"github.com/a/b is not cloned on this machine; clone it into /r/b (pass --clone, or clone it yourself and pass --to)",
			"github.com/a/b is not cloned on this machine", "let hopsesh clone it into /r/b, or choose a folder where it is cloned",
			clientapi.PlanAction{Kind: "plan", Params: map[string]any{"clone": true}}},
		{"a, b are not cloned on this machine; clone them into /w (pass --clone, or clone them yourself)",
			"a, b are not cloned on this machine", "let hopsesh clone them into /w",
			clientapi.PlanAction{Kind: "plan", Params: map[string]any{"clone": true}}},
		{"this session is open on this machine (busy); quit it first, or let hopsesh quit it (--stop-local)",
			"this session is open on this machine (busy)", "quit it, or let hopsesh quit it",
			clientapi.PlanAction{Kind: "plan", Params: map[string]any{"stopOpen": true}}},
		{"a copy is here; choose --replace (the copy here is kept for undo) or --keep-both (bring this one in as a separate session)",
			"a copy is here", "replace the copy here (it is kept for undo), or keep both, bringing this one in as a separate session",
			clientapi.PlanAction{Kind: "plan", Params: map[string]any{"conflict": "replace"}}},
		{"destination has independent work; use --keep-both to preserve a separate branch",
			"destination has independent work", "keep both, as separate branches",
			clientapi.PlanAction{Kind: "plan", Params: map[string]any{"conflict": "keep-both"}}},
		{move.HistoryChangedBlocker("history was compacted or rewound"),
			"this conversation's history no longer matches what hopsesh recorded when it last moved it (history was compacted or rewound)",
			"take it as a separate conversation from where it is now; the copies made before stay as they are",
			clientapi.PlanAction{Kind: "plan", Params: map[string]any{"conflict": "keep-both"}}},
		{"it diverged; choose --keep-both", "it diverged", "keep both",
			clientapi.PlanAction{Kind: "plan", Params: map[string]any{"conflict": "keep-both"}}},
		{"the local checkout is on main but the session was on fix; use --worktree create to work on fix in a separate worktree",
			"the local checkout is on main but the session was on fix", "work on fix in a separate worktree",
			clientapi.PlanAction{Kind: "plan", Params: map[string]any{"worktree": "create"}}},
		{"your instructions for every Claude Code project (~/.claude/CLAUDE.md) do not carry over to codex; --carry-rules adds them to the briefing",
			"your instructions for every Claude Code project (~/.claude/CLAUDE.md) do not carry over to codex", "add them to the briefing",
			clientapi.PlanAction{Kind: "plan", Params: map[string]any{"carryRules": true}}},
		{"the original on demo2 won't be protected: without hopsesh there, nothing stops it from being continued on demo2 too, and the two copies drift apart; set up hopsesh on demo2 first to protect it",
			"the original on demo2 won't be protected: without hopsesh there, nothing stops it from being continued on demo2 too, and the two copies drift apart", "set up hopsesh on demo2 first",
			clientapi.PlanAction{Kind: "setup", Machine: "demo2"}},
		{"this session is large (300 MB)", "this session is large (300 MB)", "", clientapi.PlanAction{}},
	} {
		n := planNote(clientapi.NoteWarning, c.text)
		if n.Text != sentence(c.want) || n.Fix != sentence(c.fix) {
			t.Errorf("%q:\n text %q\n fix  %q", c.text, n.Text, n.Fix)
			continue
		}
		if strings.Contains(n.Text, "--") || strings.Contains(n.Fix, "--") {
			t.Errorf("a flag is left in %+v", n)
		}
		if c.action.Kind == "" {
			if len(n.Actions) != 0 {
				t.Errorf("%q: no actions expected, got %+v", c.text, n.Actions)
			}
			continue
		}
		a := n.Actions[0]
		if a.Kind != c.action.Kind || a.Machine != c.action.Machine || a.Label == "" ||
			strings.Join(a.Ask, ",") != strings.Join(c.action.Ask, ",") || len(a.Params) != len(c.action.Params) {
			t.Errorf("%q: action %+v, want %+v", c.text, a, c.action)
		}
		for k, v := range c.action.Params {
			if a.Params[k] != v {
				t.Errorf("%q: param %s = %v, want %v", c.text, k, a.Params[k], v)
			}
		}
	}

	notes := planNotes([]string{"a blocker; choose --keep-both"}, []string{"w1"})
	if len(notes) != 2 || notes[0].Level != "blocker" || notes[1].Level != "warning" {
		t.Fatalf("notes: %+v", notes)
	}
	if got := noteTexts(notes, "blocker"); len(got) != 1 || got[0] != "A blocker." {
		t.Fatalf("blocker texts: %v", got)
	}
	if e := blockedError(notes); e.Code != clientapi.CodeBlocked || e.Message != "A blocker." || e.Fix != "Keep both." {
		t.Fatalf("blocked: %+v", e)
	}
}

func TestSentence(t *testing.T) {
	for in, want := range map[string]string{
		"the original on demo2 won't be protected: x":                             "The original on demo2 won't be protected: x.",
		"set up hopsesh on demo2 first":                                           "Set up hopsesh on demo2 first.",
		"Claude Code was not found on this machine; install it before continuing": "Claude Code was not found on this machine; install it before continuing.",
		"hopsesh could not read it":                                               "hopsesh could not read it.",
		"github.com/a/b is not cloned on this machine":                            "github.com/a/b is not cloned on this machine.",
		"this session is large (300 MB)":                                          "This session is large (300 MB).",
		"the copy here is set aside: /Users/x/s.jsonl":                            "The copy here is set aside: /Users/x/s.jsonl",
		"Already one.": "Already one.",
		"":             "",
	} {
		if got := sentence(in); got != want {
			t.Errorf("sentence(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPlanNoteFlags fails when the planner names a flag in a warning or blocker that the
// note rules don't turn into words: add a rule for it (and a param, if a client can set it).
func TestPlanNoteFlags(t *testing.T) {
	handled := map[string]bool{"--to": true, "--clone": true, "--stop-local": true, "--replace": true,
		"--keep-both": true, "--worktree": true, "--carry-rules": true,
		// set only by options a client can't choose, so a client's plan never has them
		"--via": true, "--target-session": true, "--new-session": true}
	files, _ := filepath.Glob("../core/move/*.go")
	appendRe := regexp.MustCompile(`(Blockers|Warnings)\s*=\s*append\(.*`)
	flagRe := regexp.MustCompile(`--[a-z][a-z-]*`)
	n := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range appendRe.FindAllString(string(b), -1) {
			for _, flag := range flagRe.FindAllString(line, -1) {
				n++
				if !handled[flag] {
					t.Errorf("%s: %s in %q has no note rule", filepath.Base(f), flag, strings.TrimSpace(line))
				}
			}
		}
	}
	if n == 0 {
		t.Fatal("found no flags in the planner's notes: is the path right?")
	}
}
