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
