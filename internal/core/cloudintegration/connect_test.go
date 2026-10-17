package cloudintegration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testFingerprint = "abcdefghijklmnopqrstuvwxyz012345"

func TestConnectCodeRoundTripAndDamage(t *testing.T) {
	invitation := []byte(`{"schema":1,"ticket":"secret-value"}`)
	code, err := FormatConnectCode(testFingerprint, invitation)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(code, "secret-value") || strings.ContainsAny(code, "\r\n") {
		t.Fatal("connect code must be one encoded line")
	}
	fp, got, err := ParseConnectCode("  " + code + "\n")
	if err != nil || fp != testFingerprint || string(got) != string(invitation) {
		t.Fatalf("round trip: %q %q %v", fp, got, err)
	}
	for _, bad := range []string{
		"hopsesh-connect 1 " + testFingerprint,                         // truncated
		code[:len(code)-3] + "!!!",                                     // damaged payload
		"hopsesh-connect 2 " + strings.TrimPrefix(code, connectPrefix), // unknown version
		"hopsesh-connect 1 short " + strings.Fields(code)[3],           // bad fingerprint
	} {
		if _, _, err := ParseConnectCode(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if _, err := FormatConnectCode(testFingerprint, []byte("not json")); err == nil {
		t.Fatal("formatted an invalid invitation")
	}
}

func prompt(t *testing.T, event, text string) string {
	t.Helper()
	b, _ := json.Marshal(UserPrompt{Session: "native-session", Workspace: "/work", Event: event, Prompt: text})
	return string(b)
}

func TestUserPromptHookActsOnlyOnItsOwnMessages(t *testing.T) {
	code, _ := FormatConnectCode(testFingerprint, []byte(`{}`))
	cases := []struct {
		remote, input string
		ours, err     bool
	}{
		{"", prompt(t, "UserPromptSubmit", StatusPrompt), false, false}, // local: never acts
		{"true", prompt(t, "UserPromptSubmit", "please fix the tests"), false, false},
		{"true", prompt(t, "UserPromptSubmit", "what is hopsesh status?"), false, false},
		{"true", prompt(t, "UserPromptSubmit", "  "+StatusPrompt+"\n"), true, false},
		{"true", prompt(t, "UserPromptSubmit", code), true, false},
		{"true", prompt(t, "SessionStart", StatusPrompt), false, true},
		{"true", "{", false, true},
		{"true", prompt(t, "UserPromptSubmit", strings.Repeat("x", 70000)), false, false}, // large ordinary prompt
	}
	for i, c := range cases {
		_, ours, err := ReadUserPrompt(strings.NewReader(c.input), c.remote)
		if ours != c.ours || (err != nil) != c.err {
			t.Fatalf("case %d: ours=%v err=%v", i, ours, err)
		}
	}
}

func TestClaudeStartupInstallsPromptHookOnceAndExportOnlyWhenAsked(t *testing.T) {
	repo := startupRepository(t)
	plan, err := PlanRepository("claude-hosted", "0.5.0-alpha.1", DownloadOrigin, repo)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.ClaudeHookScript, "--allow-transcript-export") || plan.TranscriptExport {
		t.Fatal("export enabled without the owner's choice")
	}
	if err = plan.Apply(t.Context()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // a repeat install keeps exactly one hopsesh entry per event
		if plan, err = PlanRepository("claude-hosted", "0.5.0-alpha.1", DownloadOrigin, repo); err != nil {
			t.Fatal(err)
		}
		if err = plan.Apply(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	body, err := os.ReadFile(filepath.Join(repo, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Hooks map[string][]json.RawMessage `json:"hooks"`
	}
	if json.Unmarshal(body, &decoded) != nil || len(decoded.Hooks["SessionStart"]) != 1 || len(decoded.Hooks["UserPromptSubmit"]) != 1 {
		t.Fatalf("hooks: %s", body)
	}
	script, err := os.ReadFile(filepath.Join(repo, ".hopsesh", "cloud-session-connect.sh"))
	if err != nil || !strings.Contains(string(script), "cloud-integration connect-hook") || !strings.Contains(string(script), `"${CLAUDE_CODE_REMOTE:-}" = true`) {
		t.Fatalf("connect script: %s %v", script, err)
	}
	exported, err := PlanRepositoryWith("claude-hosted", "0.5.0-alpha.1", DownloadOrigin, repo, Options{AllowTranscriptExport: true})
	if err != nil || !exported.TranscriptExport || !strings.Contains(exported.ClaudeHookScript, "prepare --claude-hook --quiet --allow-transcript-export\n") {
		t.Fatalf("export option not applied: %v", err)
	}
	if _, err = PlanWith("codex-current", "0.5.0-alpha.1", DownloadOrigin, Options{AllowTranscriptExport: true}); err == nil {
		t.Fatal("transcript export enabled for Codex startup")
	}
}
