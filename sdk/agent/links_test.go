package agent

import (
	"strings"
	"testing"
)

// Only links on the named host count: another host before or after it, a port, user info
// or plain http does not.
func TestLinksIn(t *testing.T) {
	text := strings.Join([]string{
		"Created: https://ampcode.com/threads/T-1.",
		"https://ampcode.com.evil.example/threads/T-2",
		"https://evil.example/https://ampcode.com/threads/T-3",
		"https://evil.example/?u=https://ampcode.com/threads/T-4",
		"https://user@ampcode.com/threads/T-5",
		"https://ampcode.com:8443/threads/T-6",
		"http://ampcode.com/threads/T-7",
		"https://evilampcode.com/threads/T-8",
		"(see https://AmpCode.com/threads/T-9)",
		"evil.example/x=https://ampcode.com/threads/T-10",
	}, "\n")
	var got []string
	for _, u := range LinksIn(text, "ampcode.com") {
		got = append(got, u.Path)
	}
	if want := "/threads/T-1 /threads/T-9"; strings.Join(got, " ") != want {
		t.Errorf("LinksIn = %q, want %q", strings.Join(got, " "), want)
	}
	if p := PathParts(LinksIn("https://github.com/a/b/pull/7/", "github.com")[0]); strings.Join(p, ",") != "a,b,pull,7" {
		t.Errorf("PathParts = %v", p)
	}
}

// A pasted link counts only on the named host, over https, without user info or a port;
// a link with no scheme is read as https.
func TestLinkOn(t *testing.T) {
	for s, want := range map[string]bool{
		"https://chatgpt.com/codex/tasks/task_e_1":          true,
		"  chatgpt.com/codex/tasks/task_e_1  ":              true,
		"https://ChatGPT.com/codex/tasks/task_e_1?tab=diff": true,
		"http://chatgpt.com/codex/tasks/task_e_1":           false,
		"https://chatgpt.com.evil.example/codex/tasks/x":    false,
		"https://evil.example/chatgpt.com/codex/tasks/x":    false,
		"https://user@chatgpt.com/codex/tasks/x":            false,
		"https://chatgpt.com:8443/codex/tasks/x":            false,
		"https://evilchatgpt.com/codex/tasks/x":             false,
		"javascript:chatgpt.com/codex":                      false,
		"https://chatgpt.com/a https://evil.example/":       false,
		"task_e_1": false,
		"":         false,
	} {
		if _, ok := LinkOn(s, "chatgpt.com"); ok != want {
			t.Errorf("LinkOn(%q) = %v, want %v", s, ok, want)
		}
	}
}
