package agent

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestMovementHookMergePreservesSettingsAndOtherHooks(t *testing.T) {
	f := JSONMovementHookFile("settings.json", "'/opt/bin/hopsesh' notice-hook --agent 'claude' --profile 'abc'")
	original := []byte(`{"large":9007199254740993,"permissions":{"allow":["Bash(git status)"]},"hooks":{"Stop":[{"hooks":[{"type":"command","command":"other"}]}],"SessionStart":[{"matcher":"resume","hooks":[{"type":"command","command":"keep"}]}]}}`)
	installed, err := f.Merge(original)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Has(installed) {
		t.Fatal("not installed")
	}
	again, err := f.Merge(installed)
	if err != nil || !bytes.Equal(again, installed) {
		t.Fatalf("non-idempotent: %s %v", again, err)
	}
	removed, err := f.Remove(installed)
	if err != nil {
		t.Fatal(err)
	}
	if f.Has(removed) {
		t.Fatal("not removed")
	}
	if !equalHookJSON(original, removed) {
		t.Fatalf("changed unrelated config: %s", removed)
	}
	if !strings.Contains(string(removed), "9007199254740993") {
		t.Fatal("rounded unrelated number")
	}
	again, err = f.Remove(removed)
	if err != nil || !bytes.Equal(again, removed) {
		t.Fatal("remove is not idempotent")
	}
}

func TestMovementHookRejectsMalformedAndPreservesEditedHandlers(t *testing.T) {
	f := JSONMovementHookFile("hooks.json", "hook")
	for _, bad := range []string{`null`, `[]`, `{`, `{"hooks":null}`, `{"hooks":[]}`, `{"hooks":{"SessionStart":{}}}`, `{"hooks":{"SessionStart":[null]}}`, `{"hooks":{"SessionStart":[{"hooks":false}]}}`} {
		if _, err := f.Merge([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
		if _, err := f.Remove([]byte(bad)); err == nil {
			t.Errorf("removed invalid %s", bad)
		}
	}
	// A changed timeout is user-owned; removing ours must leave it intact.
	original := []byte(`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"hook","timeout":7},{"type":"command","command":"other"}]}]}}`)
	b, err := f.Merge(original)
	if err != nil {
		t.Fatal(err)
	}
	b, err = f.Remove(b)
	if err != nil || !equalHookJSON(b, original) {
		t.Fatalf("lost edited/foreign hook: %s %v", b, err)
	}
}

func TestMovementHookCommandQuotesAndVersionGate(t *testing.T) {
	command, err := NoticeHookCommand("/opt/a b/it's $(touch nope)/hopsesh", "claude", "a'b")
	if err != nil || !strings.Contains(command, `'"'"'`) {
		t.Fatalf("quote %q %v", command, err)
	}
	if _, err = NoticeHookCommand("/bin/hopsesh\nother", "claude", ""); err == nil {
		t.Fatal("accepted control")
	}
	for _, v := range []string{"", "0.159.9", "0.160.0-alpha.1", "1.0.0", "0.160", "0.160.0junk"} {
		if HookVersionAtLeast(v, "0.160.0") {
			t.Errorf("accepted %s", v)
		}
	}
	for _, v := range []string{"0.160.0", "0.160.1", "0.161.0"} {
		if !HookVersionAtLeast(v, "0.160.0") {
			t.Errorf("rejected %s", v)
		}
	}
	b, _ := JSONMovementHookFile("x", command).Merge(nil)
	var doc map[string]any
	if json.Unmarshal(b, &doc) != nil {
		t.Fatal("bad JSON")
	}
}
