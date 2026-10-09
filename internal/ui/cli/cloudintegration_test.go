package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/cloudintegration"
)

func TestCloudStartupCLIReviewsAndInstallsGuidanceWithoutIdentity(t *testing.T) {
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(t.TempDir(), "unused-state")
	t.Setenv("HOPSESH_STATE_DIR", state)
	for _, preview := range []bool{true, false} {
		cmd := cloudStartupInstallCmd()
		args := []string{repo, "--provider", "codex-current", "--version", "0.5.0"}
		if preview {
			args = append(args, "--dry-run")
		}
		cmd.SetArgs(args)
		var out bytes.Buffer
		cmd.SetOut(&out)
		if err = cmd.ExecuteContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		var result struct {
			cloudintegration.RepositorySetup
			Applied bool `json:"applied"`
		}
		if err = json.Unmarshal(out.Bytes(), &result); err != nil || result.Applied == preview || result.Connected {
			t.Fatal("incorrect startup review outcome", err)
		}
		found := false
		for _, change := range result.Changes {
			if change.Path == "AGENTS.md" && change.Action == "create" {
				found = true
			}
		}
		if !found {
			t.Fatal("review omitted repository guidance")
		}
		_, err = os.Stat(filepath.Join(repo, "AGENTS.md"))
		if preview && !os.IsNotExist(err) || !preview && err != nil {
			t.Fatal("preview/apply wrote unexpected repository state", err)
		}
		if _, err = os.Stat(state); !os.IsNotExist(err) {
			t.Fatal("repository setup created runtime or cloud identity state", err)
		}
	}
}

func TestCloudPrepareRetainsActualHookReasonAndManualOrigin(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOPSESH_STATE_DIR", filepath.Join(dir, "state"))
	t.Setenv("CLAUDE_CODE_REMOTE", "true")
	for _, source := range []string{"startup", "resume", "clear", "compact", "fork", "manual"} {
		cmd := cloudPrepareCmd()
		if source == "manual" {
			cmd.SetArgs([]string{"--provider", "codex-current", "--session", "native-test", "--workspace", dir})
		} else {
			body, _ := json.Marshal(cloudintegration.SessionStart{Event: "SessionStart", Source: source, Session: "native-test", Workspace: dir})
			cmd.SetIn(bytes.NewReader(body))
			cmd.SetArgs([]string{"--claude-hook"})
		}
		var output bytes.Buffer
		cmd.SetOut(&output)
		if err := cmd.ExecuteContext(t.Context()); err != nil {
			t.Fatal(source, err)
		}
		var incarnation cloudintegration.Incarnation
		if err := json.Unmarshal(output.Bytes(), &incarnation); err != nil {
			t.Fatal(err)
		}
		saved, err := cloudintegration.Load(t.Context(), incarnation.Directory)
		if err != nil || saved.Source != source || saved.ExportTranscript {
			t.Fatal("prepare discarded startup reason or granted export", source, saved.Source, err)
		}
	}
}
