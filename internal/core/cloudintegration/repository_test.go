package cloudintegration

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func startupRepository(t *testing.T) string {
	t.Helper()
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestStartupInstallationPreservesUnrelatedSettingsAndIsIdempotent(t *testing.T) {
	repo := startupRepository(t)
	if err := os.Mkdir(filepath.Join(repo, ".claude"), 0700); err != nil {
		t.Fatal(err)
	}
	settings := []byte(`{"env":{"PRIVATE_FIXTURE":"private-secret"},"permissions":{"allow":["Read"]},"hooks":{"SessionStart":[{"matcher":"startup","hooks":[{"type":"command","command":"user-owned-command"}]}],"Stop":[{"hooks":[{"type":"command","command":"other-hook"}]}]}}`)
	if err := os.WriteFile(filepath.Join(repo, ".claude", "settings.json"), settings, 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanRepository("claude-hosted", "0.5.0-alpha.1", DownloadOrigin, repo)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(plan)
	if strings.Contains(string(raw), "private-secret") || strings.Contains(string(raw), "user-owned-command") {
		t.Fatal("review exposed existing private settings")
	}
	if _, err = os.Stat(filepath.Join(repo, ".hopsesh")); !os.IsNotExist(err) {
		t.Fatal("preview wrote files")
	}
	if err = plan.Apply(t.Context()); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(repo, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"private-secret", "user-owned-command", "other-hook"} {
		if !bytes.Contains(body, []byte(want)) {
			t.Fatal("startup installation discarded unrelated configuration")
		}
	}
	var decoded struct {
		Hooks struct {
			SessionStart []json.RawMessage `json:"SessionStart"`
		} `json:"hooks"`
	}
	if json.Unmarshal(body, &decoded) != nil || len(decoded.Hooks.SessionStart) != 2 {
		t.Fatal("startup hook missing or duplicated")
	}
	plan, err = PlanRepository("claude-hosted", "0.5.0-alpha.1", DownloadOrigin, repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range plan.Changes {
		if change.Action != "unchanged" {
			t.Fatal("repeat install changed startup files", change)
		}
	}
	if err = plan.Apply(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Unrelated settings edits remain supported during a version update.
	var object map[string]any
	if err = json.Unmarshal(body, &object); err != nil {
		t.Fatal(err)
	}
	object["custom"] = "retained-edit"
	body, _ = json.Marshal(object)
	if err = os.WriteFile(filepath.Join(repo, ".claude", "settings.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	plan, err = PlanRepository("claude-hosted", "0.5.0-alpha.2", DownloadOrigin, repo)
	if err != nil {
		t.Fatal(err)
	}
	if err = plan.Apply(t.Context()); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(filepath.Join(repo, ".claude", "settings.json"))
	if !bytes.Contains(body, []byte("retained-edit")) {
		t.Fatal("update lost unrelated edit")
	}
	script, _ := os.ReadFile(filepath.Join(repo, ".hopsesh", "cloud-session-start.sh"))
	if !bytes.Contains(script, []byte("v0.5.0-alpha.2")) || !bytes.Contains(script, []byte("--quiet")) {
		t.Fatal("hook did not pin updated quiet helper")
	}
}

func TestStartupReviewRejectsConcurrentEditsAndDoesNotOverwriteForeignFiles(t *testing.T) {
	for _, scenario := range []string{"concurrent-settings", "foreign-script", "edited-script", "edited-hook"} {
		t.Run(scenario, func(t *testing.T) {
			repo := startupRepository(t)
			plan, err := PlanRepository("claude-hosted", "0.5.0", DownloadOrigin, repo)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "concurrent-settings" {
				if err = os.Mkdir(filepath.Join(repo, ".claude"), 0700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(repo, ".claude", "settings.json")
				if err = os.WriteFile(path, []byte(`{"user":"concurrent"}`), 0600); err != nil {
					t.Fatal(err)
				}
				if plan.Apply(t.Context()) == nil {
					t.Fatal("stale review overwrote settings")
				}
				if _, err = os.Stat(filepath.Join(repo, ".hopsesh")); !os.IsNotExist(err) {
					t.Fatal("stale review partially wrote files")
				}
				return
			}
			if scenario != "foreign-script" {
				if err = plan.Apply(t.Context()); err != nil {
					t.Fatal(err)
				}
			} else {
				if err = os.Mkdir(filepath.Join(repo, ".hopsesh"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(repo, ".hopsesh", "cloud-session-start.sh")
			body := []byte("user-owned script\n")
			if scenario == "edited-hook" {
				path = filepath.Join(repo, ".claude", "settings.json")
				body, _ = os.ReadFile(path)
				body = bytes.Replace(body, []byte("startup|resume|clear|compact|fork"), []byte("startup"), 1)
			}
			if err = os.WriteFile(path, body, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = PlanRepository("claude-hosted", "0.5.1", DownloadOrigin, repo); err == nil {
				t.Fatal("foreign or edited cloud startup configuration was overwritten")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(after, body) {
				t.Fatal("rejected startup install changed a user file")
			}
		})
	}
}

func TestCodexStartupExportsInstructionsWithoutInventingCallbackOrEnrollment(t *testing.T) {
	repo := startupRepository(t)
	plan, err := PlanRepository("codex-current", "0.5.0", DownloadOrigin, repo)
	if err != nil {
		t.Fatal(err)
	}
	if plan.CallbackSupported || plan.Connected || plan.Callback != "Repository guidance / Start skill" {
		t.Fatal("startup instructions became a guaranteed callback")
	}
	if err = plan.Apply(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(repo, ".claude")); !os.IsNotExist(err) {
		t.Fatal("Codex setup wrote Claude hooks")
	}
	body, err := os.ReadFile(filepath.Join(repo, ".hopsesh", "codex-start.md"))
	if err != nil || !bytes.Contains(body, []byte("actual task ID")) {
		t.Fatal("Codex instructions lost actual session binding", err)
	}
	if _, err = PlanRepository("work-cloud", "0.5.0", DownloadOrigin, repo); err == nil {
		t.Fatal("unsupported Work callback installed")
	}
	claude, err := PlanRepository("claude-hosted", "0.5.1", DownloadOrigin, repo)
	if err != nil {
		t.Fatal("independent providers could not share a repository", err)
	}
	if err = claude.Apply(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = PlanRepository("codex-current", "0.5.0", DownloadOrigin, repo); err != nil {
		t.Fatal("Claude startup update changed Codex file ownership", err)
	}
}

func TestStartupInstallationRefusesSymlinkEscape(t *testing.T) {
	repo := startupRepository(t)
	outside := startupRepository(t)
	if err := os.Symlink(outside, filepath.Join(repo, ".hopsesh")); err != nil {
		t.Skip("symlink unavailable", err)
	}
	if _, err := PlanRepository("claude-hosted", "0.5.0", DownloadOrigin, repo); err == nil {
		t.Fatal("startup preview followed repository escape")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("outside directory changed", err)
	}
}
