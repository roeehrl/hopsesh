package termapp

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

func env(kv ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

// Rule 16: hostile titles (newlines, ESC, BEL, C1, bidi overrides, broken UTF-8, very
// long) come out as one plain line.
func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"Fix the parser":                            "Fix the parser",
		"line one\nline two\r\n":                    "line one line two",
		"\x1b]0;evil title\a then":                  "]0;evil title then",
		"\x1b]1337;SetUserVar=hopsesh_title=eA==\a": "]1337;SetUserVar=hopsesh_title=eA==",
		"csi \u009b31m red":                         "csi 31m red",
		"abc\u202edcba\u2066x\u2069":                "abc dcba x",
		"tab\there \x00 nul \x7f del":               "tab here nul del",
		"bad \xff\xfe utf8":                         "bad utf8",
		"   spaced   out   ":                        "spaced out",
	}
	for in, want := range cases {
		if got := Sanitize(in, 80); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("ab ", 100)
	got := Sanitize(long, 80)
	if utf8.RuneCountInString(got) > 80 || !strings.HasSuffix(got, "…") {
		t.Fatalf("%q", got)
	}
	// Whatever goes in, no control character comes out.
	ctl := regexp.MustCompile("[\x00-\x1f\x7f\u0080-\u009f\u202a-\u202e\u2066-\u2069]")
	for i := 0; i < 256; i++ {
		s := "a" + string(rune(i)) + "b" + string([]byte{byte(i)}) + "c"
		if out := Sanitize(s, 80); ctl.MatchString(out) || !utf8.ValidString(out) {
			t.Fatalf("Sanitize(%q) = %q", s, out)
		}
	}
}

var oscRe = regexp.MustCompile("\x1b\\]1337;([A-Za-z]+)=([^\a]*)\a")

// iTerm2 gets user variables (base64 of the sanitised values) and a badge whose format
// is hopsesh's constant text; WezTerm only the variables; other terminals nothing.
func TestSequences(t *testing.T) {
	l := Labels{Title: "Fix \x1b]0;pwned\a the\nparser", Agent: "Claude Code", Machine: "laptop"}
	if s := Sequences(l, env("TERM_PROGRAM", "Apple_Terminal")); s != nil {
		t.Fatalf("Terminal got %q", s)
	}
	if s := Sequences(l, env()); s != nil {
		t.Fatalf("no terminal got %q", s)
	}
	seq := string(Sequences(l, env("TERM_PROGRAM", "iTerm.app")))
	got := map[string]string{}
	for _, m := range oscRe.FindAllStringSubmatch(seq, -1) {
		if m[1] == "SetUserVar" {
			k, v, _ := strings.Cut(m[2], "=")
			b, err := base64.StdEncoding.DecodeString(v)
			if err != nil {
				t.Fatal(err)
			}
			got[k] = string(b)
		} else {
			b, err := base64.StdEncoding.DecodeString(m[2])
			if err != nil {
				t.Fatal(err)
			}
			got[m[1]] = string(b)
		}
	}
	want := map[string]string{"hopsesh_title": "Fix ]0;pwned the parser", "hopsesh_agent": "Claude Code", "hopsesh_machine": "laptop",
		"SetBadgeFormat": "\\(user.hopsesh_title)\n\\(user.hopsesh_agent) · \\(user.hopsesh_machine)"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if strings.Count(seq, "\x1b") != 4 || strings.Count(seq, "\a") != 4 {
		t.Fatalf("one ESC and one BEL per sequence: %q", seq)
	}
	// LC_TERMINAL survives tmux and ssh; inside tmux each sequence is a passthrough with
	// its ESCs doubled.
	tm := string(Sequences(l, env("LC_TERMINAL", "iTerm2", "TERM_PROGRAM", "tmux", "TMUX", "/tmp/tmux-501/default,1,0")))
	parts := strings.Split(strings.TrimSuffix(tm, "\x1b\\"), "\x1b\\")
	if len(parts) != 4 {
		t.Fatalf("%q", tm)
	}
	for _, p := range parts {
		if !strings.HasPrefix(p, "\x1bPtmux;\x1b\x1b]1337;") || !strings.HasSuffix(p, "\a") {
			t.Fatalf("passthrough: %q", p)
		}
	}
	wez := string(Sequences(l, env("TERM_PROGRAM", "WezTerm")))
	if strings.Contains(wez, "SetBadgeFormat") || strings.Count(wez, "SetUserVar=") != 3 {
		t.Fatalf("WezTerm: %q", wez)
	}
	clear := string(ClearSequences(env("TERM_PROGRAM", "iTerm.app")))
	if !strings.Contains(clear, "SetBadgeFormat=\a") || !strings.Contains(clear, "SetUserVar=hopsesh_title=\a") {
		t.Fatalf("clear: %q", clear)
	}
	if ClearSequences(env()) != nil {
		t.Fatal("cleared elsewhere")
	}
}

// Rule 17: a sign-in or shell tab gets a fixed label whatever it was given; a badge shows
// only the parts that are set.
func TestLabelsFor(t *testing.T) {
	l := Labels{Title: "secret prompt text", Agent: "Claude Code", Machine: "laptop"}
	if got := l.For(KindSignIn); got != (Labels{Title: "hopsesh · sign in"}) {
		t.Fatalf("%+v", got)
	}
	if got := l.For(KindShell); got != (Labels{Title: "hopsesh · shell"}) {
		t.Fatalf("%+v", got)
	}
	if got := (Labels{Title: "a\nb"}).For(KindSession); got.Title != "a b" {
		t.Fatalf("%+v", got)
	}
	if f := badgeFormat(Labels{Title: "x"}); f != `\(user.hopsesh_title)` {
		t.Fatal(f)
	}
}
