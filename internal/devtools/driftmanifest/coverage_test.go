package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/agents/all"
)

// Exercise the serialized artifact the reviewer receives, not just the Go struct.
func TestProfileReviewCoverage(t *testing.T) {
	t.Chdir(filepath.Join("..", "..", ".."))
	mods := modules()
	b, err := json.Marshal(mods)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []module
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	for i, adapter := range all.Registry().All() {
		spec := adapter.Spec()
		m := decoded[i]
		if m.ID != spec.ID || !reflect.DeepEqual(m.Accounts, spec.Accounts) || m.DesktopScheme != spec.DesktopScheme {
			t.Fatalf("%s: manifest lost runtime policy", spec.ID)
		}
		if spec.Accounts == nil {
			continue
		}
		if len(m.IntegrationFiles) == 0 {
			t.Fatalf("%s: no shared consumers or regression evidence", spec.ID)
		}
		for _, path := range m.IntegrationFiles {
			if _, err := os.Stat(path); err != nil {
				t.Errorf("%s: stale integration path: %v", spec.ID, err)
			}
		}
		watch := agentWatch[spec.ID].Watch
		grep := regexp.MustCompile(watch.Grep)
		for _, env := range spec.Accounts.RootEnv {
			if !grep.MatchString(env) {
				t.Errorf("%s: root %s absent from upstream change filter", spec.ID, env)
			}
		}
		loginHelp := append(append([]string{string(spec.ID)}, spec.Accounts.Login...), "--help")
		found := false
		for _, command := range watch.Help {
			found = found || reflect.DeepEqual(command, loginHelp)
		}
		if !found {
			t.Errorf("%s: no login help probe for %v", spec.ID, loginHelp)
		}
	}
	keep := regexp.MustCompilePOSIX(agentWatch["codex"].Watch.Schema.Keep)
	for _, name := range []string{"GetAccountParams.json", "GetAccountResponse.json", "LoginAccountParams.json", "AccountUpdatedNotification.json", "ThreadResumeParams.json"} {
		if !keep.MatchString(name) {
			t.Errorf("review drops relied-on account/thread schema %s", name)
		}
	}
}

func TestReviewPrompts(t *testing.T) {
	for _, tool := range []string{"sh", "jq", "awk"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("prompt integration requires %s (mandatory in drift workflow)", tool)
		}
	}
	t.Chdir(filepath.Join("..", "..", ".."))
	mods := modules()
	targets, err := buildTargets(mods)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]any{"project": hopsesh(mods), "modules": mods, "targets": targets})
	if err != nil {
		t.Fatal(err)
	}
	intel := t.TempDir()
	if err := os.WriteFile(filepath.Join(intel, "manifest.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	for group, required := range map[string][]string{
		"local":             {"CLAUDE_CONFIG_DIR", "CODEX_SQLITE_HOME", "refreshToken:false", "codex://threads/<uuid>", "lineage/4", "binding epochs", "accounts.spec.ts", "multi-party"},
		"anthropic-cloud":   {"driver profile and binding", "causal receipts"},
		"openai-cloud":      {"CODEX_SQLITE_HOME", "stale plans"},
		"third-party-cloud": {"shared move and", "lineage"},
	} {
		result, err := exec.Command("sh", "ci/drift/prompt.sh", group, intel).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v: %s", group, err, result)
		}
		prompt := string(result)
		for _, token := range append(required, "integrationFiles", "schema/<target>/**/*.diff", "incomplete coverage") {
			if !strings.Contains(prompt, token) {
				t.Errorf("%s review omits %q", group, token)
			}
		}
		if strings.Contains(prompt, "{{") || strings.Contains(prompt, "module refuses to extend") {
			t.Errorf("%s: unexpanded template or obsolete storage limitation", group)
		}
	}
}
