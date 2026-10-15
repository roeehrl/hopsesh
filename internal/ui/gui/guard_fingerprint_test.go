package gui

import "testing"

// "Not now" lasts while the same problems remain: the fingerprint ignores order and
// wording, and changes when an agent's problem changes or the command goes missing.
func TestProblemFingerprint(t *testing.T) {
	a := HookHealthDTO{CLIReady: true, Problems: []HookProblem{{Agent: "claude", State: "not-installed", Message: "x"}, {Agent: "codex", Profile: "p", State: "unsupported"}}}
	b := HookHealthDTO{CLIReady: true, Problems: []HookProblem{{Agent: "codex", Profile: "p", State: "unsupported", Message: "other words"}, {Agent: "claude", State: "not-installed"}}}
	if problemFingerprint(a) == "" || problemFingerprint(a) != problemFingerprint(b) {
		t.Fatal("same problems, different fingerprint")
	}
	c := b
	c.Problems = []HookProblem{{Agent: "codex", Profile: "p", State: "needs-review"}, {Agent: "claude", State: "not-installed"}}
	d := a
	d.CLIReady = false
	if problemFingerprint(c) == problemFingerprint(a) || problemFingerprint(d) == problemFingerprint(a) {
		t.Fatal("changed problems kept the fingerprint")
	}
	if problemFingerprint(HookHealthDTO{CLIReady: true}) != "" {
		t.Fatal("nothing to fix has no fingerprint")
	}
}
