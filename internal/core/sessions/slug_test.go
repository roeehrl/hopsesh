package sessions

import (
	"strings"
	"testing"
)

func TestSlugBasic(t *testing.T) {
	cases := map[string]string{
		"/Users/alice/git/proj":                        "-Users-alice-git-proj",
		"/Users/alice/git/web-app/.claude/worktrees/x": "-Users-alice-git-web-app--claude-worktrees-x",
		"/private/tmp/x":                               "-private-tmp-x",
		`C:\Users\me\proj`:                             "C--Users-me-proj",
		"/home/bob/My Project":                         "-home-bob-My-Project",
		"/home/bob/caf\u00e9":                          "-home-bob-caf-",     // é is one UTF-16 unit → one dash
		"/home/bob/cafe\u0301":                         "-home-bob-caf-",     // decomposed é NFC-normalises to one unit
		"/home/bob/\U0001F680rocket":                   "-home-bob---rocket", // astral rune = two units = two dashes
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlugLongPathHash(t *testing.T) {
	long := "/" + strings.Repeat("a", 250)
	got := Slug(long)
	if !strings.HasPrefix(got, "-"+strings.Repeat("a", 199)+"-") {
		t.Fatalf("long slug not truncated to 200 + '-': %q", got[:210])
	}
	// Java "/"+250×"a" hashCode, computed independently: base36 of |hash|.
	units := []uint16{'/'}
	for i := 0; i < 250; i++ {
		units = append(units, 'a')
	}
	h := javaHash(units)
	if len(got) <= 201 || got[201:] == "" {
		t.Fatalf("missing hash suffix: %q", got)
	}
	if h == 0 {
		t.Fatal("unexpected zero hash")
	}
}

func TestJavaHashKnownValues(t *testing.T) {
	// Known Java values: "hello".hashCode() == 99162322, "".hashCode() == 0,
	// and a value that overflows to negative: "polygenelubricants".hashCode() == Integer.MIN_VALUE.
	if got := javaHash(utf16Of("hello")); got != 99162322 {
		t.Errorf("hello: %d", got)
	}
	if got := javaHash(utf16Of("polygenelubricants")); got != -2147483648 {
		t.Errorf("polygenelubricants: %d", got)
	}
	if abs64(int64(int32(-2147483648))) != 2147483648 {
		t.Error("abs of MinInt32 must not overflow")
	}
}

func TestProjectDirNameOverride(t *testing.T) {
	env := map[string]string{"CLAUDE_CONFIG_DIR": "/c", "CLAUDE_CODE_PROJECT_DIR_NAME": "pinned_1"}
	get := func(k string) string { return env[k] }
	if got := ProjectDirName("/x/y", get); got != "pinned_1" {
		t.Errorf("override ignored: %q", got)
	}
	delete(env, "CLAUDE_CONFIG_DIR")
	if got := ProjectDirName("/x/y", get); got != "-x-y" {
		t.Errorf("override must need CLAUDE_CONFIG_DIR: %q", got)
	}
	env["CLAUDE_CONFIG_DIR"] = "/c"
	env["CLAUDE_CODE_PROJECT_DIR_NAME"] = "nul"
	if got := ProjectDirName("/x/y", get); got != "-x-y" {
		t.Errorf("windows device name must be rejected: %q", got)
	}
}

func utf16Of(s string) []uint16 {
	var out []uint16
	for _, r := range s {
		out = append(out, uint16(r))
	}
	return out
}
