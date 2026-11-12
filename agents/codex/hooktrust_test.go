package codex

import (
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestHookTrustFromCodexHooksList(t *testing.T) {
	ours := `'/Applications/hopsesh.app/Contents/Resources/bin/hopsesh' notice-hook --agent 'codex'`
	list := func(entries string) [][]byte {
		return [][]byte{[]byte(`{"id":1,"result":{}}`), []byte(`{"method":"account/updated","params":{}}`),
			[]byte(`{"id":2,"result":{"data":[{"cwd":"/h","hooks":[` + entries + `]}]}}`)}
	}
	entry := func(event, command string, enabled bool, trust string) string {
		e := map[bool]string{true: "true", false: "false"}[enabled]
		return `{"eventName":"` + event + `","command":"` + jsonEscape(command) + `","enabled":` + e + `,"trustStatus":"` + trust + `"}`
	}
	for _, c := range []struct {
		name, entries, want string
	}{
		{"untrusted (the Review hooks prompt was dismissed)", entry("sessionStart", ours, true, "trusted") + "," + entry("userPromptSubmit", ours, true, "untrusted"), agent.HookNeedsReview},
		{"changed after trust", entry("userPromptSubmit", ours, true, "modified"), agent.HookNeedsReview},
		{"turned off in /hooks", entry("userPromptSubmit", ours, false, "trusted"), agent.HookDisabled},
		{"trusted", entry("sessionStart", ours, true, "trusted") + "," + entry("userPromptSubmit", ours, true, "trusted"), agent.HookTrusted},
		{"someone else's hook only", entry("sessionStart", "other", true, "untrusted"), agent.HookMissing},
	} {
		got := hookTrustFrom(list(c.entries), []string{ours}, "fix")
		if got.State != c.want {
			t.Fatalf("%s: got %+v, want %s", c.name, got, c.want)
		}
	}
	if got := hookTrustFrom([][]byte{[]byte(`{"id":2,"error":{"message":"unknown method"}}`)}, []string{ours}, "fix"); got.State != agent.HookUnknown {
		t.Fatalf("an older Codex without hooks/list must be unknown, got %+v", got)
	}
}

func jsonEscape(s string) string {
	out := ""
	for _, r := range s {
		if r == '"' || r == '\\' {
			out += `\`
		}
		out += string(r)
	}
	return out
}
