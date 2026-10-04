package claude

import (
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Only a link on claude.ai itself names a session: another host before or after it, a
// look-alike, a link inside another link's path or query, user info, a port, plain http or
// another path does not.
func TestLinksOnlyOnClaudeAI(t *testing.T) {
	const id = "session_01ABCDEFGHJKMNPQRSTVWXYZ01"
	for line, want := range map[string]string{
		"View: https://claude.ai/code/" + id + "?from=cli&m=0":          id,
		"(https://claude.ai/code/" + id + ")":                           id,
		"<https://Claude.AI/code/cse_01ABCDEFGHJKMNPQRSTVWXYZ01>.":      id,
		"View: https://claude.ai.evil.example/code/" + id:               "",
		"View: https://evilclaude.ai/code/" + id:                        "",
		"View: https://claude.ai-evil.example/code/" + id:               "",
		"View: https://evil.example/https://claude.ai/code/" + id:       "",
		"View: https://evil.example/?next=https://claude.ai/code/" + id: "",
		"View: http://evil.example/?next=https://claude.ai/code/" + id:  "",
		"View: evil.example/x=https://claude.ai/code/" + id:             "",
		"View: https://evil.example#https://claude.ai/code/" + id:       "",
		"View: https://user@claude.ai/code/" + id:                       "",
		"View: https://claude.ai@evil.example/code/" + id:               "",
		"View: https://claude.ai:8443/code/" + id:                       "",
		"View: http://claude.ai/code/" + id:                             "",
		"View: https://claude.ai/code/" + id + "/files":                 "",
		"View: https://claude.ai/settings/" + id:                        "",
		"View: https://claude.ai/code/not-a-session":                    "",
		"View: https://claude.ai%2eevil.example/code/" + id:             "",
		"View: https://claude.ai․evil.example/code/" + id:               "",
		"View: https://clаude.ai/code/" + id:/* a Cyrillic а */ "",
	} {
		got := ""
		for _, u := range agent.LinksIn(line, "claude.ai") {
			if s := sessionOf(u); s != "" {
				got = s
			}
		}
		if got != want {
			t.Errorf("%q: got %q, want %q", line, got, want)
		}
	}
	if got := teleportID("Resume with: claude --teleport " + id + "."); got != id {
		t.Errorf("teleport: %q", got)
	}
	if got := teleportID("Resume with: claude --teleport https://evil.example/" + id); got != "" {
		t.Errorf("a link after --teleport is not an id: %q", got)
	}
}
