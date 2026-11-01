package app

import (
	"regexp"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/clientapi"
)

// The planner writes its warnings and blockers for the command line, naming its flags. A
// client gets each as a note: the same facts without the flags, what the user can do, and
// the actions that do it (plan again with a param set, or set hopsesh up on a machine).

type noteRule struct {
	re *regexp.Regexp
	// note makes the note from the submatches (m[0] the whole text).
	note func(m []string) (text, fix string, actions []clientapi.PlanAction)
}

func replan(label string, params map[string]any, ask ...string) clientapi.PlanAction {
	return clientapi.PlanAction{Kind: clientapi.ActionPlan, Label: label, Params: params, Ask: ask}
}

var chooseDir = replan("Choose a folder", nil, "dir")

var noteRules = []noteRule{
	{regexp.MustCompile(`^(this session is already here, in this folder); to move it to another folder on this machine use --to <dir>$`),
		func(m []string) (string, string, []clientapi.PlanAction) {
			return m[1], "choose another folder on this machine", []clientapi.PlanAction{replan("Choose another folder", nil, "dir")}
		}},
	{regexp.MustCompile(`^(.*); try again, or choose a folder here with --to$`),
		func(m []string) (string, string, []clientapi.PlanAction) {
			return m[1], "try again, or choose a folder for it here", []clientapi.PlanAction{replan("Try again", nil), chooseDir}
		}},
	{regexp.MustCompile(`^(.*); choose a folder here with --to$`),
		func(m []string) (string, string, []clientapi.PlanAction) {
			return m[1], "choose a folder for it here", []clientapi.PlanAction{chooseDir}
		}},
	{regexp.MustCompile(`^(.*) is not cloned on this machine; clone it into (.*) \(pass --clone, or clone it yourself and pass --to\)$`),
		func(m []string) (string, string, []clientapi.PlanAction) {
			return m[1] + " is not cloned on this machine", "let hopsesh clone it into " + m[2] + ", or choose a folder where it is cloned",
				[]clientapi.PlanAction{replan("Clone it", map[string]any{"clone": true}), chooseDir}
		}},
	{regexp.MustCompile(`^(.*) (is|are) not cloned on this machine; clone (it|them) into (.*) \(pass --clone, or clone (it|them) yourself\)$`),
		func(m []string) (string, string, []clientapi.PlanAction) {
			return m[1] + " " + m[2] + " not cloned on this machine", "let hopsesh clone " + m[3] + " into " + m[4],
				[]clientapi.PlanAction{replan("Clone "+m[3], map[string]any{"clone": true})}
		}},
	{regexp.MustCompile(`^(.*); quit it first, or let hopsesh quit it \(--stop-local\)$`),
		func(m []string) (string, string, []clientapi.PlanAction) {
			return m[1], "quit it, or let hopsesh quit it", []clientapi.PlanAction{replan("Quit it", map[string]any{"stopOpen": true})}
		}},
	{regexp.MustCompile(`^(.*); choose --replace \(the copy here is kept for undo\) or --keep-both \(bring this one in as a separate session\)$`),
		func(m []string) (string, string, []clientapi.PlanAction) {
			return m[1], "replace the copy here (it is kept for undo), or keep both, bringing this one in as a separate session",
				[]clientapi.PlanAction{replan("Replace it", map[string]any{"conflict": "replace"}), replan("Keep both", map[string]any{"conflict": "keep-both"})}
		}},
	{regexp.MustCompile(`^(.*); use --keep-both to preserve a separate branch$`),
		func(m []string) (string, string, []clientapi.PlanAction) {
			return m[1], "keep both, as separate branches", []clientapi.PlanAction{replan("Keep both", map[string]any{"conflict": "keep-both"})}
		}},
	{regexp.MustCompile(`^(.*); choose --keep-both$`),
		func(m []string) (string, string, []clientapi.PlanAction) {
			return m[1], "keep both", []clientapi.PlanAction{replan("Keep both", map[string]any{"conflict": "keep-both"})}
		}},
	{regexp.MustCompile(`^(.*); use --worktree create to work on (.*) in a separate worktree$`),
		func(m []string) (string, string, []clientapi.PlanAction) {
			return m[1], "work on " + m[2] + " in a separate worktree", []clientapi.PlanAction{replan("Use a worktree", map[string]any{"worktree": "create"})}
		}},
	{regexp.MustCompile(`^(.*); --carry-rules adds them to the briefing$`),
		func(m []string) (string, string, []clientapi.PlanAction) {
			return m[1], "add them to the briefing", []clientapi.PlanAction{replan("Add them", map[string]any{"carryRules": true})}
		}},
	{regexp.MustCompile(`^(the original on (.+) won't be protected: .*); set up hopsesh on .+ first to protect it$`),
		func(m []string) (string, string, []clientapi.PlanAction) {
			return m[1], "set up hopsesh on " + m[2] + " first",
				[]clientapi.PlanAction{{Kind: clientapi.ActionSetup, Label: "Set up hopsesh on " + m[2], Machine: m[2]}}
		}},
}

// planNote is a plan's warning or blocker as a client gets it.
func planNote(level, text string) clientapi.PlanNote {
	for _, r := range noteRules {
		if m := r.re.FindStringSubmatch(text); m != nil {
			t, fix, actions := r.note(m)
			return clientapi.PlanNote{Level: level, Text: t, Fix: fix, Actions: actions}
		}
	}
	return clientapi.PlanNote{Level: level, Text: text}
}

// planNotes are a plan's blockers, then its warnings, as notes.
func planNotes(blockers, warnings []string) []clientapi.PlanNote {
	var out []clientapi.PlanNote
	for _, b := range blockers {
		out = append(out, planNote(clientapi.NoteBlocker, b))
	}
	for _, w := range warnings {
		out = append(out, planNote(clientapi.NoteWarning, w))
	}
	return out
}

// noteTexts are the notes' texts at one level.
func noteTexts(notes []clientapi.PlanNote, level string) []string {
	var out []string
	for _, n := range notes {
		if n.Level == level {
			out = append(out, n.Text)
		}
	}
	return out
}

// blockedError is apply's refusal of a plan with blockers.
func blockedError(notes []clientapi.PlanNote) *clientapi.Error {
	var texts, fixes []string
	for _, n := range notes {
		if n.Level == clientapi.NoteBlocker {
			texts = append(texts, n.Text)
			if n.Fix != "" {
				fixes = append(fixes, n.Fix)
			}
		}
	}
	e := clientapi.Errorf(clientapi.CodeBlocked, "%s", strings.Join(texts, "; "))
	e.Fix = strings.Join(fixes, "; ")
	return e
}
