package cloudintegration

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexGuidancePreservesRepositoryEditsAcrossUpdates(t *testing.T) {
	repo := startupRepository(t)
	path := filepath.Join(repo, "AGENTS.md")
	original := []byte("# User rules\r\nPrivate repository instruction\r\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanRepository("codex-current", "0.5.0", DownloadOrigin, repo)
	if err != nil {
		t.Fatal(err)
	}
	review, _ := json.Marshal(plan)
	if bytes.Contains(review, []byte("Private repository instruction")) {
		t.Fatal("review leaked existing repository instructions")
	}
	if err = plan.Apply(t.Context()); err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(path)
	if err != nil || !bytes.HasPrefix(installed, original) {
		t.Fatal("repository content was changed", err)
	}
	for _, change := range plan.Changes {
		if change.Path == "AGENTS.md" && change.Action != "update" {
			t.Fatal("guidance update was not reviewable", change)
		}
	}
	// Owners may edit unrelated guidance after installation; the owned block is
	// the only content protected by the startup ownership hash.
	edited := append([]byte("# Added before\n"), installed...)
	edited = append(edited, []byte("\n# Added after\n")...)
	if err = os.WriteFile(path, edited, 0600); err != nil {
		t.Fatal(err)
	}
	plan, err = PlanRepository("codex-current", "0.5.1", DownloadOrigin, repo)
	if err != nil {
		t.Fatal(err)
	}
	if err = plan.Apply(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, edited) || bytes.Count(after, []byte(codexGuidanceBegin)) != 1 {
		t.Fatal("update altered unrelated content or duplicated guidance")
	}
	plan, err = PlanRepository("codex-current", "0.5.1", DownloadOrigin, repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range plan.Changes {
		if change.Action != "unchanged" {
			t.Fatal("repeat installation was not idempotent", change)
		}
	}
}

func TestCodexGuidanceRefusesAmbiguousOrChangedInstructions(t *testing.T) {
	for _, scenario := range []string{"edited-block", "removed-block", "duplicate", "missing-end", "reversed", "override", "large", "invalid-utf8", "stale-review", "new-override"} {
		t.Run(scenario, func(t *testing.T) {
			repo := startupRepository(t)
			plan, err := PlanRepository("codex-current", "0.5.0", DownloadOrigin, repo)
			if err != nil {
				t.Fatal(err)
			}
			if err = plan.Apply(t.Context()); err != nil {
				t.Fatal(err)
			}
			plan, err = PlanRepository("codex-current", "0.5.1", DownloadOrigin, repo)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(repo, "AGENTS.md")
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "edited-block":
				body = bytes.Replace(body, []byte("Skip this"), []byte("Do not skip this"), 1)
			case "removed-block":
				body = []byte("# Intentionally removed Hopsesh guidance\n")
			case "duplicate":
				body = append(body, body...)
			case "missing-end":
				body = bytes.Replace(body, []byte(codexGuidanceEnd), nil, 1)
			case "reversed":
				body = []byte(codexGuidanceEnd + "\n" + codexGuidanceBegin)
			case "override", "new-override":
				path, body = filepath.Join(repo, "AGENTS.override.md"), []byte("# Higher priority rules\n")
			case "large":
				body = append(body, bytes.Repeat([]byte("x"), 32*1024)...)
			case "invalid-utf8":
				body = append(body, 0xff)
			case "stale-review":
				body = append(body, []byte("# Concurrent user edit\n")...)
			}
			if err = os.WriteFile(path, body, 0600); err != nil {
				t.Fatal(err)
			}
			if scenario == "stale-review" || scenario == "new-override" {
				if err = plan.Apply(t.Context()); err == nil {
					t.Fatal("stale review installed startup instructions")
				}
				instruction, _ := os.ReadFile(filepath.Join(repo, ".hopsesh", "codex-start.md"))
				if strings.Contains(string(instruction), "v0.5.1") {
					t.Fatal("stale review partially updated helper version")
				}
			} else if _, err = PlanRepository("codex-current", "0.5.1", DownloadOrigin, repo); err == nil {
				t.Fatal("conflicting repository instructions accepted")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(body, after) {
				t.Fatal("refused update changed repository instructions")
			}
		})
	}
}

func TestCodexGuidanceRefusesLinkedInstructionFiles(t *testing.T) {
	for _, name := range []string{"AGENTS.md", "AGENTS.override.md"} {
		t.Run(name, func(t *testing.T) {
			repo := startupRepository(t)
			outside := filepath.Join(startupRepository(t), "rules.md")
			if err := os.WriteFile(outside, []byte("private rules"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(repo, name)); err != nil {
				t.Skip("symlinks unavailable", err)
			}
			if _, err := PlanRepository("codex-current", "0.5.0", DownloadOrigin, repo); err == nil {
				t.Fatal("linked repository instruction accepted")
			}
		})
	}
}
