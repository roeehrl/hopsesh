package termapp

import (
	"encoding/base64"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Labels name a tab for people: iTerm2 shows them as a badge, and iTerm2 and WezTerm keep
// them as user variables (user.hopsesh_title, …) a title or status bar can show. hopsesh's
// own verb prints them in the tab before the agent starts; it never sets the tab's title,
// which the agent sets itself (Claude Code from --name and /rename).
//
// Labels are for people only. Anything in a tab can print the same sequences, so hopsesh
// never reads them back to decide which session a tab holds (that comes from the agent's
// own files, the process table and hopsesh's launch records).
type Labels struct {
	Title   string `json:"title,omitempty"`
	Agent   string `json:"agent,omitempty"`   // "Claude Code"
	Machine string `json:"machine,omitempty"` // where it runs
}

// fixed are the only labels a sign-in or shell tab gets.
var fixed = map[Kind]Labels{
	KindSignIn: {Title: "hopsesh · sign in"},
	KindShell:  {Title: "hopsesh · shell"},
}

// For is the labels a launch of kind k shows: the launch's own, sanitised, or for a
// sign-in or shell a fixed label whatever was asked.
func (l Labels) For(k Kind) Labels {
	if f, ok := fixed[k]; ok {
		return f
	}
	return Labels{Title: Sanitize(l.Title, 80), Agent: Sanitize(l.Agent, 40), Machine: Sanitize(l.Machine, 40)}
}

// Emulator is a terminal that shows labels, as its environment says.
type Emulator string

const (
	EmulatorNone    Emulator = ""
	EmulatorITerm2  Emulator = "iterm2"
	EmulatorWezTerm Emulator = "wezterm"
)

// DetectEmulator reads the environment: iTerm2 sets TERM_PROGRAM=iTerm.app (and
// LC_TERMINAL=iTerm2, which survives tmux and ssh); WezTerm sets TERM_PROGRAM=WezTerm.
func DetectEmulator(getenv func(string) string) Emulator {
	switch {
	case getenv("TERM_PROGRAM") == "iTerm.app" || getenv("LC_TERMINAL") == "iTerm2":
		return EmulatorITerm2
	case getenv("TERM_PROGRAM") == "WezTerm":
		return EmulatorWezTerm
	}
	return EmulatorNone
}

// Sequences are the bytes that label this tab: user variables (iTerm2 and WezTerm) and a
// badge (iTerm2), wrapped for tmux when $TMUX is set; nil in any other terminal. Values go
// in base64, as the protocol has them; the badge's format is hopsesh's own constant text
// that names the variables, so no value is ever read as a format.
func Sequences(l Labels, getenv func(string) string) []byte {
	em := DetectEmulator(getenv)
	if em == EmulatorNone {
		return nil
	}
	l = Labels{Title: Sanitize(l.Title, 80), Agent: Sanitize(l.Agent, 40), Machine: Sanitize(l.Machine, 40)}
	var seqs []string
	vars := []struct{ k, v string }{{"hopsesh_title", l.Title}, {"hopsesh_agent", l.Agent}, {"hopsesh_machine", l.Machine}}
	for _, kv := range vars {
		seqs = append(seqs, osc1337("SetUserVar="+kv.k+"="+base64.StdEncoding.EncodeToString([]byte(kv.v))))
	}
	if em == EmulatorITerm2 {
		seqs = append(seqs, osc1337("SetBadgeFormat="+base64.StdEncoding.EncodeToString([]byte(badgeFormat(l)))))
	}
	return wrap(seqs, getenv("TMUX") != "")
}

// ClearSequences take the labels off again (when the agent has ended and the tab goes on
// as a shell); nil where Sequences would be.
func ClearSequences(getenv func(string) string) []byte {
	em := DetectEmulator(getenv)
	if em == EmulatorNone {
		return nil
	}
	var seqs []string
	for _, k := range []string{"hopsesh_title", "hopsesh_agent", "hopsesh_machine"} {
		seqs = append(seqs, osc1337("SetUserVar="+k+"="))
	}
	if em == EmulatorITerm2 {
		seqs = append(seqs, osc1337("SetBadgeFormat="))
	}
	return wrap(seqs, getenv("TMUX") != "")
}

// badgeFormat is the badge: the title on one line, agent · machine on the next, each only
// when set (iTerm2 interpolates \(user.…) with the variable's value as plain text).
func badgeFormat(l Labels) string {
	var lines, second []string
	if l.Title != "" {
		lines = append(lines, `\(user.hopsesh_title)`)
	}
	if l.Agent != "" {
		second = append(second, `\(user.hopsesh_agent)`)
	}
	if l.Machine != "" {
		second = append(second, `\(user.hopsesh_machine)`)
	}
	if len(second) > 0 {
		lines = append(lines, strings.Join(second, " · "))
	}
	return strings.Join(lines, "\n")
}

func osc1337(body string) string { return "\x1b]1337;" + body + "\a" }

// wrap joins sequences, each in a tmux passthrough (ESC P tmux; … ESC \, its ESCs
// doubled) when inside tmux, which forwards them only with allow-passthrough on.
func wrap(seqs []string, tmux bool) []byte {
	var b strings.Builder
	for _, s := range seqs {
		if tmux {
			b.WriteString("\x1bPtmux;" + strings.ReplaceAll(s, "\x1b", "\x1b\x1b") + "\x1b\\")
		} else {
			b.WriteString(s)
		}
	}
	return []byte(b.String())
}

// Sanitize makes untrusted text (a session's title from a vendor's files) safe to show:
// control characters (C0, DEL, C1, so ESC and BEL too) and bidirectional overrides become
// spaces, runs of spaces one, invalid UTF-8 is dropped, and the result is cut to max
// characters (with "…").
func Sanitize(s string, max int) string {
	var b strings.Builder
	space := false
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		s = s[n:]
		if r == utf8.RuneError && n <= 1 {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || bidi(r) || unicode.IsSpace(r) {
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	out := []rune(b.String())
	if max > 0 && len(out) > max {
		return strings.TrimRight(string(out[:max-1]), " ") + "…"
	}
	return string(out)
}

// bidi reports the characters that reorder text: marks, embeddings, overrides, isolates.
func bidi(r rune) bool {
	return r == 0x061c || r == 0x200e || r == 0x200f || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
}
